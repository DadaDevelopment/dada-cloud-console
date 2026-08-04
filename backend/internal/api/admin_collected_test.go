package api

import (
	"context"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDedupeSortedCollapsesRuns pins the plan label of a client whose projects
// all sit in one org: "free", not "free+free".
func TestDedupeSortedCollapsesRuns(t *testing.T) {
	got := dedupeSorted([]string{"free", "free", "free", "startup"})
	if len(got) != 2 || got[0] != "free" || got[1] != "startup" {
		t.Fatalf("dedupeSorted: want [free startup], got %v", got)
	}
}

// setProjectOwner gives a seeded project an owner and returns that owner id in
// the form the cost tree uses as ClientID, so a test client can be matched to
// the org money by the same rule production uses.
func setProjectOwner(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID) string {
	t.Helper()
	owner := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`UPDATE projects SET owner_id = $1 WHERE id = $2`, owner, projectID,
	); err != nil {
		t.Fatalf("set project owner: %v", err)
	}
	return owner.String()
}

// TestAttachClientMoneyCreditsOneClientPerOrg guards the number that made the
// column worth shipping. An org is not a client: the only real payment the
// platform ever settled belongs to an org with nine projects and two owners, so
// crediting every owner turned 990 RUB into 1980 RUB of "collected". The org's
// money must land on exactly one client, and the totals must equal the money
// that actually settled.
func TestAttachClientMoneyCreditsOneClientPerOrg(t *testing.T) {
	pool := appUsagePool(t)
	founderProject, _, orgID, _, _ := seedAppUsageEnv(t, pool)
	ctx := context.Background()

	var joinerProject uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id, owner_id)
		 VALUES ($1, $1, $2, $3) RETURNING id`,
		"appusage-joiner-"+uuid.NewString()[:8], orgID, uuid.New(),
	).Scan(&joinerProject); err != nil {
		t.Fatalf("seed second project in the same org: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, joinerProject) })

	h := &Handler{pool: pool, billingPlans: []pricing.Plan{{Key: "startup", PriceRUB: 990}}}
	if _, err := pool.Exec(ctx,
		`INSERT INTO billing_accounts (org_id, plan) VALUES ($1, 'startup')
		 ON CONFLICT (org_id) DO UPDATE SET plan = EXCLUDED.plan`, orgID,
	); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO payments (id, org_id, plan, amount_value, status, created_by_sub, paid_at)
		 VALUES ($1, $2, 'startup', 990, 'succeeded', 'test', now())`,
		uuid.New(), orgID,
	); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
	})

	founder := setProjectOwner(t, pool, founderProject)
	var joiner string
	if err := pool.QueryRow(ctx,
		`SELECT owner_id::text FROM projects WHERE id = $1`, joinerProject,
	).Scan(&joiner); err != nil {
		t.Fatalf("read joiner owner: %v", err)
	}

	clients := []*adminCostClient{
		{ClientID: founder, Projects: []adminCostProject{{ProjectID: founderProject.String()}}},
		{ClientID: joiner, Projects: []adminCostProject{{ProjectID: joinerProject.String()}}},
	}
	totals := h.attachClientMoney(ctx, clients, 30)

	if clients[0].PaidRUB != 990 {
		t.Fatalf("org founder must carry the org payment: want 990, got %v", clients[0].PaidRUB)
	}
	if clients[1].PaidRUB != 0 {
		t.Fatalf("co-owner must not be credited the same payment: want 0, got %v", clients[1].PaidRUB)
	}
	if clients[1].PlanPriceRUB != 0 {
		t.Fatalf("co-owner must not be charged the same subscription: want 0, got %v", clients[1].PlanPriceRUB)
	}
	if totals.PaidRUB != 990 {
		t.Fatalf("totals must count the org payment once: want 990, got %v", totals.PaidRUB)
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
		appUsageKindPod, 1, 2, 0, 1, nil, nil, 8, appUsageSourceMeter) {
		t.Fatal("seed pod ledger row failed")
	}
	if !h.upsertAppUsage(ctx, appUsageKey{namespace: k8sNS, app: "web"}, target, hour,
		appUsageKindVolume, 0, 0, 24, 0, nil, nil, 2, appUsageSourceMeter) {
		t.Fatal("seed volume ledger row failed")
	}

	owner := setProjectOwner(t, pool, projectID)
	client := &adminCostClient{
		ClientID:   owner,
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
		ClientID: setProjectOwner(t, pool, projectID),
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
