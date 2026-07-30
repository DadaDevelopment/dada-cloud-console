package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
)

// box-agent ingest webhook tests. fakeVerifier and newWebhookCtx are shared with
// webhooks_dadagent_test.go.

func boxAgentClaims() *auth.KeycloakClaims {
	return &auth.KeycloakClaims{Azp: "box-agent"}
}

// --- auth gate (no database) ---

func TestBoxAgentWebhook_AuthGate(t *testing.T) {
	h := &Handler{}
	cases := []struct {
		name       string
		authHeader string
		verifier   tokenVerifier
		want       int
	}{
		{"missing bearer", "", fakeVerifier{claims: boxAgentClaims()}, http.StatusUnauthorized},
		{"verify error", "Bearer bad", fakeVerifier{err: errors.New("nope")}, http.StatusUnauthorized},
		{"wrong client", "Bearer ok", fakeVerifier{claims: &auth.KeycloakClaims{Azp: "dada-agent"}}, http.StatusForbidden},
		{"unconfigured", "Bearer ok", nil, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		c, rec := newWebhookCtx(t, tc.authHeader, `{}`)
		h.boxAgentStatusWebhook(c, tc.verifier)
		if rec.Code != tc.want {
			t.Errorf("status webhook, %s: code = %d, want %d", tc.name, rec.Code, tc.want)
		}
		c2, rec2 := newWebhookCtx(t, tc.authHeader, `{}`)
		h.boxAgentSampleWebhook(c2, tc.verifier)
		if rec2.Code != tc.want {
			t.Errorf("sample webhook, %s: code = %d, want %d", tc.name, rec2.Code, tc.want)
		}
	}
}

// TestBoxAgentWebhook_RejectsUnknownStatus: the agent does not get to invent a
// phase. An unknown status is a 400 rather than a stored string, because the
// column's CHECK constraint would reject it anyway and a 400 names the cause.
func TestBoxAgentWebhook_RejectsUnknownStatus(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer ok", `{"instance_ref":"i-1","status":"Running"}`)
	h.boxAgentStatusWebhook(c, fakeVerifier{claims: boxAgentClaims()})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestBoxAgentWebhook_RequiresInstanceRef(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer ok", `{"status":"Ready"}`)
	h.boxAgentStatusWebhook(c, fakeVerifier{claims: boxAgentClaims()})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status without instance_ref: code = %d, want 400", rec.Code)
	}
	c2, rec2 := newWebhookCtx(t, "Bearer ok", `{"active":true}`)
	h.boxAgentSampleWebhook(c2, fakeVerifier{claims: boxAgentClaims()})
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("sample without instance_ref: code = %d, want 400", rec2.Code)
	}
}

// --- ingest (needs a database) ---

// seedBoxWithInstanceRef creates a project, a box environment and a box already
// carrying a runtime handle, which is the state the webhook expects to find.
func seedBoxWithInstanceRef(t *testing.T, pool *pgxpool.Pool, status models.BoxStatus) (projectID, boxID uuid.UUID, instanceRef string) {
	t.Helper()
	projectID = seedBoxFixture(t, pool)
	suffix := uuid.NewString()[:8]
	instanceRef = "i-" + suffix
	var envID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO environments (project_id, name, namespace, type, runtime)
		 VALUES ($1, $2, $3, 'dev', 'box') RETURNING id`,
		projectID, "wh-"+suffix, "wh-ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO boxes (project_id, environment_id, name, image, profile, status, instance_ref)
		 VALUES ($1, $2, $3, 'warm-v1', 'box-standard', $4, $5) RETURNING id`,
		projectID, envID, "wh-"+suffix, string(status), instanceRef,
	).Scan(&boxID); err != nil {
		t.Fatalf("seed box: %v", err)
	}
	return projectID, boxID, instanceRef
}

// TestBoxAgentStatusWebhook_TurnsBoxReadyWithCoordinates is the slice's second
// Verify line: a curled webhook moves the box to Ready and records the SSH
// coordinates that GET .../state then shows.
func TestBoxAgentStatusWebhook_TurnsBoxReadyWithCoordinates(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, boxID, ref := seedBoxWithInstanceRef(t, pool, models.BoxStatusBooting)

	body := `{"instance_ref":"` + ref + `","status":"Ready","node_ref":"host-3",` +
		`"ssh_host":"boxes.dada-tuda.ru","ssh_port":2222,"mcp_url":"https://boxes.dada-tuda.ru/mcp/box/1"}`
	c, rec := newWebhookCtx(t, "Bearer ok", body)
	h.boxAgentStatusWebhook(c, fakeVerifier{claims: boxAgentClaims()})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var (
		status, sshHost, mcpURL, nodeRef string
		sshPort                          *int
		expiresAt                        *string
		lastActive                       *string
	)
	if err := pool.QueryRow(context.Background(),
		`SELECT status, ssh_host, ssh_port, mcp_url, node_ref,
		        expires_at::text, last_active_at::text
		   FROM boxes WHERE id = $1`, boxID,
	).Scan(&status, &sshHost, &sshPort, &mcpURL, &nodeRef, &expiresAt, &lastActive); err != nil {
		t.Fatalf("read box: %v", err)
	}
	if status != string(models.BoxStatusReady) {
		t.Errorf("status = %q, want Ready", status)
	}
	if sshHost != "boxes.dada-tuda.ru" || sshPort == nil || *sshPort != 2222 {
		t.Errorf("ssh coordinates lost: host=%q port=%v", sshHost, sshPort)
	}
	if mcpURL == "" || nodeRef != "host-3" {
		t.Errorf("mcp_url/node_ref lost: %q / %q", mcpURL, nodeRef)
	}
	// The TTL clock starts when the box becomes usable, not when it was requested:
	// a customer must not be charged TTL for our own boot time.
	if expiresAt == nil {
		t.Error("expires_at not stamped on the Ready transition")
	}
	if lastActive == nil {
		t.Error("last_active_at not stamped on the Ready transition")
	}

	// And the state endpoint shows them.
	claims := godClaims(seedUser(t, pool))
	var boxName string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM boxes WHERE id = $1`, boxID).Scan(&boxName); err != nil {
		t.Fatalf("read name: %v", err)
	}
	cs, recs := newBoxCtx(t, http.MethodGet, "", boxParams(projectID, boxName), claims)
	h.GetBoxState(cs)
	if recs.Code != http.StatusOK {
		t.Fatalf("GetBoxState: code = %d; body=%s", recs.Code, recs.Body.String())
	}
	var state struct {
		Status  string `json:"status"`
		Ready   bool   `json:"ready"`
		Connect struct {
			SSHHost string `json:"ssh_host"`
			SSHPort int    `json:"ssh_port"`
			MCPURL  string `json:"mcp_url"`
		} `json:"connect"`
	}
	if err := json.Unmarshal(recs.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if !state.Ready || state.Connect.SSHHost != "boxes.dada-tuda.ru" || state.Connect.SSHPort != 2222 {
		t.Errorf("state did not surface the coordinates: %+v", state)
	}
}

// TestBoxAgentStatusWebhook_StaleTransitionIsIgnored: box-agent retries, so a
// callback WILL arrive after the box moved on. A stale report must not resurrect a
// deleted box — and must answer 200, not 4xx, or the agent retries it forever.
func TestBoxAgentStatusWebhook_StaleTransitionIsIgnored(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	_, boxID, ref := seedBoxWithInstanceRef(t, pool, models.BoxStatusDeleting)
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET status = 'Deleted', deleted_at = now() WHERE id = $1`, boxID); err != nil {
		t.Fatalf("tombstone: %v", err)
	}

	// A Deleted box is not even resolvable: the lookup excludes tombstones, so the
	// webhook 404s rather than reporting on a box that no longer exists.
	c, rec := newWebhookCtx(t, "Bearer ok", `{"instance_ref":"`+ref+`","status":"Ready"}`)
	h.boxAgentStatusWebhook(c, fakeVerifier{claims: boxAgentClaims()})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("report on a deleted box: code = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	// A live-but-tearing-down box is resolvable, and its stale Ready is dropped.
	_, boxID2, ref2 := seedBoxWithInstanceRef(t, pool, models.BoxStatusDeleting)
	c2, rec2 := newWebhookCtx(t, "Bearer ok", `{"instance_ref":"`+ref2+`","status":"Ready"}`)
	h.boxAgentStatusWebhook(c2, fakeVerifier{claims: boxAgentClaims()})
	if rec2.Code != http.StatusOK {
		t.Fatalf("stale transition: code = %d, want 200 (a 4xx would make the agent retry forever); body=%s",
			rec2.Code, rec2.Body.String())
	}
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM boxes WHERE id = $1`, boxID2).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != string(models.BoxStatusDeleting) {
		t.Errorf("status = %q; a stale Ready must not pull a box out of teardown", status)
	}
}

// TestBoxAgentWebhook_UnknownInstanceRefIs404: tenancy is resolved from
// instance_ref and never trusted from the agent, so a ref the platform does not
// know about gets a 404 and no information about which refs exist.
func TestBoxAgentWebhook_UnknownInstanceRefIs404(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}

	c, rec := newWebhookCtx(t, "Bearer ok", `{"instance_ref":"i-nope","status":"Ready"}`)
	h.boxAgentStatusWebhook(c, fakeVerifier{claims: boxAgentClaims()})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status with an unknown ref: code = %d, want 404", rec.Code)
	}
	c2, rec2 := newWebhookCtx(t, "Bearer ok", `{"instance_ref":"i-nope","active":true}`)
	h.boxAgentSampleWebhook(c2, fakeVerifier{claims: boxAgentClaims()})
	if rec2.Code != http.StatusNotFound {
		t.Errorf("sample with an unknown ref: code = %d, want 404", rec2.Code)
	}
}

// TestBoxAgentSampleWebhook_ActiveRefreshesIdleClock_InactiveDoesNot. Idleness is
// the ABSENCE of activity, so an inactive sample must not be written as an
// activity event — otherwise a sleeping box would look busy to the reaper and
// "idle is not billed" would quietly stop being true.
func TestBoxAgentSampleWebhook_ActiveRefreshesIdleClock_InactiveDoesNot(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	_, boxID, ref := seedBoxWithInstanceRef(t, pool, models.BoxStatusReady)

	// Plant an old activity mark so a "no update" is distinguishable from "set to now".
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET last_active_at = now() - INTERVAL '2 hours' WHERE id = $1`, boxID); err != nil {
		t.Fatalf("plant last_active_at: %v", err)
	}

	c, rec := newWebhookCtx(t, "Bearer ok",
		`{"instance_ref":"`+ref+`","active":false,"sample":{"cpu_pct":0.1,"egress_bytes":0}}`)
	h.boxAgentSampleWebhook(c, fakeVerifier{claims: boxAgentClaims()})
	if rec.Code != http.StatusOK {
		t.Fatalf("inactive sample: code = %d; body=%s", rec.Code, rec.Body.String())
	}
	var idleAgeSeconds float64
	var sampleCPU *float64
	if err := pool.QueryRow(context.Background(),
		`SELECT EXTRACT(EPOCH FROM (now() - last_active_at)),
		        (last_sample_json->>'cpu_pct')::float8
		   FROM boxes WHERE id = $1`, boxID,
	).Scan(&idleAgeSeconds, &sampleCPU); err != nil {
		t.Fatalf("read box: %v", err)
	}
	if idleAgeSeconds < 3000 {
		t.Errorf("last_active_at moved on an INACTIVE sample (idle age %.0fs); idleness is the absence of activity", idleAgeSeconds)
	}
	if sampleCPU == nil || *sampleCPU != 0.1 {
		t.Errorf("sample blob not stored verbatim: %v", sampleCPU)
	}

	c2, rec2 := newWebhookCtx(t, "Bearer ok", `{"instance_ref":"`+ref+`","active":true,"sample":{"cpu_pct":42}}`)
	h.boxAgentSampleWebhook(c2, fakeVerifier{claims: boxAgentClaims()})
	if rec2.Code != http.StatusOK {
		t.Fatalf("active sample: code = %d; body=%s", rec2.Code, rec2.Body.String())
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT EXTRACT(EPOCH FROM (now() - last_active_at)) FROM boxes WHERE id = $1`, boxID,
	).Scan(&idleAgeSeconds); err != nil {
		t.Fatalf("read box: %v", err)
	}
	if idleAgeSeconds > 60 {
		t.Errorf("last_active_at did not move on an ACTIVE sample (idle age %.0fs)", idleAgeSeconds)
	}
}
