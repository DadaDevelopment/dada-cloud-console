package metrics

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/backend/internal/dbtest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// seedOrgLedger writes one ledger row for a throwaway org on the given plan and
// returns the org id. The row is dated inside the current calendar month, which
// is the window the collector reports on.
func seedOrgLedger(t *testing.T, pool *pgxpool.Pool, plan string, costRub float64) string {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	org := "org-overage-" + suffix

	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"overage-"+suffix, org,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dbtest.DropProject(pool, projectID) })

	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime)
		 VALUES ($1, 'prod', $2, 'prod', 'k8s') RETURNING id`,
		projectID, "overage-ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO billing_accounts (org_id, plan) VALUES ($1, $2)
		 ON CONFLICT (org_id) DO UPDATE SET plan = EXCLUDED.plan`, org, plan,
	); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_usage (environment_id, app_name, hour_start, kind, org_id, project_id, cost_rub)
		 VALUES ($1, 'web', date_trunc('hour', now()) - interval '1 hour', 'pod', $2, $3, $4)`,
		envID, org, projectID, costRub,
	); err != nil {
		t.Fatalf("seed ledger row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_usage WHERE org_id = $1`, org)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, org)
	})
	return org
}

// gaugeFor reads one org's value out of a labelled gauge vector, reporting
// whether the series exists at all. Absence is a real answer here: an org the
// alert must never fire for has to have NO series, not a zero one.
func gaugeFor(t *testing.T, vec *prometheus.GaugeVec, org string) (float64, bool) {
	t.Helper()
	ch := make(chan prometheus.Metric, 256)
	go func() {
		vec.Collect(ch)
		close(ch)
	}()
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatalf("read gauge: %v", err)
		}
		for _, l := range pb.GetLabel() {
			if l.GetName() == "org" && l.GetValue() == org {
				return pb.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

// TestCollectOverageReportsConsumptionAgainstAllowance is the alert's contract:
// the org and its plan must be readable off the metric, and the threshold has
// to travel with the series. A bare consumption gauge would force the rule to
// hardcode one number for every plan, and the number that matters most (the
// free tier's) is the one no plan price contains.
func TestCollectOverageReportsConsumptionAgainstAllowance(t *testing.T) {
	pool := testCollectorPool(t)
	org := seedOrgLedger(t, pool, "free", 400)

	collectOverage(context.Background(), pool, map[string]float64{"free": 164.72})

	spent, ok := gaugeFor(t, orgUsageMonthRub, org)
	if !ok {
		t.Fatalf("no dada_org_usage_month_rub series for org %q; the alert cannot name an org it has no label for", org)
	}
	if spent != 400 {
		t.Errorf("month-to-date consumption = %v, want 400", spent)
	}
	included, ok := gaugeFor(t, orgUsageAllowanceRub, org)
	if !ok {
		t.Fatalf("no dada_org_usage_allowance_rub series for org %q; the rule would have nothing to compare against", org)
	}
	if included != 164.72 {
		t.Errorf("allowance = %v, want 164.72", included)
	}
}

// TestCollectOverageSkipsPlansWithoutAllowance keeps enterprise quiet.
// Enterprise is a negotiated contract with zero price and zero footprint in
// plans.yaml, so a derived allowance of zero would put every enterprise account
// permanently over its limit -- an alert that fires forever is an alert nobody
// reads.
func TestCollectOverageSkipsPlansWithoutAllowance(t *testing.T) {
	pool := testCollectorPool(t)
	org := seedOrgLedger(t, pool, "enterprise", 9000)

	collectOverage(context.Background(), pool, map[string]float64{"free": 164.72, "enterprise": 0})

	if v, ok := gaugeFor(t, orgUsageMonthRub, org); ok {
		t.Errorf("enterprise org %q produced a consumption series (%v); with no allowance it can only alert forever", org, v)
	}
	if v, ok := gaugeFor(t, orgUsageAllowanceRub, org); ok {
		t.Errorf("enterprise org %q produced an allowance series (%v); zero is 'undefined', not a limit", org, v)
	}
}

// TestCollectOverageDropsOrgsThatStoppedConsuming guards the Reset(). Every
// org's consumption falls to zero on the first of the month; a series frozen at
// last month's value would carry a settled overage into a month the customer
// has already been billed for.
func TestCollectOverageDropsOrgsThatStoppedConsuming(t *testing.T) {
	pool := testCollectorPool(t)
	org := seedOrgLedger(t, pool, "free", 400)
	allowance := map[string]float64{"free": 164.72}

	collectOverage(context.Background(), pool, allowance)
	if _, ok := gaugeFor(t, orgUsageMonthRub, org); !ok {
		t.Fatalf("seeded org %q produced no series", org)
	}

	if _, err := pool.Exec(context.Background(),
		`DELETE FROM app_usage WHERE org_id = $1`, org); err != nil {
		t.Fatalf("clear ledger: %v", err)
	}
	collectOverage(context.Background(), pool, allowance)

	if v, ok := gaugeFor(t, orgUsageMonthRub, org); ok {
		t.Errorf("org %q has no ledger rows left but still reports %v consumed; the alert would keep firing", org, v)
	}
}
