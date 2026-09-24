package worker

import (
	"context"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// ReconcileRecorder returns the git reconcile hook that keeps the operations
// table truthful after a stranded commit is repaired: a replayed commit's
// operation is pointed at the SHA that is actually on the remote, and the
// operation of a dropped commit is put back in the queue so its change is
// written again rather than left Committed against a commit nobody has.
func ReconcileRecorder(pool *pgxpool.Pool) func(git.ReconcileResult) {
	return func(r git.ReconcileResult) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		for oldSHA, newSHA := range r.Replayed {
			if oldSHA == newSHA {
				continue
			}
			n, err := db.RepointCommit(ctx, pool, oldSHA, newSHA)
			if err != nil {
				log.Error().Err(err).Str("old", oldSHA).Str("new", newSHA).Msg("reconcile: repointing replayed commit")
				continue
			}
			log.Warn().Str("old", oldSHA).Str("new", newSHA).Int64("operations", n).Msg("reconcile: operations follow their replayed commit")
		}

		for _, c := range r.Dropped {
			raw := git.OperationIDFromMessage(c.Message)
			id, err := uuid.Parse(raw)
			if err != nil {
				log.Error().Str("sha", c.SHA).Str("operation", raw).
					Msg("reconcile: dropped a stranded commit that names no operation; its change is lost and needs a manual look")
				continue
			}
			requeued, err := db.RequeueStrandedOperation(ctx, pool, id, c.SHA)
			if err != nil {
				log.Error().Err(err).Str("operation", id.String()).Msg("reconcile: re-queuing operation of dropped commit")
				continue
			}
			log.Warn().Str("operation", id.String()).Str("sha", c.SHA).Bool("requeued", requeued).
				Msg("reconcile: stranded commit dropped; operation re-queued so its change is written again")
		}
	}
}
