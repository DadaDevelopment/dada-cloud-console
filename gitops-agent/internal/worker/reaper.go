package worker

import (
	"context"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Reaper is the TTL sweep for ephemeral preview environments. It runs
// alongside DBWatcher but is a separate ticker: DBWatcher only reacts to rows
// already in the operations table, while Reaper is the thing that PUTS rows
// there once a preview environment's expires_at has passed. It also doubles as
// orphan cleanup for any PR "closed" webhook that never fired (per Phase 1 of
// the preview-env plan) — an expired-but-live environment gets torn down here
// regardless of why the normal close path missed it.
type Reaper struct {
	pool *pgxpool.Pool
	cfg  *config.Config
}

// NewReaper constructs a Reaper sharing DBWatcher's pool and config, so
// PreviewReapInterval / PreviewEnvTTL stay a single source of truth.
func NewReaper(pool *pgxpool.Pool, cfg *config.Config) *Reaper {
	return &Reaper{pool: pool, cfg: cfg}
}

// Start runs the reap loop until ctx is cancelled. Ticks on
// cfg.PreviewReapInterval (default 10m); each tick is independent, so a slow
// or failed tick never blocks the next one.
func (r *Reaper) Start(ctx context.Context) {
	log.Info().Dur("interval", r.cfg.PreviewReapInterval).Msg("preview-env reaper started")
	ticker := time.NewTicker(r.cfg.PreviewReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reap(ctx)
		}
	}
}

// reap enqueues DeletePreviewEnv for every expired preview environment that
// does not already have a teardown in flight. Failures are logged, not
// fatal — the next tick retries.
func (r *Reaper) reap(ctx context.Context) {
	ids, err := db.ReapExpiredPreviewEnvs(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("preview-env reaper: sweep failed")
		return
	}
	if len(ids) == 0 {
		return
	}
	log.Info().Int("count", len(ids)).Msg("preview-env reaper: enqueued teardown for expired preview environments")
}
