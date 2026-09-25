package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/promptsource"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeAgentRepo is a GitHub API stand-in serving one agent directory whose
// contents and head sha the test moves between calls.
type fakeAgentRepo struct {
	sha   string
	files map[string]string
	calls map[string]int
}

func (f *fakeAgentRepo) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls[strings.SplitN(strings.TrimPrefix(r.URL.Path, "/repos/acme/agents/"), "/", 2)[0]]++
		switch {
		case r.URL.Path == "/repos/acme/agents/commits/main":
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": f.sha})
		case strings.HasPrefix(r.URL.Path, "/repos/acme/agents/git/trees/"):
			var tree []map[string]any
			for p := range f.files {
				tree = append(tree, map[string]any{"path": p, "type": "blob", "sha": "blob:" + p})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": tree})
		case strings.HasPrefix(r.URL.Path, "/repos/acme/agents/git/blobs/blob:"):
			p := strings.TrimPrefix(r.URL.Path, "/repos/acme/agents/git/blobs/blob:")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"content": base64.StdEncoding.EncodeToString([]byte(f.files[p])), "encoding": "base64"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	old := promptsource.APIBase
	promptsource.APIBase = srv.URL
	t.Cleanup(func() { promptsource.APIBase = old; srv.Close() })
	return srv
}

func seedManagedAgentSnapshot(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, name string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'ManagedAgent', $3, 'Ready', '{}')`, projectID, envID, name); err != nil {
		t.Fatalf("seed managed agent: %v", err)
	}
	t.Cleanup(func() { dropSeededAudit(pool, managedAgentKind, name) })
}

func promptSourceCtx(t *testing.T, method string, projectID, envID uuid.UUID, name string, body any, userID uuid.UUID) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	c, rec := newAgentCtx(t, method, append(params(projectID, envID), gin.Param{Key: "name", Value: name}), godClaims(userID))
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	c.Request = httptest.NewRequest(method, "/", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, rec
}

func stubPromptSourceMint(t *testing.T) {
	t.Helper()
	old := promptSourceMintToken
	promptSourceMintToken = func(ctx context.Context, appID, pem string, installationID int64, repos []string) (string, time.Time, error) {
		return "tok-" + repos[0], time.Now().Add(time.Hour), nil
	}
	promptSourceTokens.tokens = map[installTokenKey]cachedInstallToken{}
	t.Cleanup(func() { promptSourceMintToken = old })
}

const promptSourceCore = "# Roman \u00b7 2026-09-16.native.46\n\nYou are Roman.\n"

func TestAgentPromptSourceLifecycle(t *testing.T) {
	pool := testOptimisticPool(t)
	stubPromptSourceMint(t)
	org := "prompt-source-org-" + uuid.NewString()[:8]
	projectID := seedInstallBindProject(t, pool, org)
	seedInstallation(t, pool, projectID, org, "acme", 7700001)
	var envID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+uuid.NewString()[:8]).Scan(&envID); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	userID := seedUser(t, pool)
	agent := "roman-" + uuid.NewString()[:8]
	h := &Handler{pool: pool, cfg: &config.Config{GithubAppID: "1", GithubAppPrivateKey: "pem"}}
	repo := &fakeAgentRepo{sha: "aaaa111", calls: map[string]int{}, files: map[string]string{
		"agents/roman/core.md":            promptSourceCore,
		"agents/roman/domains/deposit.md": "# Deposit\n",
		"agents/roman/experiments/x.md":   "ignored",
	}}
	repo.serve(t)
	body := setAgentPromptSourceRequest{RepoFullName: "acme/agents", Path: "agents/roman"}

	t.Run("refuses an agent that is not a console claim yet", func(t *testing.T) {
		c, rec := promptSourceCtx(t, http.MethodPut, projectID, envID, agent, body, userID)
		h.SetAgentPromptSource(c)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	seedManagedAgentSnapshot(t, pool, projectID, envID, agent)

	t.Run("attach syncs and queues a prompt-only update", func(t *testing.T) {
		c, rec := promptSourceCtx(t, http.MethodPut, projectID, envID, agent, body, userID)
		h.SetAgentPromptSource(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Source AgentPromptSource           `json:"source"`
			Sync   syncAgentPromptSourceResult `json:"sync"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Sync.Error != "" || !out.Sync.Changed || out.Sync.Files != 2 {
			t.Fatalf("sync = %+v", out.Sync)
		}
		if out.Source.LastSyncStatus != "ok" || out.Source.ResolvedSHA != "aaaa111" || out.Source.PromptVersion != "2026-09-16.native.46" || out.Source.Ref != "main" {
			t.Fatalf("source = %+v", out.Source)
		}
		if len(out.Source.Skills) != 1 || out.Source.Skills[0].Name != "deposit" || out.Source.Skills[0].Bytes != len("# Deposit\n") {
			t.Fatalf("skills = %+v", out.Source.Skills)
		}
		var payloadJSON []byte
		var action string
		if err := pool.QueryRow(context.Background(),
			`SELECT action, payload FROM operations WHERE id = $1`, out.Sync.OperationID).Scan(&action, &payloadJSON); err != nil {
			t.Fatalf("operation: %v", err)
		}
		var payload models.SaveAgentPayload
		_ = json.Unmarshal(payloadJSON, &payload)
		if action != "UpdateAgent" || payload.Name != agent || payload.Prompt != promptSourceCore || payload.PromptVersion != "2026-09-16.native.46" {
			t.Fatalf("payload = %+v", payload)
		}
		if payload.ModelConfig != "" || len(payload.Tools) != 0 || payload.Runtime != "" {
			t.Fatalf("a sync must not restate model, runtime or tools: %+v", payload)
		}
	})

	t.Run("unchanged head is a no-op", func(t *testing.T) {
		before := repo.calls["git"]
		c, rec := promptSourceCtx(t, http.MethodPost, projectID, envID, agent, nil, userID)
		h.SyncAgentPromptSource(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"changed":true`) {
			t.Fatalf("unchanged sha must not re-sync: %s", rec.Body.String())
		}
		if repo.calls["git"] != before {
			t.Fatalf("unchanged sha must not fetch the tree")
		}
	})

	t.Run("saveAgent refuses a prompt that differs from the synced one", func(t *testing.T) {
		c, rec := saveAgentCtx(t, projectID, envID, userID, saveAgentRequest{Name: agent, Prompt: "hand edited", PromptVersion: "hand", ModelConfig: "gpt"})
		h.SaveAgent(c)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out["code"] != "prompt_owned_by_source" || out["repo_full_name"] != "acme/agents" || out["prompt_version"] != "2026-09-16.native.46" {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("saveAgent with the synced prompt passes and keeps the source version", func(t *testing.T) {
		c, rec := saveAgentCtx(t, projectID, envID, userID, saveAgentRequest{Name: agent, Prompt: promptSourceCore, PromptVersion: "hand", ModelConfig: "gpt"})
		h.SaveAgent(c)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Operation models.Operation `json:"operation"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		var payload models.SaveAgentPayload
		_ = json.Unmarshal(out.Operation.Payload, &payload)
		if payload.Prompt != promptSourceCore || payload.PromptVersion != "2026-09-16.native.46" || payload.ModelConfig != "gpt" {
			t.Fatalf("payload = %+v", payload)
		}
	})

	t.Run("saveAgent without a prompt passes and carries the synced one", func(t *testing.T) {
		c, rec := saveAgentCtx(t, projectID, envID, userID, saveAgentRequest{Name: agent, ModelConfig: "gpt"})
		h.SaveAgent(c)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Operation models.Operation `json:"operation"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		var payload models.SaveAgentPayload
		_ = json.Unmarshal(out.Operation.Payload, &payload)
		if payload.Prompt != promptSourceCore || payload.PromptVersion != "2026-09-16.native.46" {
			t.Fatalf("payload = %+v", payload)
		}
	})

	t.Run("a broken commit is reported once and the last good prompt stays", func(t *testing.T) {
		repo.sha = "bbbb222"
		repo.files["agents/roman/domains/big.md"] = strings.Repeat("x", 9000)
		h.RunAgentPromptSourceTick(context.Background())
		row, _, err := h.loadPromptSource(context.Background(), projectID, envID, agent)
		if err != nil {
			t.Fatal(err)
		}
		if row.LastSyncStatus != "error" || !strings.Contains(row.LastSyncError, "big.md") || row.ResolvedSHA != "aaaa111" || row.CheckedSHA != "bbbb222" || row.Prompt != promptSourceCore {
			t.Fatalf("row = %+v", row)
		}
		fetches, failures := repo.calls["git"], countPromptSourceAudit(t, pool, projectID, agent, auditOutcomeFailure)
		h.RunAgentPromptSourceTick(context.Background())
		if repo.calls["git"] != fetches {
			t.Fatalf("an already examined broken commit must not be fetched again")
		}
		if got := countPromptSourceAudit(t, pool, projectID, agent, auditOutcomeFailure); got != failures {
			t.Fatalf("an already examined broken commit must not add audit failures: %d -> %d", failures, got)
		}
		row, _, _ = h.loadPromptSource(context.Background(), projectID, envID, agent)
		if row.LastSyncStatus != "error" || row.ResolvedSHA != "aaaa111" {
			t.Fatalf("row = %+v", row)
		}
	})

	t.Run("the poller picks up the fix", func(t *testing.T) {
		delete(repo.files, "agents/roman/domains/big.md")
		repo.sha = "cccc333"
		repo.files["agents/roman/core.md"] = "# Roman \u00b7 2026-09-17.native.47\n\nYou are Roman, v47.\n"
		h.RunAgentPromptSourceTick(context.Background())
		row, _, err := h.loadPromptSource(context.Background(), projectID, envID, agent)
		if err != nil {
			t.Fatal(err)
		}
		if row.LastSyncStatus != "ok" || row.ResolvedSHA != "cccc333" || row.PromptVersion != "2026-09-17.native.47" {
			t.Fatalf("row = %+v", row)
		}
		var n int
		_ = pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM operations WHERE project_id = $1 AND resource_name = $2 AND action = 'UpdateAgent' AND actor_id = $3`,
			projectID, agent, systemDeployActorID).Scan(&n)
		if n != 1 {
			t.Fatalf("poller must queue one update under the system actor, got %d", n)
		}
	})

	t.Run("a concurrent sync of the same agent is refused", func(t *testing.T) {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := tx.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtext($1))`,
			projectID.String()+"/"+envID.String()+"/"+agent); err != nil {
			t.Fatal(err)
		}
		c, rec := promptSourceCtx(t, http.MethodPost, projectID, envID, agent, nil, userID)
		h.SyncAgentPromptSource(c)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "sync_in_progress") {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("the installation cannot be removed while a source points at it", func(t *testing.T) {
		_, err := pool.Exec(context.Background(), `DELETE FROM git_app_installations WHERE org_id = $1`, org)
		if err == nil || !strings.Contains(err.Error(), "agent_prompt_sources") {
			t.Fatalf("delete must be refused by the foreign key, got %v", err)
		}
	})

	t.Run("the same agent name in another project cannot attach", func(t *testing.T) {
		otherProject := seedInstallBindProject(t, pool, org)
		var otherEnv uuid.UUID
		if err := pool.QueryRow(context.Background(),
			`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
			otherProject, "ns-"+uuid.NewString()[:8]).Scan(&otherEnv); err != nil {
			t.Fatal(err)
		}
		seedManagedAgentSnapshot(t, pool, otherProject, otherEnv, agent)
		c, rec := promptSourceCtx(t, http.MethodPut, otherProject, otherEnv, agent, body, userID)
		h.SetAgentPromptSource(c)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "agent_name_attached_elsewhere") {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a repository name outside owner/name is refused", func(t *testing.T) {
		c, rec := promptSourceCtx(t, http.MethodPut, projectID, envID, agent, setAgentPromptSourceRequest{RepoFullName: "acme/agents/nested"}, userID)
		h.SetAgentPromptSource(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("detach frees the prompt", func(t *testing.T) {
		c, rec := promptSourceCtx(t, http.MethodDelete, projectID, envID, agent, nil, userID)
		h.DeleteAgentPromptSource(c)
		if c.Writer.Status() != http.StatusNoContent {
			t.Fatalf("status = %d body = %s", c.Writer.Status(), rec.Body.String())
		}
		c, rec = promptSourceCtx(t, http.MethodGet, projectID, envID, agent, nil, userID)
		h.GetAgentPromptSource(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
		if _, owned, err := h.promptSourceOverride(context.Background(), projectID, envID, agent); owned || err != nil {
			t.Fatalf("override must be gone after detach: owned=%v err=%v", owned, err)
		}
	})

	t.Run("deleting the agent drops its source", func(t *testing.T) {
		c, rec := promptSourceCtx(t, http.MethodPut, projectID, envID, agent, body, userID)
		h.SetAgentPromptSource(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("re-attach status = %d body = %s", rec.Code, rec.Body.String())
		}
		c, rec = promptSourceCtx(t, http.MethodDelete, projectID, envID, agent, nil, userID)
		h.DeleteAgent(c)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		if _, found, err := h.loadPromptSource(context.Background(), projectID, envID, agent); found || err != nil {
			t.Fatalf("source row must go with the agent: found=%v err=%v", found, err)
		}
	})
}

func saveAgentCtx(t *testing.T, projectID, envID, userID uuid.UUID, req saveAgentRequest) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	c, rec := newAgentCtx(t, http.MethodPost, params(projectID, envID), godClaims(userID))
	reqBody, _ := json.Marshal(req)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(reqBody))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, rec
}

func countPromptSourceAudit(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, agent, outcome string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_events WHERE project_id = $1 AND resource_name = $2 AND action = 'SyncAgentPromptSource' AND outcome = $3`,
		projectID, agent, outcome).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAgentPromptSourcePermissions(t *testing.T) {
	pool := testOptimisticPool(t)
	org := "prompt-source-perm-" + uuid.NewString()[:8]
	projectID := seedInstallBindProject(t, pool, org)
	var envID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+uuid.NewString()[:8]).Scan(&envID); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	userID := seedUser(t, pool)
	h := &Handler{pool: pool, cfg: &config.Config{GithubAppID: "1", GithubAppPrivateKey: "pem"}}
	agent := "roman-" + uuid.NewString()[:8]
	body := setAgentPromptSourceRequest{RepoFullName: "acme/agents"}
	nobody := &auth.Claims{UserID: userID}
	reader := &auth.Claims{UserID: userID, Groups: []string{"/orgs/" + org + "/projects/" + projectID.String() + "/ReadOnly"}}

	call := func(t *testing.T, claims *auth.Claims, method string, handler func(*gin.Context), reqBody any) int {
		t.Helper()
		c, _ := newAgentCtx(t, method, append(params(projectID, envID), gin.Param{Key: "name", Value: agent}), claims)
		var payload []byte
		if reqBody != nil {
			payload, _ = json.Marshal(reqBody)
		}
		c.Request = httptest.NewRequest(method, "/", bytes.NewReader(payload))
		c.Request.Header.Set("Content-Type", "application/json")
		handler(c)
		return c.Writer.Status()
	}

	for name, tc := range map[string]struct {
		method  string
		handler func(*gin.Context)
		body    any
		reader  int
	}{
		"get":    {http.MethodGet, h.GetAgentPromptSource, nil, http.StatusNotFound},
		"set":    {http.MethodPut, h.SetAgentPromptSource, body, http.StatusForbidden},
		"sync":   {http.MethodPost, h.SyncAgentPromptSource, nil, http.StatusForbidden},
		"delete": {http.MethodDelete, h.DeleteAgentPromptSource, nil, http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			if got := call(t, nobody, tc.method, tc.handler, tc.body); got != http.StatusNotFound {
				t.Fatalf("non-member: status = %d, want 404", got)
			}
			if got := call(t, reader, tc.method, tc.handler, tc.body); got != tc.reader {
				t.Fatalf("read-only member: status = %d, want %d", got, tc.reader)
			}
		})
	}
}
