// Package metrics exposes Prometheus gauges derived from the console's own
// state tables so stuck or failed resource operations page the platform team
// instead of a user noticing first.
//
// The console is a state machine: every create/attach (domain, build, app,
// database, bucket) writes a row to operations. For most actions the terminal
// state is Committed (the change reached git); the real outcome then lives
// downstream (cert-manager issuing a cert, Argo turning the app Healthy). Two
// failure shapes therefore matter and both are surfaced here:
//
//   - Synchronous failures land as operations.status = 'Failed'.
//   - Silent stalls (e.g. a custom domain committed to git whose ACME
//     challenge never solves) leave a domain_hostnames row pending forever.
//
// cert-manager is not scraped on this cluster, so hostname/cert health has to
// come from these DB-derived gauges rather than certmanager_* series.
package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

var (
	operations = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_operations",
		Help: "Console operations grouped by action and status. status=\"Failed\" is an actionable failure (build/appserver/etc did not complete).",
	}, []string{"action", "status"})

	domainHostnames = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_domain_hostnames",
		Help: "Attached custom hostnames grouped by status (pending|active|failed) and cert_status. status!=\"active\" means the domain has not finished attaching.",
	}, []string{"status", "cert_status"})

	operationsFailedRecent = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_operations_failed_recent",
		Help: "Operations that reached status='Failed' within the last hour, by action. Alert on >0; it clears itself as failures age out so it tracks live breakage (broken build, failed DB/bucket/appserver create) rather than historical totals.",
	}, []string{"action"})

	domainHostnamePendingAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_domain_hostname_pending_age_seconds",
		Help: "Age of the oldest custom hostname that is not yet active. 0 when every hostname is active. A high value means a domain has been silently stuck attaching.",
	})

	collectErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "dada_metrics_collect_errors_total",
		Help: "DB query errors while refreshing dada_* state gauges.",
	})

	collectDuration = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_metrics_collect_duration_seconds",
		Help: "Wall-clock duration of the last state-gauge refresh.",
	})
)

// Handler returns the /metrics HTTP handler over the default registry.
func Handler() http.Handler { return promhttp.Handler() }

// StartCollector launches a background goroutine that refreshes the state
// gauges from the database every interval until ctx is cancelled. It runs one
// refresh synchronously first so /metrics is populated before the first scrape.
func StartCollector(ctx context.Context, pool *pgxpool.Pool, interval time.Duration) {
	collect(ctx, pool)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				collect(ctx, pool)
			}
		}
	}()
}

func collect(ctx context.Context, pool *pgxpool.Pool) {
	start := time.Now()
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if rows, err := pool.Query(c,
		`SELECT action, status, count(*) FROM operations GROUP BY action, status`); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: operations query failed")
	} else {
		operations.Reset()
		for rows.Next() {
			var action, status string
			var n float64
			if err := rows.Scan(&action, &status, &n); err != nil {
				collectErrors.Inc()
				continue
			}
			operations.WithLabelValues(action, status).Set(n)
		}
		rows.Close()
	}

	if rows, err := pool.Query(c,
		`SELECT action, count(*) FROM operations
		  WHERE status = 'Failed' AND updated_at > now() - interval '1 hour'
		  GROUP BY action`); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: recent-failed query failed")
	} else {
		operationsFailedRecent.Reset()
		for rows.Next() {
			var action string
			var n float64
			if err := rows.Scan(&action, &n); err != nil {
				collectErrors.Inc()
				continue
			}
			operationsFailedRecent.WithLabelValues(action).Set(n)
		}
		rows.Close()
	}

	if rows, err := pool.Query(c,
		`SELECT status, cert_status, count(*) FROM domain_hostnames GROUP BY status, cert_status`); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: domain_hostnames query failed")
	} else {
		domainHostnames.Reset()
		for rows.Next() {
			var status, certStatus string
			var n float64
			if err := rows.Scan(&status, &certStatus, &n); err != nil {
				collectErrors.Inc()
				continue
			}
			domainHostnames.WithLabelValues(status, certStatus).Set(n)
		}
		rows.Close()
	}

	var age float64
	if err := pool.QueryRow(c,
		`SELECT COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at))), 0)
		   FROM domain_hostnames WHERE status = 'pending'`).Scan(&age); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: pending-hostname age query failed")
	} else {
		domainHostnamePendingAge.Set(age)
	}

	collectDuration.Set(time.Since(start).Seconds())
}
