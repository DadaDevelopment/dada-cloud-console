package metrics

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
)

// The money gauges exist because the payment path had no witness.
//
// On 2026-08-14 the first outside buyer pressed "pay" twice and got nothing:
// both rows sat in payments as 'pending' with no YooKassa payment behind them.
// Nothing anywhere said so. The rows were found a day later by reading the
// table by hand, and only because someone went looking. Everything downstream
// of that -- "nobody wants the paid plan" -- was a conclusion drawn from a
// broken instrument.
//
// The gauges below are the instrument. They cover the four ways the money path
// can hurt a customer without anyone hearing about it:
//
//   - the payment gets stuck (dada_payments_pending_*),
//   - a webhook is lost and the sweeper has to repair it
//     (dada_payment_reconciled_total, dada_payment_sweep_age_seconds),
//   - the customer complains (dada_feedback_open),
//   - the customer gets degraded or blocked (dada_orgs_quota_locked,
//     dada_autopay_failing_orgs).
//
// Plus the funnel (dada_money_funnel_7d), which answers the question the
// alerts cannot: how many people saw the offer, how many pressed buy, and how
// many of those actually paid.
var (
	paymentsPending = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_payments_pending",
		Help: "Payments still in status='pending', by stage. stage=\"awaiting_provider\" has no yk_payment_id -- the YooKassa payment was never created, so no webhook is ever coming and the customer is looking at a dead button. stage=\"awaiting_payment\" is a real YooKassa payment the customer has not finished yet.",
	}, []string{"stage"})

	paymentsPendingAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_payments_pending_age_seconds",
		Help: "Age of the OLDEST pending payment in each stage. Rises whether the webhook died or the sweeper died, which is why the alert rides this rather than a webhook counter: both failures look the same to the customer, and both show up here.",
	}, []string{"stage"})

	paymentReconciled = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dada_payment_reconciled_total",
		Help: "Pending payments the hourly sweeper had to resolve itself, by outcome (succeeded|canceled|abandoned). Steady non-zero is not a healthy sweeper, it is a leaking webhook path being papered over: every 'succeeded' here is a customer who paid and waited up to an hour for the plan they already owned.",
	}, []string{"outcome"})

	paymentSweepAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_payment_sweep_age_seconds",
		Help: "Seconds since the pending-payment sweeper last completed a pass. Before the first pass of this process the age is measured from process start, deliberately: a sweeper that never ticks at all must age out and alert, not go missing and stay silent.",
	})

	feedbackOpen = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_feedback_open",
		Help: "Feedback rows by status. status=\"new\" is a user who took the trouble to complain and has had no answer yet.",
	}, []string{"status"})

	feedbackOldestAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_feedback_oldest_unread_age_seconds",
		Help: "Age of the oldest unanswered feedback row. Zero when the queue is empty.",
	})

	orgsQuotaLocked = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_orgs_quota_locked",
		Help: "Free orgs whose quota grace has run out while they are still over the free limits (quota_breach_count > 0). Every one of them is a user whose next create fails. This is the platform blocking a user, so it is news even at one.",
	})

	autopayFailingOrgs = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_autopay_failing_orgs",
		Help: "Paid orgs whose recurring charge is failing (autopay_failures > 0). These are existing customers about to lapse to free through no decision of their own.",
	})

	moneyFunnel = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_money_funnel_7d",
		Help: "Distinct people at each step of the upgrade funnel over the last 7 days: saw_offer -> clicked_locked -> clicked_buy -> returned -> stuck -> resumed, plus paid (succeeded payments, not people). Counted per person, not per event, so one user rage-clicking cannot look like demand.",
	}, []string{"step"})
)

// lastPaymentSweepUnix is when the pending-payment sweeper last finished a
// pass. Zero means it has not finished one in this process.
var lastPaymentSweepUnix atomic.Int64

// processStartUnix backs the sweep-age gauge before the first sweep. See the
// gauge Help: measuring from process start is what makes "the sweeper never
// ran" an alerting condition instead of a missing series.
var processStartUnix = time.Now().Unix()

// RecordPaymentReconciled counts one pending payment the sweeper resolved.
// outcome is bounded by the caller to the sweeper's own vocabulary; it never
// carries provider text.
func RecordPaymentReconciled(outcome string) {
	paymentReconciled.WithLabelValues(outcome).Inc()
}

// MarkPaymentSweep records that a sweeper pass completed. Called at the END of
// a pass on purpose: a sweeper that starts and then wedges mid-pass has not
// done its job, and stamping on entry would hide exactly that.
func MarkPaymentSweep(now time.Time) {
	lastPaymentSweepUnix.Store(now.Unix())
}

// collectMoney refreshes every money gauge. Wired into collect() rather than
// given its own ticker: these are cheap aggregate queries over small tables,
// and one refresh cadence is one thing to reason about when a number looks
// stale.
func collectMoney(c context.Context, pool *pgxpool.Pool) {
	collectPendingPayments(c, pool)
	collectFeedback(c, pool)
	collectDegradedOrgs(c, pool)
	collectMoneyFunnel(c, pool)

	refreshSweepAge()
}

// refreshSweepAge republishes how long it has been since a sweeper pass
// finished, falling back to process start before the first one.
func refreshSweepAge() {
	since := lastPaymentSweepUnix.Load()
	if since == 0 {
		since = processStartUnix
	}
	paymentSweepAge.Set(time.Since(time.Unix(since, 0)).Seconds())
}

// collectPendingPayments splits pending payments by whether a YooKassa payment
// exists behind them, because the two states are different incidents with the
// same status column: one is our checkout call failing, the other is the
// customer not having finished, or a webhook that never arrived.
func collectPendingPayments(c context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(c, `
		SELECT CASE WHEN yk_payment_id IS NULL OR yk_payment_id = ''
		            THEN 'awaiting_provider' ELSE 'awaiting_payment' END AS stage,
		       count(*),
		       COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at))), 0)
		  FROM payments
		 WHERE status = 'pending'
		 GROUP BY 1
	`)
	if err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: pending payments query failed")
		return
	}
	defer rows.Close()

	paymentsPending.Reset()
	paymentsPendingAge.Reset()
	for _, stage := range []string{"awaiting_provider", "awaiting_payment"} {
		paymentsPending.WithLabelValues(stage).Set(0)
		paymentsPendingAge.WithLabelValues(stage).Set(0)
	}
	for rows.Next() {
		var stage string
		var n, age float64
		if err := rows.Scan(&stage, &n, &age); err != nil {
			collectErrors.Inc()
			continue
		}
		paymentsPending.WithLabelValues(stage).Set(n)
		paymentsPendingAge.WithLabelValues(stage).Set(age)
	}
	if err := rows.Err(); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: pending payments read failed")
	}
}

// collectFeedback surfaces the complaint queue. A product with four paying
// customers cannot afford to read feedback on a schedule; one new row is worth
// a message.
func collectFeedback(c context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(c, `SELECT status, count(*) FROM feedback GROUP BY status`)
	if err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: feedback query failed")
		return
	}
	defer rows.Close()

	feedbackOpen.Reset()
	feedbackOpen.WithLabelValues("new").Set(0)
	for rows.Next() {
		var status string
		var n float64
		if err := rows.Scan(&status, &n); err != nil {
			collectErrors.Inc()
			continue
		}
		feedbackOpen.WithLabelValues(status).Set(n)
	}
	if err := rows.Err(); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: feedback read failed")
		return
	}

	var age float64
	if err := pool.QueryRow(c, `
		SELECT COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at))), 0)
		  FROM feedback WHERE status = 'new'
	`).Scan(&age); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: feedback age query failed")
		return
	}
	feedbackOldestAge.Set(age)
}

// collectDegradedOrgs counts the users the platform is currently punishing:
// free orgs past their grace window that are still over the limits, and paid
// orgs whose card keeps declining. Both populations are silent by design --
// the first only finds out when a create fails, the second when the plan
// lapses -- so the only way anyone hears about them first is a gauge.
func collectDegradedOrgs(c context.Context, pool *pgxpool.Pool) {
	var locked float64
	if err := pool.QueryRow(c, `
		SELECT count(*) FROM billing_accounts
		 WHERE plan = 'free'
		   AND quota_breach_count > 0
		   AND (quota_grace_until IS NULL OR quota_grace_until < now())
	`).Scan(&locked); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: quota-locked orgs query failed")
	} else {
		orgsQuotaLocked.Set(locked)
	}

	var failing float64
	if err := pool.QueryRow(c, `
		SELECT count(*) FROM billing_accounts
		 WHERE autopay_enabled AND autopay_failures > 0
	`).Scan(&failing); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: autopay-failing orgs query failed")
	} else {
		autopayFailingOrgs.Set(failing)
	}
}

// moneyFunnelSteps maps each funnel step to the ux_events rows that count as
// that step. The targets are the literal strings the console emits; they live
// here rather than in a shared constant because the console and this query are
// deliberately allowed to drift apart for one deploy -- a renamed target
// should make a step read zero, which is visible, not break the collector.
var moneyFunnelSteps = []struct {
	step      string
	eventType string
	pattern   string
	exclude   string
}{
	{"saw_offer", "view", "upgrade_dialog:%", "upgrade_dialog:checkout:%"},
	{"clicked_locked", "click", "catalog_locked:%", ""},
	{"clicked_buy", "click", "upgrade_dialog:checkout:%", ""},
	{"returned", "view", "checkout_return:%", ""},
	{"stuck", "view", "checkout_pending_stuck", ""},
	{"resumed", "click", "payment_resume", ""},
}

// collectMoneyFunnel counts distinct people per funnel step over 7 days.
//
// This is the digest the alerts cannot give: alerts fire when the path breaks,
// the funnel says whether anyone is walking it. A week of saw_offer with zero
// clicked_buy is a pricing or copy problem; clicked_buy without paid is a
// checkout problem, and the two need opposite responses.
//
// People, not events: an anonymous visitor is keyed by anon_id and a signed-in
// one by user_id, so a single frustrated user clicking buy six times counts
// once.
func collectMoneyFunnel(c context.Context, pool *pgxpool.Pool) {
	moneyFunnel.Reset()
	for _, s := range moneyFunnelSteps {
		var n float64
		err := pool.QueryRow(c, `
			SELECT count(DISTINCT COALESCE(user_id, anon_id, session_id))
			  FROM ux_events
			 WHERE occurred_at > now() - interval '7 days'
			   AND event_type = $1
			   AND target LIKE $2
			   AND ($3 = '' OR target NOT LIKE $3)
		`, s.eventType, s.pattern, s.exclude).Scan(&n)
		if err != nil {
			collectErrors.Inc()
			log.Warn().Err(err).Str("step", s.step).Msg("metrics: money funnel query failed")
			continue
		}
		moneyFunnel.WithLabelValues(s.step).Set(n)
	}

	var paid float64
	if err := pool.QueryRow(c, `
		SELECT count(*) FROM payments
		 WHERE status = 'succeeded' AND created_at > now() - interval '7 days'
	`).Scan(&paid); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: money funnel paid query failed")
		return
	}
	moneyFunnel.WithLabelValues("paid").Set(paid)
}
