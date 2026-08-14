package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

// TestResolveSoleAppRef_ExactlyOneApp proves the ground state this whole fix
// exists to reach: with app_ref left empty and exactly one App in the
// environment, resolveSoleAppRef returns that app's name instead of leaving
// the database permanently unbound.
func TestResolveSoleAppRef_ExactlyOneApp(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	appName := "app-" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, appName)

	got, err := h.resolveSoleAppRef(context.Background(), projectID, envID)
	if err != nil {
		t.Fatalf("resolveSoleAppRef: %v", err)
	}
	if got != appName {
		t.Fatalf("resolveSoleAppRef = %q, want %q", got, appName)
	}
}

// TestResolveSoleAppRef_NoApps proves an environment with zero apps is left
// unresolved rather than guessed.
func TestResolveSoleAppRef_NoApps(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)

	got, err := h.resolveSoleAppRef(context.Background(), projectID, envID)
	if err != nil {
		t.Fatalf("resolveSoleAppRef: %v", err)
	}
	if got != "" {
		t.Fatalf("resolveSoleAppRef = %q, want empty (no apps)", got)
	}
}

// TestResolveSoleAppRef_TwoApps proves an ambiguous environment (2+ apps) is
// also left unresolved: auto-binding to one of several apps would be a guess,
// not a resolution.
func TestResolveSoleAppRef_TwoApps(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	seedApp(t, pool, projectID, envID, "app-"+uuid.NewString()[:8])
	seedApp(t, pool, projectID, envID, "app-"+uuid.NewString()[:8])

	got, err := h.resolveSoleAppRef(context.Background(), projectID, envID)
	if err != nil {
		t.Fatalf("resolveSoleAppRef: %v", err)
	}
	if got != "" {
		t.Fatalf("resolveSoleAppRef = %q, want empty (ambiguous: 2 apps)", got)
	}
}

// TestCreateServiceDatabase_AutoBindsSoleApp is the end-to-end proof: a create
// request with app_ref left empty, in an environment holding exactly one app,
// must persist the resolved app_ref into the optimistic snapshot's app_ref AND
// spec.appRef -- not just a local variable inside createManagedDatabase --
// since spec.appRef is what the payload the renderer reads is built from. The
// dbcreds fake reports the secret as not-ready so the async delivery goroutine
// createManagedDatabase starts (proof it started at all: engine=="" and the
// resolved app_ref is non-empty, the exact gate that never let a
// console-issued request through before this fix) loops harmlessly instead of
// panicking on a nil resolver. CreateServiceDatabase's own audit row still
// reads "pending", not "success": auditInsertSQL (audit.go) deliberately
// downgrades a claimed success while the linked operation is still "Created"
// and not yet Committed/Ready/Failed, for every create endpoint, not just this
// one.
func TestCreateServiceDatabase_AutoBindsSoleApp(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}, dbcreds: fakeDBCredsResolver{notReady: true}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	appName := "app-" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, appName)
	dbName := "db-" + uuid.NewString()[:8]

	c, rec := newCreateCtx(t, `{"name":"`+dbName+`","database":"appdb"}`, params(projectID, envID), claims)
	h.CreateServiceDatabase(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "ServiceDatabaseV2", dbName)

	var rawSummary []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, dbName,
	).Scan(&rawSummary); err != nil {
		t.Fatalf("read back snapshot: %v", err)
	}
	var summary map[string]any
	if err := json.Unmarshal(rawSummary, &summary); err != nil {
		t.Fatalf("summary json: %v", err)
	}
	if got := summary["app_ref"]; got != appName {
		t.Fatalf("snapshot app_ref = %v, want %q -- resolution never reached persist", got, appName)
	}
	spec, _ := summary["spec"].(map[string]any)
	if spec == nil || spec["appRef"] != appName {
		t.Fatalf("snapshot spec.appRef = %v, want %q -- resolution never reached the payload the renderer reads", spec, appName)
	}

	outcome, _, _ := lastAuditRow(t, pool, projectID, "CreateServiceDatabase")
	if outcome != auditOutcomePending {
		t.Fatalf("CreateServiceDatabase outcome = %q, want pending (operation still Created)", outcome)
	}
}

// TestCreateServiceDatabase_LeavesAmbiguousEnvUnbound is the RED case this fix
// must not break: with two apps in the environment and app_ref left empty,
// the database must stay unbound (app_ref empty in the persisted snapshot) --
// auto-binding to either app would silently hand a database's credentials to
// the wrong app.
func TestCreateServiceDatabase_LeavesAmbiguousEnvUnbound(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	seedApp(t, pool, projectID, envID, "app-"+uuid.NewString()[:8])
	seedApp(t, pool, projectID, envID, "app-"+uuid.NewString()[:8])
	dbName := "db-" + uuid.NewString()[:8]

	c, rec := newCreateCtx(t, `{"name":"`+dbName+`","database":"appdb"}`, params(projectID, envID), claims)
	h.CreateServiceDatabase(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "ServiceDatabaseV2", dbName)

	var rawSummary []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, dbName,
	).Scan(&rawSummary); err != nil {
		t.Fatalf("read back snapshot: %v", err)
	}
	var summary map[string]any
	if err := json.Unmarshal(rawSummary, &summary); err != nil {
		t.Fatalf("summary json: %v", err)
	}
	if got := summary["app_ref"]; got != "" {
		t.Fatalf("snapshot app_ref = %v, want empty -- ambiguous environment must not be guessed", got)
	}
}

// TestCreateServiceDatabase_AutoBoundAppRefReachesSeedAudit is the full chain
// this fix exists to close: an empty app_ref on the create request, resolved
// to the sole app in the environment, must reach the exact place that writes
// the SeedDatabaseDSN audit row -- not just a local variable inside
// createManagedDatabase. It calls seedDatabaseDSNIfAbsent directly with the
// same resolved value CreateServiceDatabase would have queued
// deliverDatabaseDSNAsync with, so the assertion does not depend on winning a
// race against the background goroutine.
func TestCreateServiceDatabase_AutoBoundAppRefReachesSeedAudit(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	projectID, envID := seedOptimisticFixture(t, pool)
	userID := seedUser(t, pool)
	appName := "app-" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, appName)

	resolved, err := h.resolveSoleAppRef(context.Background(), projectID, envID)
	if err != nil {
		t.Fatalf("resolveSoleAppRef: %v", err)
	}
	if resolved != appName {
		t.Fatalf("resolveSoleAppRef = %q, want %q", resolved, appName)
	}

	dbName := "db-" + uuid.NewString()[:8]
	seeded, err := h.seedDatabaseDSNIfAbsent(context.Background(), projectID, envID, dbName, resolved,
		"postgresql://app:x@pg-router.databases.svc.cluster.local:5432/appdb?sslmode=disable", userID, "auto")
	if err != nil {
		t.Fatalf("seedDatabaseDSNIfAbsent: %v", err)
	}
	if !seeded {
		t.Fatalf("seeded = false, want true (env var was not set yet)")
	}

	outcome, _, gotEnv := lastAuditRow(t, pool, projectID, auditActionSeedDatabaseDSN)
	if outcome != auditOutcomeSuccess {
		t.Fatalf("SeedDatabaseDSN outcome = %q, want success", outcome)
	}
	if gotEnv == nil || *gotEnv != envID {
		t.Fatalf("SeedDatabaseDSN environment_id = %v, want %v", gotEnv, envID)
	}
}
