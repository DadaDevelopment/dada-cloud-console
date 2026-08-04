package api

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// appBackfillStep is how long the backfill waits between hours.
//
// The pass is a one-off catching up on weeks, not a loop that has to finish by
// any particular moment, and each hour costs six range queries against the same
// Mimir that serves the console's metrics pages. Pacing it means a customer
// opening a dashboard while the platform reconstructs three weeks of history
// does not wait behind five hundred hours of range queries.
const appBackfillStep = 2 * time.Second

// appBackfillSkipRecentHours is the tail of the timeline the backfill refuses to
// touch. Those hours belong to the live meter, which reaches back
// appMeterMaxLagHours on every tick and would fight the backfill for them.
const appBackfillSkipRecentHours = appMeterMaxLagHours + 1

// StartAppUsageBackfill reconstructs the ledger for hours that predate it, once,
// in the background.
//
// The ledger was born on 2026-08-04 and can only answer for hours after its own
// birth. The question that produced it was about the weeks before -- whether an
// account that consumed for a month would ever be shown to have consumed
// anything. Local Prometheus cannot answer that (three days), but the same
// kube-state series are remote_written into Mimir under a single tenant and
// survive about three weeks there, which covers the whole current calendar month
// and most of the previous one.
//
// It is a one-shot rather than a loop: it stops at the first hour that already
// has rows, and after one full pass every hour in range has rows, so the next
// boot finds nothing to do. That self-termination is why it needs no cursor
// table and no lock -- a second replica racing it writes the same numbers into
// the same primary key, and reconstruction never overwrites a measurement.
func (h *Handler) StartAppUsageBackfill(ctx context.Context) {
	days := h.cfg.AppUsageBackfillDays
	if days <= 0 {
		log.Info().Msg("app usage backfill disabled")
		return
	}
	src := h.backfillMetricsSource()
	if src.client == nil {
		log.Warn().Msg("app usage backfill NOT started: no long-retention metrics store configured")
		return
	}
	go h.RunAppUsageBackfill(ctx, days, src)
}

// backfillMetricsSource points the backfill at the long-retention store.
//
// It reuses the multitenant Mimir client the console already has for user
// metrics, because it is the same Mimir -- the difference is only which tenant
// is asked. Cluster-state series land under the tenant Prometheus stamps on its
// remote_write (opencost, historically, because OpenCost is what the pipeline
// was built for), which has nothing to do with any customer's org and is
// therefore configurable rather than derived.
func (h *Handler) backfillMetricsSource() appMetricsSource {
	return appMetricsSource{
		client:        h.userMetrics,
		tenant:        h.cfg.AppUsageBackfillTenant,
		runningFilter: podContainerRunning,
	}
}

// RunAppUsageBackfill walks hours newest-first and meters the ones with no rows.
//
// Newest-first is the useful order: the current calendar month is what a bill or
// an overage alert is computed from, so those hours become true first and the
// older ones are context. Exported so an operator can drive one pass without
// waiting for a restart.
func (h *Handler) RunAppUsageBackfill(ctx context.Context, days int, src appMetricsSource) {
	start := time.Now()
	now := h.nowUTC().Truncate(time.Hour)
	oldest := now.Add(-time.Duration(days) * 24 * time.Hour)
	newest := now.Add(-time.Duration(appBackfillSkipRecentHours) * time.Hour)

	var hours, rows int
	for hour := newest; !hour.Before(oldest); hour = hour.Add(-time.Hour) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(appBackfillStep):
		}
		metered, err := h.appHourAlreadyMetered(ctx, hour)
		if err != nil {
			log.Warn().Err(err).Time("hour", hour).Msg("app usage backfill: cannot tell whether hour is metered")
			continue
		}
		if metered {
			continue
		}
		written, apps, err := h.meterAppHourFrom(ctx, hour, src, appUsageSourceBackfill)
		if err != nil {
			log.Warn().Err(err).Time("hour", hour).Msg("app usage backfill: hour failed")
			continue
		}
		hours++
		rows += written
		if apps > 0 {
			log.Debug().Time("hour", hour).Int("rows", written).Int("apps", apps).Msg("app usage backfill: hour reconstructed")
		}
	}
	log.Info().Int("hours", hours).Int("rows", rows).Int("days", days).
		Dur("took", time.Since(start)).Msg("app usage backfill: pass complete")
}
