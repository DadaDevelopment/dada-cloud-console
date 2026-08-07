package api

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestStorageUsedGBRoundsUp(t *testing.T) {
	const gib = int64(1) << 30
	for _, tc := range []struct {
		bytes int64
		want  int
	}{
		{0, 0},
		{-1, 0},
		{1, 1},
		{gib, 1},
		{gib + 1, 2},
		{15 * gib, 15},
	} {
		if got := storageUsedGB(tc.bytes); got != tc.want {
			t.Errorf("storageUsedGB(%d) = %d, want %d", tc.bytes, got, tc.want)
		}
	}
}

// A managed database is where a customer's gigabytes actually accumulate - the
// fonbet ticket was a 15 GB database nobody could see a number for - so the org
// total has to include db_quota_state, not just app volumes.
func TestBuildUsageCountsDatabaseGigabytes(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()
	h := &Handler{pool: pool, cfg: &config.Config{BillingEnabled: true}, billingPlans: testPlans()}
	orgID := "org-dbusage-" + uuid.NewString()[:8]
	seedFreePlanOrg(t, pool, orgID)
	projectID, envID := seedStorageCapFixture(t, pool, orgID)

	appName := "app-" + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		VALUES ($1, $2, 'App', $3, 'Ready', $4)
	`, projectID, envID, appName, `{"volume":{"path":"/data","size":"2Gi"}}`); err != nil {
		t.Fatalf("seed app volume: %v", err)
	}

	const dbBytes = int64(3) << 30
	if _, err := pool.Exec(ctx, `
		INSERT INTO db_quota_state (environment_id, name, project_id, size_bytes)
		VALUES ($1, $2, $3, $4)
	`, envID, "db-"+uuid.NewString()[:8], projectID, dbBytes); err != nil {
		t.Fatalf("seed db quota state: %v", err)
	}

	plan, err := h.planFor(ctx, orgID)
	if err != nil {
		t.Fatalf("planFor: %v", err)
	}
	usage, err := h.buildUsage(ctx, orgID, plan)
	if err != nil {
		t.Fatalf("buildUsage: %v", err)
	}
	row, ok := usage["storage_gb"]
	if !ok {
		t.Fatal("storage_gb missing from usage: the console renders this row and would silently drop it")
	}
	if row["used"] != 5 {
		t.Fatalf("storage_gb used = %v, want 5 (2Gi volume + 3Gi database)", row["used"])
	}
	if row["limit"] != 2 {
		t.Fatalf("storage_gb limit = %v, want the free plan quota 2", row["limit"])
	}
}

// Storage over the plan number must not appear in quota_over_limit: nothing
// enforces the org total, and a banner about a wall that does not exist reads
// as the console being broken.
func TestOverQuotaLinesIgnoresStorage(t *testing.T) {
	over := overQuotaLines(map[string]gin.H{
		"storage_gb": {"used": 40, "limit": 2},
	})
	if len(over) != 0 {
		t.Fatalf("overQuotaLines = %v, want empty", over)
	}
}
