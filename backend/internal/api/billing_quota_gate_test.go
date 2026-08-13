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
		dropSeededProject(pool, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		dropSeededAudit(pool, "BillingAccount", orgID)
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

// TestCheckQuota_ActiveGrace_RecordsTheBreach pins the loud half of the grace
// window. Allowing the create is the product decision; doing it silently is
// how a grandfathered org sits three apps over its plan for months with nobody
// on either side aware, and then hits a wall the day the window closes.
func TestCheckQuota_ActiveGrace_RecordsTheBreach(t *testing.T) {
	pool := quotaGatePool(t)
	future := time.Now().UTC().Add(30 * 24 * time.Hour)
	orgID := seedOverQuotaOrg(t, pool, &future)
	h := quotaGateHandler(pool, nil)

	if err := h.checkQuota(context.Background(), orgID, "apps"); err != nil {
		t.Fatalf("grandfathered org was blocked during its grace window: %v", err)
	}

	var count int
	var lastAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT quota_breach_count, quota_breach_last_at FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&count, &lastAt); err != nil {
		t.Fatalf("read breach counters: %v", err)
	}
	if count != 1 || lastAt == nil {
		t.Fatalf("quota_breach_count=%d last_at=%v want 1 and a timestamp; an over-limit create during grace must leave a trace", count, lastAt)
	}

	var audits int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE action = 'QuotaBreachAllowed' AND resource_name = $1`, orgID,
	).Scan(&audits); err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	if audits != 1 {
		t.Fatalf("QuotaBreachAllowed audit events=%d want 1", audits)
	}

	if err := h.checkQuota(context.Background(), orgID, "apps"); err != nil {
		t.Fatalf("second over-limit create was blocked during grace: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT quota_breach_count FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&count); err != nil {
		t.Fatalf("re-read breach count: %v", err)
	}
	if count != 2 {
		t.Fatalf("quota_breach_count=%d want 2; the counter is how loud the banner gets", count)
	}
}

// TestCheckQuota_UnderLimit_RecordsNothing keeps the noise honest: a compliant
// org inside a grace window must not accumulate breaches.
func TestCheckQuota_UnderLimit_RecordsNothing(t *testing.T) {
	pool := quotaGatePool(t)
	future := time.Now().UTC().Add(30 * 24 * time.Hour)
	orgID := seedOverQuotaOrg(t, pool, &future)
	h := quotaGateHandler(pool, nil)

	if err := h.checkQuota(context.Background(), orgID, "databases"); err != nil {
		t.Fatalf("under-limit resource was blocked: %v", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT quota_breach_count FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&count); err != nil {
		t.Fatalf("read breach count: %v", err)
	}
	if count != 0 {
		t.Fatalf("quota_breach_count=%d want 0 for a resource that is within its limit", count)
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

// TestCheckQuota_ActiveGrace_StillBlocksHardZero pins the one thing grace must
// not do: hand out a resource the plan includes none of. Every free org on
// prod carries grace to 2026-09-25 and none of them owns a VM, so a grace that
// covered app_servers would open the public-IP farming gate it was built to
// close, for six weeks, to exactly the accounts a fresh signup lands in.
func TestCheckQuota_ActiveGrace_StillBlocksHardZero(t *testing.T) {
	pool := quotaGatePool(t)
	future := time.Now().UTC().Add(30 * 24 * time.Hour)
	orgID := seedOverQuotaOrg(t, pool, &future)
	h := quotaGateHandler(pool, nil)

	err := h.checkQuota(context.Background(), orgID, "app_servers")
	if err == nil {
		t.Fatal("grace let a free org create a VM; app_servers is a hard zero, not a grandfathered overage")
	}
	qe, ok := err.(*quotaExceededError)
	if !ok {
		t.Fatalf("err=%T (%v) want *quotaExceededError", err, err)
	}
	if qe.Resource != "app_servers" || qe.Limit != 0 {
		t.Fatalf("resource=%q limit=%d want app_servers/0", qe.Resource, qe.Limit)
	}

	var breaches int
	if err := pool.QueryRow(context.Background(),
		`SELECT quota_breach_count FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&breaches); err != nil {
		t.Fatalf("read breach count: %v", err)
	}
	if breaches != 0 {
		t.Fatalf("quota_breach_count=%d want 0; a refused create is not a breach allowed by grace", breaches)
	}
}
