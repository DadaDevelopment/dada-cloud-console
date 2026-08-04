package api

import (
	"context"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/google/uuid"
)

// TestDedupeSortedCollapsesRuns pins the plan label of a client whose projects
// all sit in one org: "free", not "free+free".
func TestDedupeSortedCollapsesRuns(t *testing.T) {
	got := dedupeSorted([]string{"free", "free", "free", "startup"})
	if len(got) != 2 || got[0] != "free" || got[1] != "startup" {
		t.Fatalf("dedupeSorted: want [free startup], got %v", got)
	}
}

// TestAttachClientMoneyCountsOnlySettledPayments is the whole point of the
// column: a client on a free plan who runs a fat app must show consumption with
// zero collected, and a pending payment must not count as money. The modelled
// `revenue` number next to it says the opposite, which is why the settled one
// had to exist.
func TestAttachClientMoneyCountsOnlySettledPayments(t *testing.T) {
	pool := appUsagePool(t)
	projectID, envID, orgID, k8sNS, _ := seedAppUsageEnv(t, pool)
	ctx := context.Background()

	h := &Handler{pool: pool, billingPlans: []pricing.Plan{
		{Key: "free", PriceRUB: 0},
		{Key: "startup", PriceRUB: 990},
	}}

	if _, err := pool.Exec(ctx,
		`INSERT INTO billing_accounts (org_id, plan) VALUES ($1, 'free')
		 ON CONFLICT (org_id) DO UPDATE SET plan = EXCLUDED.plan`, orgID,
	); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
	})

	for _, p := range []struct {
		status string
		amount float64
	}{{"succeeded", 500}, {"pending", 100000}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO payments (id, org_id, plan, amount_value, status, created_by_sub, paid_at)
			 VALUES ($1, $2, 'startup', $3, $4, 'test', now())`,
			uuid.New(), orgID, p.amount, p.status,
		); err != nil {
			t.Fatalf("seed %s payment: %v", p.status, err)
		}
	}

	hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	target := appMeterTarget{envID: envID, projectID: projectID, orgID: orgID}
	if !h.upsertAppUsage(ctx, appUsageKey{namespace: k8sNS, app: "web"}, target, hour,
		appUsageKindPod, 1, 2, 0, 1, nil, nil, 8) {
		t.Fatal("seed pod ledger row failed")
	}
	if !h.upsertAppUsage(ctx, appUsageKey{namespace: k8sNS, app: "web"}, target, hour,
		appUsageKindVolume, 0, 0, 24, 0, nil, nil, 2) {
		t.Fatal("seed volume ledger row failed")
	}

	client := &adminCostClient{
		ClientID:   "owner-" + uuid.NewString()[:8],
		ClientName: "owner",
		Projects:   []adminCostProject{{ProjectID: projectID.String(), ProjectName: "p"}},
	}
	totals := h.attachClientMoney(ctx, []*adminCostClient{client}, 30)

	if client.Plan != "free" {
		t.Fatalf("plan: want free, got %q", client.Plan)
	}
	if client.PlanPriceRUB != 0 {
		t.Fatalf("free plan price: want 0, got %v", client.PlanPriceRUB)
	}
	if client.PaidRUB != 500 {
		t.Fatalf("paid: pending payment must not count, want 500, got %v", client.PaidRUB)
	}
	if client.MeteredRUB != 10 {
		t.Fatalf("metered: want 10 (pod 8 + volume 2), got %v", client.MeteredRUB)
	}
	if client.Projects[0].MeteredRUB != 10 {
		t.Fatalf("project metered: want 10, got %v", client.Projects[0].MeteredRUB)
	}
	if client.UncollectedRUB != -490 {
		t.Fatalf("uncollected must stay signed: want -490, got %v", client.UncollectedRUB)
	}
	if totals.MeteredRUB < 10 {
		t.Fatalf("totals must include this client's ledger: got %v", totals.MeteredRUB)
	}
	if totals.LedgerHours < 1 || totals.MeteredSince == "" {
		t.Fatalf("ledger coverage must be reported: hours=%d since=%q", totals.LedgerHours, totals.MeteredSince)
	}
}

// TestAttachClientMoneyScalesPlanPriceToWindow proves the subscription column
// is comparable to the metered one: both must describe the SAME window, or a
// 7-day view would show a month of subscription against a week of consumption
// and make every paying client look profitable.
func TestAttachClientMoneyScalesPlanPriceToWindow(t *testing.T) {
	pool := appUsagePool(t)
	projectID, _, orgID, _, _ := seedAppUsageEnv(t, pool)
	ctx := context.Background()

	h := &Handler{pool: pool, billingPlans: []pricing.Plan{{Key: "startup", PriceRUB: 990}}}
	if _, err := pool.Exec(ctx,
		`INSERT INTO billing_accounts (org_id, plan) VALUES ($1, 'startup')
		 ON CONFLICT (org_id) DO UPDATE SET plan = EXCLUDED.plan`, orgID,
	); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})

	client := &adminCostClient{
		ClientID: "owner-" + uuid.NewString()[:8],
		Projects: []adminCostProject{{ProjectID: projectID.String()}},
	}
	h.attachClientMoney(ctx, []*adminCostClient{client}, 7)

	want := round2(990 * 7 / billingMonthDays)
	if client.PlanPriceRUB != want {
		t.Fatalf("plan price over 7d: want %v, got %v", want, client.PlanPriceRUB)
	}
	if client.Plan != "startup" {
		t.Fatalf("plan: want startup, got %q", client.Plan)
	}
}
