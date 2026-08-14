package api

import (
	"context"
	"os"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestTierReconciler_NeverPutsPlatformDatabasesOnThePlanLadder runs a real tick
// over two seeded databases: one owned by the platform org, one owned by a
// customer on the same billing plan.
//
// The platform one must come out "internal" (no storage limit), the customer
// one on the tier its plan implies. Without the exemption both would land on
// "starter": every platform database - cloud-console, keycloak, nexus, powerdns
// - lives in a project of org "dada", which is itself on the startup plan, so a
// plan lookup would hand the control plane a 5 GB read-only ceiling.
func TestTierReconciler_NeverPutsPlatformDatabasesOnThePlanLadder(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping tier reconciler integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := uuid.NewString()[:8]
	platformOrg := "platform-org-" + suffix
	customerOrg := "customer-org-" + suffix

	for _, org := range []string{platformOrg, customerOrg} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO billing_accounts (org_id, plan) VALUES ($1, 'startup')
			 ON CONFLICT (org_id) DO UPDATE SET plan = 'startup'`, org); err != nil {
			t.Fatalf("seed billing account for %s: %v", org, err)
		}
		t.Cleanup(func() {
			pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, org)
		})
	}

	h := &Handler{
		pool:         pool,
		cfg:          &config.Config{DBQuotaExemptOrgs: []string{platformOrg}},
		billingPlans: testPlans(),
	}
	r := &dbTierReconciler{h: h}

	seed := func(org, dbName string) uuid.UUID {
		var projectID, envID uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
			"db-tier-test-"+dbName, org).Scan(&projectID); err != nil {
			t.Fatalf("seed project for %s: %v", dbName, err)
		}
		t.Cleanup(func() { dropSeededProject(pool, projectID) })
		if err := pool.QueryRow(ctx,
			`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
			projectID, "ns-"+dbName).Scan(&envID); err != nil {
			t.Fatalf("seed environment for %s: %v", dbName, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
			 VALUES ($1, $2, 'ServiceDatabaseV2', $3, 'Ready', $4::jsonb)`,
			projectID, envID, dbName, `{"tier":"unlimited","spec":{"appRef":"`+dbName+`"}}`); err != nil {
			t.Fatalf("seed snapshot for %s: %v", dbName, err)
		}
		return envID
	}

	platformEnv := seed(platformOrg, "platform-"+suffix)
	customerEnv := seed(customerOrg, "customer-"+suffix)

	r.tick(ctx)

	tierOf := func(envID uuid.UUID) string {
		var payload string
		if err := pool.QueryRow(ctx,
			`SELECT payload->>'tier' FROM operations
			  WHERE environment_id = $1 AND action = 'SetDatabaseTier'
			  ORDER BY created_at DESC LIMIT 1`, envID).Scan(&payload); err != nil {
			t.Fatalf("read queued tier for env %s: %v", envID, err)
		}
		return payload
	}

	if got := tierOf(platformEnv); got != dbTierInternal {
		t.Fatalf("platform database queued for tier %q, want %q: the storage quota can reach the control plane", got, dbTierInternal)
	}
	if got, want := tierOf(customerEnv), databaseTierByPlan["startup"]; got != want {
		t.Fatalf("customer database queued for tier %q, want %q", got, want)
	}
}
