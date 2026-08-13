package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ginTestContextWithHeader builds a bare gin.Context carrying the given
// X-Dada-Client value (or none, for "") through clientClaimMiddleware, the
// same middleware the router mounts on the authenticated /api/v1 group. Tests
// use its Request.Context() with h.recordAudit directly, which is exactly
// what a handler's own c.Request.Context() carries once the middleware has
// run -- so this is not a shortcut around the real code path, just a way to
// reach it without building a full multipart upload request each time.
func ginTestContextWithHeader(t *testing.T, clientHeader string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/noop", nil)
	if clientHeader != "" {
		req.Header.Set(headerClientClaimed, clientHeader)
	}
	c.Request = req
	clientClaimMiddleware()(c)
	return c, rec
}

// TestClassifyClientClaimed is a DB-free unit test of the X-Dada-Client
// allowlist: the CLI's whole contribution to the deploy kill-criterion is
// this header, so its parsing needs coverage independent of any handler.
func TestClassifyClientClaimed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"absent header defaults to ui", "", clientClaimedUI},
		{"cli with version", "cli/1.2.3", clientClaimedCLI},
		{"bare cli", "cli", clientClaimedCLI},
		{"case insensitive", "CLI/1.0.0", clientClaimedCLI},
		{"api caller", "api", clientClaimedAPI},
		{"webhook caller", "webhook/1", clientClaimedWebhook},
		{"garbage collapses to unknown", "<script>alert(1)</script>", clientClaimedUnknown},
		{"unrelated word", "chrome", clientClaimedUnknown},
		{"oversized value", "cli/" + string(make([]byte, 64)), clientClaimedUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyClientClaimed(tc.raw); got != tc.want {
				t.Errorf("classifyClientClaimed(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestClassifyAgentSessionClaimed covers the second header: it must stay
// distinguishable between "no marker sent" (empty string) and "a marker was
// sent but did not fit the allowlist" (unknown), because those are different
// facts for the agentic-call side of the kill-criterion.
func TestClassifyAgentSessionClaimed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"absent header", "", ""},
		{"plain token", "claude-code-9f3a", "claude-code-9f3a"},
		{"uuid-shaped", "550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440000"},
		{"whitespace only collapses to absent", "   ", ""},
		{"embedded spaces are unknown", "not a token", clientClaimedUnknown},
		{"oversized is unknown", string(make([]byte, 65)), clientClaimedUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyAgentSessionClaimed(tc.raw); got != tc.want {
				t.Errorf("classifyAgentSessionClaimed(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestWriteAuditRowCarriesClientClaim proves the header reaches the actual
// audit_events row through the same path UploadSourceArchive's own
// recordAudit calls use, without UploadSourceArchive itself needing to know
// about client_claimed at all -- the point of centralizing the read in
// writeAuditRow instead of copy-pasting header parsing into every handler.
func TestWriteAuditRowCarriesClientClaim(t *testing.T) {
	pool := testAuditPool(t)
	actorID, projectID := seedAuditActor(t, pool)

	h := &Handler{pool: pool}
	envID := uuid.New()

	run := func(t *testing.T, action, header string) map[string]any {
		t.Helper()
		ginCtx, _ := ginTestContextWithHeader(t, header)
		h.recordAudit(ginCtx.Request.Context(), actorID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        action,
			ResourceKind:  "Build",
			ResourceName:  "acme",
		})
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE action = $1`, action)
		})

		var metaJSON []byte
		if err := pool.QueryRow(context.Background(),
			`SELECT metadata FROM audit_events WHERE action = $1 AND actor_id = $2`,
			action, actorID,
		).Scan(&metaJSON); err != nil {
			t.Fatalf("read written audit row: %v", err)
		}
		var meta map[string]any
		if err := json.Unmarshal(metaJSON, &meta); err != nil {
			t.Fatalf("unmarshal stored metadata: %v", err)
		}
		return meta
	}

	t.Run("cli header reaches the audit row", func(t *testing.T) {
		meta := run(t, "ClientClaimCLITest"+uuid.NewString()[:8], "cli/0.1.0")
		if meta[auditClientMetaKey] != clientClaimedCLI {
			t.Errorf("client_claimed = %v, want %q", meta[auditClientMetaKey], clientClaimedCLI)
		}
	})

	t.Run("garbage header collapses to unknown, not stored raw", func(t *testing.T) {
		meta := run(t, "ClientClaimGarbageTest"+uuid.NewString()[:8], "'; DROP TABLE audit_events; --")
		if meta[auditClientMetaKey] != clientClaimedUnknown {
			t.Errorf("client_claimed = %v, want %q", meta[auditClientMetaKey], clientClaimedUnknown)
		}
	})

	t.Run("no header defaults to ui", func(t *testing.T) {
		ginCtx, _ := ginTestContextWithHeader(t, "")
		action := "ClientClaimDefaultTest" + uuid.NewString()[:8]
		h.recordAudit(ginCtx.Request.Context(), actorID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        action,
			ResourceKind:  "Build",
			ResourceName:  "acme",
		})
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE action = $1`, action)
		})
		var metaJSON []byte
		if err := pool.QueryRow(context.Background(),
			`SELECT metadata FROM audit_events WHERE action = $1 AND actor_id = $2`,
			action, actorID,
		).Scan(&metaJSON); err != nil {
			t.Fatalf("read written audit row: %v", err)
		}
		var meta map[string]any
		if err := json.Unmarshal(metaJSON, &meta); err != nil {
			t.Fatalf("unmarshal stored metadata: %v", err)
		}
		if meta[auditClientMetaKey] != clientClaimedUI {
			t.Errorf("client_claimed = %v, want %q", meta[auditClientMetaKey], clientClaimedUI)
		}
		if _, present := meta[auditAgentSessionMetaKey]; present {
			t.Errorf("agent_session_claimed = %v, want absent when no header was sent", meta[auditAgentSessionMetaKey])
		}
	})
}

// TestUploadSourceArchive_CLIHeaderReachesAuditRow is the end-to-end version:
// it drives clientClaimMiddleware exactly as the router mounts it on the
// authenticated /api/v1 group, then calls UploadSourceArchive itself (which
// contains no client_claimed-specific code), and reads the audit row its
// success-path recordAudit call wrote.
func TestUploadSourceArchive_CLIHeaderReachesAuditRow(t *testing.T) {
	pool := testSourceArchivePool(t)
	projectID, envID, appName := seedSourceArchiveProject(t, pool, "acme")

	uploader := &fakeSourceUploader{bucket: "test-bucket"}
	h := &Handler{pool: pool, sourceUploader: uploader}
	claims := &auth.Claims{UserID: seedUser(t, pool), Groups: []string{"/platform-admins"}}
	c, rec := newUploadSourceArchiveCtx(t, projectID, envID, appName, claims, buildTestArchive(t))
	c.Request.Header.Set(headerClientClaimed, "cli/0.9.0")
	c.Request.Header.Set(headerAgentSessionClaimed, "claude-code-abc123")

	clientClaimMiddleware()(c)
	h.UploadSourceArchive(c)

	if rec.Code != 202 {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}

	var metaJSON []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata FROM audit_events
		  WHERE project_id = $1 AND environment_id = $2 AND action = 'UploadSourceArchive'
		  ORDER BY created_at DESC LIMIT 1`,
		projectID, envID,
	).Scan(&metaJSON); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		t.Fatalf("unmarshal stored metadata: %v", err)
	}
	if meta[auditClientMetaKey] != clientClaimedCLI {
		t.Errorf("client_claimed = %v, want %q", meta[auditClientMetaKey], clientClaimedCLI)
	}
	if meta[auditAgentSessionMetaKey] != "claude-code-abc123" {
		t.Errorf("agent_session_claimed = %v, want %q", meta[auditAgentSessionMetaKey], "claude-code-abc123")
	}
}
