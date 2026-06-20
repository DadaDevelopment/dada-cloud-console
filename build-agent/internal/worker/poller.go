// Package worker contains the build poller backstop and the per-build runner.
package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// Poller is the backstop trigger: a ticker that periodically drains the build
// queue. It catches builds enqueued while the webhook server was down plus
// manual/rollback triggers from the backend. Mirrors gitops-agent dbwatcher
// loop shape (time.Ticker + select on ctx.Done).
type Poller struct {
	interval time.Duration
	runner   *Runner
}

// NewPoller constructs a Poller.
func NewPoller(interval time.Duration, runner *Runner) *Poller {
	return &Poller{interval: interval, runner: runner}
}

// Start runs the poll loop until ctx is canceled.
func (p *Poller) Start(ctx context.Context) {
	log.Info().Dur("interval", p.interval).Msg("build poller started")
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.runner.DrainQueue(ctx)
		}
	}
}
