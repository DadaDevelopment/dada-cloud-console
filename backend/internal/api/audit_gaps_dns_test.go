package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/pdns"
)

// seedDomainAuthorization inserts a verified apex authorization and returns its id.
func seedDomainAuthorization(t *testing.T, pool *pgxpool.Pool, projectID, userID uuid.UUID, apex string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO domain_authorizations (project_id, apex_domain, verification_token, status, created_by)
		 VALUES ($1, $2, 'seed-token', 'verified', $3) RETURNING id`,
		projectID, apex, userID,
	).Scan(&id); err != nil {
		t.Fatalf("seed domain authorization: %v", err)
	}
	return id
}

// dnsTestHandler builds a handler whose PowerDNS client points nowhere: every
// branch under test refuses before any DNS call, and a live PowerDNS would only
// make the test depend on a service it does not need.
func dnsTestHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{
		pool: pool,
		cfg:  &config.Config{PowerDNSAPIKey: "test-key"},
		pdns: pdns.NewClient("http://127.0.0.1:1", "test-key", time.Second),
	}
}

func TestUpsertManagedRecord_UnsupportedTypeIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := dnsTestHandler(pool)
	userID := seedUser(t, pool)
	projectID, _ := seedOptimisticFixture(t, pool)
	authID := seedDomainAuthorization(t, pool, projectID, userID, "upsert-audit-"+uuid.NewString()[:8]+".example")

	path := "/projects/" + projectID.String() + "/domains/authorizations/" + authID.String() + "/zone/records"
	rec := routeDatabaseCall(t, http.MethodPost,
		"/projects/:projectId/domains/authorizations/:authId/zone/records", path,
		`{"name":"www","type":"SPF","contents":["v=spf1"]}`, godClaims(userID), h.UpsertManagedRecord)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, _ := lastAuditRow(t, pool, projectID, "UpsertManagedRecord")
	if outcome != auditOutcomeFailure || reason != "unsupported_record_type" {
		t.Errorf("audit row = (%q, %q), want (failure, unsupported_record_type)", outcome, reason)
	}
}

func TestDeleteManagedRecord_ProtectedApexNSIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := dnsTestHandler(pool)
	userID := seedUser(t, pool)
	projectID, _ := seedOptimisticFixture(t, pool)
	apex := "delete-audit-" + uuid.NewString()[:8] + ".example"
	authID := seedDomainAuthorization(t, pool, projectID, userID, apex)

	path := "/projects/" + projectID.String() + "/domains/authorizations/" + authID.String() + "/zone/records"
	rec := routeDatabaseCall(t, http.MethodDelete,
		"/projects/:projectId/domains/authorizations/:authId/zone/records", path,
		`{"name":"`+apex+`","type":"NS"}`, godClaims(userID), h.DeleteManagedRecord)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, _ := lastAuditRow(t, pool, projectID, "DeleteManagedRecord")
	if outcome != auditOutcomeFailure || reason != "protected_record" {
		t.Errorf("audit row = (%q, %q), want (failure, protected_record)", outcome, reason)
	}
}

func TestImportZone_NotDelegatedIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := dnsTestHandler(pool)
	userID := seedUser(t, pool)
	projectID, _ := seedOptimisticFixture(t, pool)
	authID := seedDomainAuthorization(t, pool, projectID, userID, "import-audit-"+uuid.NewString()[:8]+".example")

	path := "/projects/" + projectID.String() + "/domains/authorizations/" + authID.String() + "/zone/import"
	rec := routeDatabaseCall(t, http.MethodPost,
		"/projects/:projectId/domains/authorizations/:authId/zone/import", path,
		`{"records":[{"name":"www","type":"A","ttl":300,"contents":["1.2.3.4"]}]}`,
		godClaims(userID), h.ImportZone)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, _ := lastAuditRow(t, pool, projectID, "ImportZone")
	if outcome != auditOutcomeFailure || reason != "zone_not_delegated" {
		t.Errorf("audit row = (%q, %q), want (failure, zone_not_delegated)", outcome, reason)
	}
}

func TestCreateCloudTask_NoAgentIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)

	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/apps/demo/cloud-tasks"
	rec := routeDatabaseCall(t, http.MethodPost,
		"/projects/:projectId/environments/:envId/apps/:appName/cloud-tasks", path,
		`{"task_type":"metrika-goals"}`, godClaims(userID), h.CreateCloudTask)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}

	outcome, reason, gotEnv := lastAuditRow(t, pool, projectID, "CreateCloudTask")
	if outcome != auditOutcomeFailure || reason != "dadagent_unavailable" {
		t.Errorf("audit row = (%q, %q), want (failure, dadagent_unavailable)", outcome, reason)
	}
	if gotEnv == nil || *gotEnv != envID {
		t.Errorf("environment_id = %v, want %v", gotEnv, envID)
	}
}
