package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/costengine"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// overagePlans gives the free plan a footprint so it has a real allowance.
// Priced against overageUnit below, free costs 100 RUB of cluster and lists at
// 100 * 1.5 = 150 RUB, which is the figure the gate measures against. The
// production plans.yaml works the same way; the numbers are round here so a
// failure names the branch rather than a rounding difference.
func overagePlans() []pricing.Plan {
	plans := testPlans()
	for i := range plans {
		if plans[i].Key == "free" {
			plans[i].InternalFootprint = costengine.Footprint{VCPU: 1}
		}
	}
	return plans
}

func overageUnit() costengine.UnitCost {
	return costengine.UnitCost{PerVCPU: 100}
}

const overageIncludedRub = 100 * pricing.MarkupDefault

// seedConsumingOrg creates an org holding one project with the given amount of
// list-price consumption booked into the current calendar month.
func seedConsumingOrg(t *testing.T, pool *pgxpool.Pool, plan string, spentRub float64) string {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	orgID := "org-overage-gate-" + suffix

	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"overage-gate-"+suffix, orgID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "overage-gate-ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, updated_at)
		 VALUES ($1, $2, now(), now())
		 ON CONFLICT (org_id) DO UPDATE SET plan = EXCLUDED.plan`, orgID, plan,
	); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	if spentRub > 0 {
		if _, err := pool.Exec(ctx,
			`INSERT INTO app_usage (environment_id, app_name, hour_start, kind, org_id, project_id, cost_rub)
			 VALUES ($1, 'web', date_trunc('hour', now()) - interval '1 hour', 'pod', $2, $3, $4)`,
			envID, orgID, projectID, spentRub,
		); err != nil {
			t.Fatalf("seed ledger row: %v", err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM app_usage WHERE org_id = $1`, orgID)
		dropSeededProject(pool, projectID)
		_, _ = pool.Exec(bg, `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		dropSeededAudit(pool, "BillingAccount", orgID)
	})
	return orgID
}

func overageGateHandler(pool *pgxpool.Pool, factor float64, exempt []string) *Handler {
	return &Handler{
		pool: pool,
		cfg: &config.Config{
			BillingEnabled:            true,
			BillingExemptOrgs:         exempt,
			BillingOverageBlockFactor: factor,
		},
		billingPlans: overagePlans(),
		billingUnit:  overageUnit(),
	}
}

// TestCheckConsumption_FreeOrgPastTheFactor_Blocks is the degradation itself.
// A free account can sit inside every counted quota -- one app, one database,
// one domain -- and still cost multiples of what the tier was budgeted for, by
// running that one app fat around the clock. Counting things cannot see that;
// only the ledger can.
func TestCheckConsumption_FreeOrgPastTheFactor_Blocks(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedConsumingOrg(t, pool, "free", overageIncludedRub*3+1)
	h := overageGateHandler(pool, 3, nil)

	err := h.checkConsumption(context.Background(), orgID)
	var ce *consumptionExceededError
	if !errors.As(err, &ce) {
		t.Fatalf("err=%v (%T) want *consumptionExceededError; a free account past 3x its included consumption is still allowed to grow", err, err)
	}
	if ce.IncludedRUB != overageIncludedRub {
		t.Errorf("included=%v want %v; the allowance must be the plan's own priced footprint, not an invented number", ce.IncludedRUB, overageIncludedRub)
	}
}

// TestCheckConsumption_UnderTheFactor_Allows keeps the gate off the ordinary
// free account. The free tier is a budgeted cost, not an accident: an account
// consuming what it was expected to consume is the product working.
func TestCheckConsumption_UnderTheFactor_Allows(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedConsumingOrg(t, pool, "free", overageIncludedRub*3-1)
	h := overageGateHandler(pool, 3, nil)

	if err := h.checkConsumption(context.Background(), orgID); err != nil {
		t.Fatalf("free org just under the factor was blocked: %v", err)
	}
}

// TestCheckConsumption_PaidPlan_NeverBlocks. Consumption past a plan someone
// actually pays for is an invoice to raise, not a wall to hit. Blocking there
// would aim the degradation at exactly the accounts worth keeping.
func TestCheckConsumption_PaidPlan_NeverBlocks(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedConsumingOrg(t, pool, "startup", 100000)
	h := overageGateHandler(pool, 3, nil)

	if err := h.checkConsumption(context.Background(), orgID); err != nil {
		t.Fatalf("paying org was blocked from growing: %v", err)
	}
}

// TestCheckConsumption_ExemptOrg_NeverBlocks. BILLING_EXEMPT_ORGS is the
// platform's own estate; it is the heaviest consumer on the cluster by design
// and gating it would take the platform's own demo and e2e apps down.
func TestCheckConsumption_ExemptOrg_NeverBlocks(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedConsumingOrg(t, pool, "free", 100000)
	h := overageGateHandler(pool, 3, []string{"someone-else", orgID})

	if err := h.checkConsumption(context.Background(), orgID); err != nil {
		t.Fatalf("exempt org was blocked: %v", err)
	}
}

// TestCheckConsumption_ZeroFactor_DisablesTheGate keeps a kill switch that does
// not need a redeploy of anything but one env var. A degradation aimed at
// customers has to be switchable off from outside the code.
func TestCheckConsumption_ZeroFactor_DisablesTheGate(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedConsumingOrg(t, pool, "free", 100000)
	h := overageGateHandler(pool, 0, nil)

	if err := h.checkConsumption(context.Background(), orgID); err != nil {
		t.Fatalf("gate refused with BILLING_OVERAGE_BLOCK_FACTOR=0: %v", err)
	}
}

// TestCheckConsumption_ActiveGrace_Allows. Grandfathered orgs were promised
// their existing footprint stays workable; a money gate that ignored the grace
// window would take that promise back through a different door.
func TestCheckConsumption_ActiveGrace_Allows(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedConsumingOrg(t, pool, "free", 100000)
	if _, err := pool.Exec(context.Background(),
		`UPDATE billing_accounts SET quota_grace_until = $2 WHERE org_id = $1`,
		orgID, time.Now().UTC().Add(30*24*time.Hour)); err != nil {
		t.Fatalf("set grace: %v", err)
	}
	h := overageGateHandler(pool, 3, nil)

	if err := h.checkConsumption(context.Background(), orgID); err != nil {
		t.Fatalf("org inside its grace window was blocked: %v", err)
	}
}

// TestCheckConsumption_NoAllowanceDerivable_Allows. billingUnit is loaded
// best-effort at startup and stays zero when cluster-cost.yaml cannot be read;
// with no unit cost the allowance is zero, and a zero allowance would put every
// free account instantly over any factor. The gate has to fail open there.
func TestCheckConsumption_NoAllowanceDerivable_Allows(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedConsumingOrg(t, pool, "free", 100000)
	h := overageGateHandler(pool, 3, nil)
	h.billingUnit = costengine.UnitCost{}

	if err := h.checkConsumption(context.Background(), orgID); err != nil {
		t.Fatalf("gate refused with no derivable allowance: %v", err)
	}
}

// TestCheckQuota_CarriesTheConsumptionBlock is why the gate lives inside
// checkQuota rather than beside it. Every growth path already calls checkQuota;
// a second gate that each path had to remember to call is a gate that a future
// growth path silently will not have.
func TestCheckQuota_CarriesTheConsumptionBlock(t *testing.T) {
	pool := quotaGatePool(t)
	orgID := seedConsumingOrg(t, pool, "free", overageIncludedRub*3+1)
	h := overageGateHandler(pool, 3, nil)

	err := h.checkQuota(context.Background(), orgID, "databases")
	var ce *consumptionExceededError
	if !errors.As(err, &ce) {
		t.Fatalf("checkQuota err=%v (%T) want *consumptionExceededError; the org is inside its database count but past its money", err, err)
	}
}

// TestBillingBlockAudit_NamesTheGate. The two refusals look identical to a
// customer -- a 403 on create -- and support has to be able to tell "you have
// too many apps" from "you are burning more than the free tier includes"
// without re-deriving the ledger by hand.
func TestBillingBlockAudit_NamesTheGate(t *testing.T) {
	quotaMeta, ok := billingBlockAudit(&quotaExceededError{Resource: "apps", Limit: 1})
	if !ok || quotaMeta["reason"] != "quota_exceeded" {
		t.Fatalf("quota audit meta = %v (ok=%v)", quotaMeta, ok)
	}
	consMeta, ok := billingBlockAudit(&consumptionExceededError{SpentRUB: 900, IncludedRUB: 270, Factor: 3})
	if !ok || consMeta["reason"] != "consumption_exceeded" {
		t.Fatalf("consumption audit meta = %v (ok=%v)", consMeta, ok)
	}
	if consMeta["spent_rub"] != float64(900) || consMeta["included_rub"] != float64(270) {
		t.Errorf("consumption audit meta lost its numbers: %v", consMeta)
	}
	if _, ok := billingBlockAudit(errors.New("database is on fire")); ok {
		t.Error("an unrelated error was reported as a billing block; a failing gate must not read as a refused customer")
	}
}
