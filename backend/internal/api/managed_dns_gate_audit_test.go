package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/pdns"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Refusals raised by the shared managed-DNS gate, against a real database.
//
// The gate answers before any handler body runs, so a rejected zone edit used to
// leave no trace at all: a read-only member trying to repoint a domain and a
// member who never tried looked identical in the audit path. Reads stay silent on
// purpose -- a refused GET says nothing about intent and would bury the writes.

func seedGateProject(t *testing.T, pool *pgxpool.Pool, org string) uuid.UUID {
	t.Helper()
	suffix := uuid.NewString()[:8]
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"dns-gate-"+suffix, org,
	).Scan(&id); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		dropSeededProject(pool, id)
	})
	return id
}

func countGateAuditRows(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE project_id = $1`, projectID,
	).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func gateHandler(pool *pgxpool.Pool, configured bool) *Handler {
	cfg := &config.Config{}
	h := &Handler{pool: pool, cfg: cfg}
	if configured {
		cfg.PowerDNSAPIKey = "test-key"
		h.pdns = pdns.NewClient("http://127.0.0.1:1", "test-key", time.Second)
	}
	return h
}

func TestManagedDNSGate_RefusalIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	userID := seedUser(t, pool)

	t.Run("read-only member editing a zone leaves a refusal row", func(t *testing.T) {
		org := "dns-gate-org-" + uuid.NewString()[:8]
		projectID := seedGateProject(t, pool, org)
		h := gateHandler(pool, true)
		claims := &auth.Claims{UserID: userID, Groups: []string{"/orgs/" + org + "/ReadOnly"}}

		path := "/projects/" + projectID.String() + "/domains/authorizations/" + uuid.NewString() + "/zone/records"
		rec := routeDatabaseCall(t, http.MethodPost,
			"/projects/:projectId/domains/authorizations/:authId/zone/records", path,
			`{"name":"@","type":"A","ttl":300,"records":["1.2.3.4"]}`, claims, h.UpsertManagedRecord)

		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 403/404; body=%s", rec.Code, rec.Body.String())
		}
		outcome, reason, _ := lastAuditRow(t, pool, projectID, "UpsertManagedRecord")
		if outcome != auditOutcomeFailure {
			t.Errorf("outcome = %q, want %q", outcome, auditOutcomeFailure)
		}
		if reason != "read_only_role" && reason != "not_a_member" {
			t.Errorf("reason = %q, want read_only_role or not_a_member", reason)
		}
	})

	t.Run("the same refusal on a read handler stays silent", func(t *testing.T) {
		org := "dns-gate-org-" + uuid.NewString()[:8]
		projectID := seedGateProject(t, pool, org)
		h := gateHandler(pool, true)
		claims := &auth.Claims{UserID: userID, Groups: []string{"/orgs/" + org + "/ReadOnly"}}

		path := "/projects/" + projectID.String() + "/domains/authorizations/" + uuid.NewString() + "/zone/records"
		rec := routeDatabaseCall(t, http.MethodGet,
			"/projects/:projectId/domains/authorizations/:authId/zone/records", path,
			"", claims, h.ListManagedRecords)

		if rec.Code == http.StatusOK {
			t.Fatalf("expected the gate to refuse the read, got 200: %s", rec.Body.String())
		}
		if n := countGateAuditRows(t, pool, projectID); n != 0 {
			t.Errorf("audit rows = %d, want 0 for a refused read", n)
		}
	})

	t.Run("managed DNS switched off is recorded as a refusal, not silence", func(t *testing.T) {
		org := "dns-gate-org-" + uuid.NewString()[:8]
		projectID := seedGateProject(t, pool, org)
		h := gateHandler(pool, false)

		path := "/projects/" + projectID.String() + "/domains/authorizations/" + uuid.NewString() + "/zone/records"
		rec := routeDatabaseCall(t, http.MethodPost,
			"/projects/:projectId/domains/authorizations/:authId/zone/records", path,
			`{"name":"@","type":"A","ttl":300,"records":["1.2.3.4"]}`, godClaims(userID), h.UpsertManagedRecord)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
		}
		outcome, reason, _ := lastAuditRow(t, pool, projectID, "UpsertManagedRecord")
		if outcome != auditOutcomeFailure {
			t.Errorf("outcome = %q, want %q", outcome, auditOutcomeFailure)
		}
		if reason != "managed_dns_not_configured" {
			t.Errorf("reason = %q, want managed_dns_not_configured", reason)
		}
	})

	t.Run("an unknown authorization id is recorded against the project", func(t *testing.T) {
		org := "dns-gate-org-" + uuid.NewString()[:8]
		projectID := seedGateProject(t, pool, org)
		h := gateHandler(pool, true)

		path := "/projects/" + projectID.String() + "/domains/authorizations/" + uuid.NewString() + "/zone/records"
		rec := routeDatabaseCall(t, http.MethodDelete,
			"/projects/:projectId/domains/authorizations/:authId/zone/records", path,
			"", godClaims(userID), h.DeleteManagedRecord)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		outcome, reason, _ := lastAuditRow(t, pool, projectID, "DeleteManagedRecord")
		if outcome != auditOutcomeFailure {
			t.Errorf("outcome = %q, want %q", outcome, auditOutcomeFailure)
		}
		if reason != "authorization_not_found" {
			t.Errorf("reason = %q, want authorization_not_found", reason)
		}
	})

	t.Run("an unauthenticated call writes nothing", func(t *testing.T) {
		org := "dns-gate-org-" + uuid.NewString()[:8]
		projectID := seedGateProject(t, pool, org)
		h := gateHandler(pool, true)

		path := "/projects/" + projectID.String() + "/domains/authorizations/" + uuid.NewString() + "/zone/records"
		rec := routeDatabaseCall(t, http.MethodPost,
			"/projects/:projectId/domains/authorizations/:authId/zone/records", path,
			`{"name":"@","type":"A","ttl":300,"records":["1.2.3.4"]}`, nil, h.UpsertManagedRecord)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
		if n := countGateAuditRows(t, pool, projectID); n != 0 {
			t.Errorf("audit rows = %d, want 0 without an actor", n)
		}
	})
}
