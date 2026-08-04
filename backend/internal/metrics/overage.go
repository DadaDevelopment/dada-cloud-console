package metrics

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
)

var (
	orgUsageMonthRub = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_org_usage_month_rub",
		Help: "List-price consumption an org has accrued in the app_usage ledger since the start of the current calendar month, labelled with the org and its plan. Compare against dada_org_usage_allowance_rub: consumption above the allowance is an account eating more than it pays for.",
	}, []string{"org", "plan"})

	orgUsageAllowanceRub = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_org_usage_allowance_rub",
		Help: "List-price consumption one calendar month of the org's plan includes. Emitted next to the consumption gauge so the alert threshold is per-plan data rather than a constant duplicated in the rule.",
	}, []string{"org", "plan"})
)

// overageSeriesLimit caps how many orgs get their own pair of series. The org
// label is user-adjacent (one per account), so an import or a signup wave would
// otherwise grow cardinality without a bound. The biggest consumers win the
// slots: they are the ones the overage alert is about, and an org too small to
// make the top of the list is by definition not over-consuming enough to page
// about.
const overageSeriesLimit = 100

// StartOverageCollector refreshes the per-org consumption and allowance gauges
// every interval until ctx is cancelled, running one refresh synchronously
// first so the series exist before the first scrape.
//
// allowance maps a plan key to the RUB of list-price consumption one month of
// that plan includes; plans missing from it (or mapped to zero) emit no
// allowance series and therefore cannot alert. Passing it in rather than
// reading plans.yaml here keeps this package what it already is -- gauges
// derived from the console's own state tables -- and leaves pricing in the one
// package that owns it.
func StartOverageCollector(ctx context.Context, pool *pgxpool.Pool, interval time.Duration, allowance map[string]float64) {
	collectOverage(ctx, pool, allowance)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				collectOverage(ctx, pool, allowance)
			}
		}
	}()
	log.Info().Int("plans_with_allowance", len(allowance)).Msg("org overage collector started")
}

// collectOverage rewrites both gauges from the ledger.
//
// The window is the current calendar month because that is the period an
// overage settles over: a rolling 30 days would carry last month's spike into a
// month the customer has already been billed for, and the alert would keep
// firing about money that is no longer in dispute.
//
// Both vectors are Reset() first. An org that stops consuming must lose its
// series rather than freeze at its last value forever -- and on the first of the
// month EVERY org's consumption drops to zero, which is the one moment where a
// frozen gauge would keep an alert firing for a month that has not started yet.
func collectOverage(ctx context.Context, pool *pgxpool.Pool, allowance map[string]float64) {
	if len(allowance) == 0 {
		return
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rows, err := pool.Query(c,
		`SELECT u.org_id, COALESCE(ba.plan, 'free'), SUM(u.cost_rub)::float8
		   FROM app_usage u
		   LEFT JOIN billing_accounts ba ON ba.org_id = u.org_id
		  WHERE u.hour_start >= date_trunc('month', now() AT TIME ZONE 'utc')
		    AND u.org_id <> ''
		  GROUP BY 1, 2
		  ORDER BY 3 DESC
		  LIMIT `+strconv.Itoa(overageSeriesLimit))
	if err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: month-to-date org consumption query failed")
		return
	}
	defer rows.Close()

	orgUsageMonthRub.Reset()
	orgUsageAllowanceRub.Reset()
	for rows.Next() {
		var org, plan string
		var spent float64
		if err := rows.Scan(&org, &plan, &spent); err != nil {
			collectErrors.Inc()
			continue
		}
		included, ok := allowance[plan]
		if !ok || included <= 0 {
			continue
		}
		orgUsageMonthRub.WithLabelValues(org, plan).Set(spent)
		orgUsageAllowanceRub.WithLabelValues(org, plan).Set(included)
	}
	if err := rows.Err(); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: month-to-date org consumption read failed")
	}
}
