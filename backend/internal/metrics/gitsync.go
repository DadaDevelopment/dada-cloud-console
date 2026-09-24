package metrics

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
)

var (
	gitSyncConsecutiveFailures = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_gitops_sync_consecutive_failures",
		Help: "Consecutive git-watcher sync failures per gitops repo, as recorded by gitops-agent. While this is above zero the agent cannot pull its clone, and every operation that pulls before rendering fails too, so no deploy on the platform goes through. 2026-09-24: a clone wedged on a stranded commit failed every 30s tick for 1h37m before anyone looked.",
	}, []string{"repo", "branch"})

	gitSyncSecondsSinceSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_gitops_sync_seconds_since_success",
		Help: "Seconds since gitops-agent last synced each gitops repo successfully. It also grows when the agent is down or not reporting at all, which the failure counter alone cannot show.",
	}, []string{"repo", "branch"})
)

// collectGitSync exports the git watcher's health from git_sync_state. Rows
// that never recorded an attempt (written before gitops-agent reported health)
// are skipped so they cannot read as an agent that stopped syncing.
func collectGitSync(c context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(c, `
		SELECT repo_url, branch, consecutive_failures, last_success_at
		FROM   git_sync_state
		WHERE  last_attempt_at IS NOT NULL`)
	if err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: git sync health query failed")
		return
	}
	defer rows.Close()

	gitSyncConsecutiveFailures.Reset()
	gitSyncSecondsSinceSuccess.Reset()
	now := time.Now()
	for rows.Next() {
		var repo, branch string
		var failures float64
		var lastSuccess *time.Time
		if err := rows.Scan(&repo, &branch, &failures, &lastSuccess); err != nil {
			collectErrors.Inc()
			continue
		}
		gitSyncConsecutiveFailures.WithLabelValues(repo, branch).Set(failures)
		if lastSuccess != nil {
			gitSyncSecondsSinceSuccess.WithLabelValues(repo, branch).Set(now.Sub(*lastSuccess).Seconds())
		}
	}
}
