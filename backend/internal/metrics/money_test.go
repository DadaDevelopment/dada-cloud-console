package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// gaugeVecValue reads one labelled series out of a GaugeVec, reporting whether
// it exists. These gauges aggregate over shared tables, so every assertion in
// this file is a DELTA around a baseline: another test's rows (or a real one)
// must not be able to decide the verdict.
func gaugeVecValue(t *testing.T, vec *prometheus.GaugeVec, want map[string]string) (float64, bool) {
	t.Helper()
	ch := make(chan prometheus.Metric, 128)
	go func() {
		vec.Collect(ch)
		close(ch)
	}()
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatalf("read gauge: %v", err)
		}
		labels := map[string]string{}
		for _, l := range pb.GetLabel() {
			labels[l.GetName()] = l.GetValue()
		}
		match := true
		for k, v := range want {
			if labels[k] != v {
				match = false
				break
			}
		}
		if match {
			return pb.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

// seedPendingPayment inserts one pending payments row aged by the given
// duration, with or without a YooKassa payment id behind it.
func seedPendingPayment(t *testing.T, pool *pgxpool.Pool, ykID string, age time.Duration) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	var yk any
	if ykID != "" {
		yk = ykID
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, yk_payment_id, created_by_sub, created_at, updated_at)
		VALUES ($1, $2, 'startup', 990, 'RUB', 'pending', $3, 'money-collector-test', now() - $4::interval, now())
	`, id, "money-collector-"+id.String()[:8], yk, age.String()); err != nil {
		t.Fatalf("seed pending payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1`, id)
	})
	return id
}

// TestCollectPendingPaymentsSeparatesTheDeadButton is the incident in a test.
//
// On 2026-08-14 a real buyer's rows sat pending with an empty yk_payment_id --
// the checkout call had failed, so no webhook was ever coming -- and looked
// identical to a customer who simply had not finished paying. The stage label
// is what makes the two different alerts, so it has to be derived from the
// provider id and not from status alone.
func TestCollectPendingPaymentsSeparatesTheDeadButton(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	collectPendingPayments(ctx, pool)
	baseProvider, _ := gaugeVecValue(t, paymentsPending, map[string]string{"stage": "awaiting_provider"})
	basePayment, _ := gaugeVecValue(t, paymentsPending, map[string]string{"stage": "awaiting_payment"})

	seedPendingPayment(t, pool, "", 3*time.Hour)
	seedPendingPayment(t, pool, "yk-"+uuid.NewString(), 3*time.Hour)
	collectPendingPayments(ctx, pool)

	gotProvider, ok := gaugeVecValue(t, paymentsPending, map[string]string{"stage": "awaiting_provider"})
	if !ok {
		t.Fatal("no dada_payments_pending series for stage=awaiting_provider")
	}
	if gotProvider-baseProvider != 1 {
		t.Errorf("awaiting_provider went %v -> %v, want +1: a payment that never reached YooKassa "+
			"must be counted apart from one the customer has not finished", baseProvider, gotProvider)
	}
	gotPayment, _ := gaugeVecValue(t, paymentsPending, map[string]string{"stage": "awaiting_payment"})
	if gotPayment-basePayment != 1 {
		t.Errorf("awaiting_payment went %v -> %v, want +1", basePayment, gotPayment)
	}

	age, ok := gaugeVecValue(t, paymentsPendingAge, map[string]string{"stage": "awaiting_provider"})
	if !ok {
		t.Fatal("no dada_payments_pending_age_seconds series for stage=awaiting_provider")
	}
	if age < 3*3600 {
		t.Errorf("oldest awaiting_provider age = %vs for a row seeded 3h ago, want >= 10800; "+
			"DadaPaymentStuckPending would never cross its threshold", age)
	}
}

// TestCollectPendingPaymentsZeroesSettledStages guards the explicit zeroing.
// A stage with no rows must report 0, not vanish: an alert on a missing series
// is an alert that never fires, and "no pending payments" is exactly the state
// the on-call wants confirmed rather than inferred from silence.
func TestCollectPendingPaymentsZeroesSettledStages(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	id := seedPendingPayment(t, pool, "", 3*time.Hour)
	collectPendingPayments(ctx, pool)
	before, _ := gaugeVecValue(t, paymentsPending, map[string]string{"stage": "awaiting_provider"})

	if _, err := pool.Exec(ctx, `UPDATE payments SET status = 'canceled' WHERE id = $1`, id); err != nil {
		t.Fatalf("settle payment: %v", err)
	}
	collectPendingPayments(ctx, pool)

	after, ok := gaugeVecValue(t, paymentsPending, map[string]string{"stage": "awaiting_provider"})
	if !ok {
		t.Fatal("stage=awaiting_provider series disappeared after the last row settled; " +
			"the alert must be able to see zero, not nothing")
	}
	if before-after != 1 {
		t.Errorf("awaiting_provider went %v -> %v after settling one row, want -1", before, after)
	}
}

// TestMoneyFunnelCountsPeopleNotClicks: one frustrated user pressing buy three
// times is one person considering a purchase. Counting events instead would
// have made the checkout outage of 2026-08-14 -- two clicks, one buyer, zero
// payments -- read as demand.
func TestMoneyFunnelCountsPeopleNotClicks(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	collectMoneyFunnel(ctx, pool)
	base, _ := gaugeVecValue(t, moneyFunnel, map[string]string{"step": "clicked_buy"})

	anon := uuid.New()
	for i := 0; i < 3; i++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO ux_events (anon_id, event_type, path, target, occurred_at)
			VALUES ($1, 'click', '/projects', 'upgrade_dialog:checkout:startup', now())
		`, anon); err != nil {
			t.Fatalf("seed ux event: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM ux_events WHERE anon_id = $1`, anon)
	})

	collectMoneyFunnel(ctx, pool)
	got, ok := gaugeVecValue(t, moneyFunnel, map[string]string{"step": "clicked_buy"})
	if !ok {
		t.Fatal("no dada_money_funnel_7d series for step=clicked_buy")
	}
	if got-base != 1 {
		t.Errorf("clicked_buy went %v -> %v after one person clicked three times, want +1", base, got)
	}
}

// TestMoneyFunnelSawOfferExcludesTheBuyClick: the buy button lives under the
// same upgrade_dialog: prefix as the modal view, so a naive LIKE would count
// every buyer twice -- once as having seen the offer, once again as having
// seen it -- and quietly inflate the top of the funnel.
func TestMoneyFunnelSawOfferExcludesTheBuyClick(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	collectMoneyFunnel(ctx, pool)
	base, _ := gaugeVecValue(t, moneyFunnel, map[string]string{"step": "saw_offer"})

	anon := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ux_events (anon_id, event_type, path, target, occurred_at)
		VALUES ($1, 'view', '/projects', 'upgrade_dialog:checkout:startup', now())
	`, anon); err != nil {
		t.Fatalf("seed ux event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM ux_events WHERE anon_id = $1`, anon)
	})

	collectMoneyFunnel(ctx, pool)
	got, _ := gaugeVecValue(t, moneyFunnel, map[string]string{"step": "saw_offer"})
	if got != base {
		t.Errorf("saw_offer went %v -> %v on a checkout target, want unchanged", base, got)
	}
}

// TestCollectFeedbackCountsUnreadRows: at four paying customers a written
// complaint is rarer than a payment, and until now it landed in a table nobody
// was told about.
func TestCollectFeedbackCountsUnreadRows(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	collectFeedback(ctx, pool)
	base, _ := gaugeVecValue(t, feedbackOpen, map[string]string{"status": "new"})

	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO feedback (route, message, created_at)
		VALUES ('/billing', 'money-collector-test', now() - interval '2 hours') RETURNING id
	`).Scan(&id); err != nil {
		t.Fatalf("seed feedback: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM feedback WHERE id = $1`, id)
	})

	collectFeedback(ctx, pool)
	got, ok := gaugeVecValue(t, feedbackOpen, map[string]string{"status": "new"})
	if !ok {
		t.Fatal("no dada_feedback_open series for status=new")
	}
	if got-base != 1 {
		t.Errorf("unread feedback went %v -> %v, want +1", base, got)
	}
}

// TestCollectDegradedOrgsSeesBlockedAndDeclined covers the two populations the
// platform punishes silently: a free org past its grace window that is still
// over the limits (its next create fails, and nothing warned it), and a paying
// org whose recurring charge keeps declining (it lapses to free without ever
// deciding to).
func TestCollectDegradedOrgsSeesBlockedAndDeclined(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	collectDegradedOrgs(ctx, pool)
	var baseLocked, baseFailing dto.Metric
	if err := orgsQuotaLocked.Write(&baseLocked); err != nil {
		t.Fatalf("read quota-locked gauge: %v", err)
	}
	if err := autopayFailingOrgs.Write(&baseFailing); err != nil {
		t.Fatalf("read autopay gauge: %v", err)
	}

	lockedOrg := "money-collector-locked-" + uuid.NewString()[:8]
	failingOrg := "money-collector-autopay-" + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, quota_grace_until, quota_breach_count, updated_at)
		VALUES ($1, 'free', now(), now() - interval '1 day', 3, now())
	`, lockedOrg); err != nil {
		t.Fatalf("seed locked org: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, autopay_enabled, autopay_failures, updated_at)
		VALUES ($1, 'startup', now(), TRUE, 2, now())
	`, failingOrg); err != nil {
		t.Fatalf("seed autopay-failing org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM billing_accounts WHERE org_id = ANY($1)`, []string{lockedOrg, failingOrg})
	})

	collectDegradedOrgs(ctx, pool)
	var locked, failing dto.Metric
	if err := orgsQuotaLocked.Write(&locked); err != nil {
		t.Fatalf("read quota-locked gauge: %v", err)
	}
	if err := autopayFailingOrgs.Write(&failing); err != nil {
		t.Fatalf("read autopay gauge: %v", err)
	}
	if got := locked.GetGauge().GetValue() - baseLocked.GetGauge().GetValue(); got != 1 {
		t.Errorf("quota-locked orgs moved by %v, want +1: an org past its grace while over the "+
			"limits is a user whose next create fails with no warning", got)
	}
	if got := failing.GetGauge().GetValue() - baseFailing.GetGauge().GetValue(); got != 1 {
		t.Errorf("autopay-failing orgs moved by %v, want +1", got)
	}
}

// TestPaymentSweepAgeFallsBackToProcessStart is the whole point of the sweeper
// gauge: a sweeper that has never run must age out and alert. If the gauge only
// existed after the first pass, "the sweeper never started" would be a missing
// series -- indistinguishable from a healthy one to any threshold rule.
func TestPaymentSweepAgeFallsBackToProcessStart(t *testing.T) {
	lastPaymentSweepUnix.Store(0)
	processStartUnix = time.Now().Add(-3 * time.Hour).Unix()
	t.Cleanup(func() {
		processStartUnix = time.Now().Unix()
		lastPaymentSweepUnix.Store(0)
	})

	var pb dto.Metric
	collectSweepAge(t, &pb)
	if pb.GetGauge().GetValue() < 3*3600 {
		t.Errorf("sweep age = %v with no pass ever completed and process started 3h ago, want >= 10800",
			pb.GetGauge().GetValue())
	}

	MarkPaymentSweep(time.Now())
	collectSweepAge(t, &pb)
	if pb.GetGauge().GetValue() > 60 {
		t.Errorf("sweep age = %v right after a completed pass, want ~0", pb.GetGauge().GetValue())
	}
}

// collectSweepAge drives the production refresh and reads the gauge back. It
// does not touch the database, so the liveness contract stays testable on its
// own.
func collectSweepAge(t *testing.T, pb *dto.Metric) {
	t.Helper()
	refreshSweepAge()
	if err := paymentSweepAge.Write(pb); err != nil {
		t.Fatalf("read sweep age gauge: %v", err)
	}
}
