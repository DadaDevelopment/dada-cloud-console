package api

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func quotaGatePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping quota-gate DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedOverQuotaOrg creates an org already holding more apps than the test plan
// set's free quota allows, so checkQuota("apps") must refuse the next one
// unless a gate bypass (exempt org or active grace) applies.
func seedOverQuotaOrg(t *testing.T, pool *pgxpool.Pool, graceUntil *time.Time) string {
	t.Helper()
	orgID := "org-quota-" + uuid.NewString()[:8]
	ctx := context.Background()

	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"quota-gate-"+uuid.NewString()[:8], orgID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "quota-gate-ns-"+uuid.NewString()[:8],
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase)
			 VALUES ($1, $2, 'App', $3, 'Ready')`,
			projectID, envID, "app-"+uuid.NewString()[:6],
		); err != nil {
			t.Fatalf("seed app snapshot: %v", err)
		}
	}
	if graceUntil != nil {
		if _, err := pool.Exec(ctx, `
			INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, quota_grace_until, updated_at)
			VALUES ($1, 'free', now(), $2, now())
		`, orgID, *graceUntil); err != nil {
			t.Fatalf("seed billing account: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM resource_snapshots WHERE project_id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM environments WHERE project_id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})
	return orgID
}

func quotaGateHandler(pool *pgxpool.Pool, exempt []string) *Handler {
	return &Handler{
		pool:         pool,
		cfg:          &config.Config{BillingEnabled: true, BillingExemptOrgs: exempt},
		billingPlans: testPlans(),
	}
}

func TestCheckQuota_AtFreeLimit_Blocks(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedOverQuotaOrg(t, pool, nil)
	h := quotaGateHandler(pool, nil)

	err := h.checkQuota(context.Background(), orgID, "apps")
	if err == nil {
		t.Fatal("checkQuota allowed a third app at the free limit; enforcement is off")
	}
	if _, ok := err.(*quotaExceededError); !ok {
		t.Fatalf("err=%T (%v) want *quotaExceededError", err, err)
	}
}

func TestCheckQuota_ActiveGrace_Allows(t *testing.T) {
	pool := quotaGatePool(t)
	future := time.Now().UTC().Add(30 * 24 * time.Hour)
	orgID := seedOverQuotaOrg(t, pool, &future)
	h := quotaGateHandler(pool, nil)

	if err := h.checkQuota(context.Background(), orgID, "apps"); err != nil {
		t.Fatalf("grandfathered org was blocked during its grace window: %v", err)
	}
}

func TestCheckQuota_ExpiredGrace_Blocks(t *testing.T) {
	pool := quotaGatePool(t)
	past := time.Now().UTC().Add(-24 * time.Hour)
	orgID := seedOverQuotaOrg(t, pool, &past)
	h := quotaGateHandler(pool, nil)

	if err := h.checkQuota(context.Background(), orgID, "apps"); err == nil {
		t.Fatal("expired grace still allowed the create; the window must close")
	}
}

func TestCheckQuota_ExemptOrg_Allows(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedOverQuotaOrg(t, pool, nil)
	h := quotaGateHandler(pool, []string{"someone-else", orgID})

	if err := h.checkQuota(context.Background(), orgID, "apps"); err != nil {
		t.Fatalf("BILLING_EXEMPT_ORGS member was gated: %v", err)
	}
}

func TestCheckQuota_BillingDisabled_AllowsEveryone(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedOverQuotaOrg(t, pool, nil)
	h := &Handler{pool: pool, cfg: &config.Config{BillingEnabled: false}, billingPlans: testPlans()}

	if err := h.checkQuota(context.Background(), orgID, "apps"); err != nil {
		t.Fatalf("quota enforced while BILLING_ENABLED=false: %v", err)
	}
}
