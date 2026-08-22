package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedGitWrittenAgent inserts the row the git-watcher writes for an agent that
// lives in an app's resources.values.yaml as a raw kagent CR: kind Agent, phase
// Unknown, summary carrying the commit it was read from. No console operation
// ever produced it.
func seedGitWrittenAgent(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, name string) {
	t.Helper()
	summary, err := json.Marshal(map[string]any{
		"git_sha":  "0123456789abcdef",
		"kind":     "Agent",
		"name":     name,
		"app_name": "kagent",
	})
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'Agent', $3, 'Unknown', $4)`,
		projectID, envID, name, summary,
	); err != nil {
		t.Fatalf("seed git-written agent: %v", err)
	}
	t.Cleanup(func() { dropSeededAudit(pool, managedAgentKind, name) })
}

func newAgentCtx(t *testing.T, method string, params gin.Params, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, "/", nil)
	c.Params = params
	auth.SetClaims(c, claims)
	return c, rec
}

// TestListAgents_ShowsAgentsWrittenIntoGitByHand is the point of the git reader:
// the console lists what the repository holds, and the repository holds
// telemost-poc, reels-poc and poc-echo as raw kagent Agent CRs that predate the
// ManagedAgent claim. Listing only claims made the console answer "no agents"
// about a cluster running three of them.
func TestListAgents_ShowsAgentsWrittenIntoGitByHand(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "telemost-poc-" + uuid.NewString()[:8]
	seedGitWrittenAgent(t, pool, projectID, envID, name)

	c, rec := newAgentCtx(t, http.MethodGet, params(projectID, envID), claims)
	h.ListAgents(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Agents []models.ResourceSnapshot `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode agents: %v", err)
	}
	for _, a := range body.Agents {
		if a.Name == name {
			if a.Kind != adoptedAgentKind {
				t.Fatalf("kind = %q, want %q so the console can tell a claim from a hand-written CR", a.Kind, adoptedAgentKind)
			}
			return
		}
	}
	t.Fatalf("agent %s is in git and missing from the list: %s", name, rec.Body.String())
}

// TestSaveAgent_RefusesToClaimAHandWrittenAgent keeps the list from becoming a
// lever that breaks the runtime: a ManagedAgent named after an existing raw
// Agent composes a SECOND CR with that name into the kagent namespace, and the
// two owners then fight over it.
func TestSaveAgent_RefusesToClaimAHandWrittenAgent(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "telemost-poc-" + uuid.NewString()[:8]
	seedGitWrittenAgent(t, pool, projectID, envID, name)

	c, rec := newCreateCtx(t,
		`{"name":"`+name+`","prompt":"Be brief."}`, params(projectID, envID), claims)
	h.SaveAgent(c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}

	var ops int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM operations WHERE project_id = $1 AND resource_name = $2`,
		projectID, name,
	).Scan(&ops); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if ops != 0 {
		t.Fatalf("a refused save queued %d operations", ops)
	}
}

// TestDeleteAgent_RefusesToDeleteAHandWrittenAgent covers the other half: the
// console has no git path to remove that agent from, so the delete would commit
// nothing and report success.
func TestDeleteAgent_RefusesToDeleteAHandWrittenAgent(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "telemost-poc-" + uuid.NewString()[:8]
	seedGitWrittenAgent(t, pool, projectID, envID, name)

	p := append(params(projectID, envID), gin.Param{Key: "name", Value: name})
	c, rec := newAgentCtx(t, http.MethodDelete, p, claims)
	h.DeleteAgent(c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}
