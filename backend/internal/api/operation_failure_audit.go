package api

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// recordOperationFailureAudit writes an outcome=failure audit row for an
// operation that died in a background worker.
//
// Every audit row a handler writes is written at enqueue time, when the only
// thing known is that the user asked. An action that is accepted and then fails
// asynchronously therefore stays outcome=success in audit_events forever, and
// path analysis counts a failed suspend, a failed resume, a failed deploy as
// things that worked. On prod that was 264 failed operations against a single
// failure row [live psql, 60d].
//
// The row is built from the operations row itself so it cannot disagree with it
// about actor, project, environment or resource, and it carries the same action
// as the success row: the pair reads as intent then result, told apart by
// outcome. The NOT EXISTS guard keeps a retried terminal write from stacking
// rows. The error text is truncated because it is an operator diagnostic, not a
// payload.
//
// Best-effort: the operation is already Failed and must not be resurrected by a
// bookkeeping error.
func recordOperationFailureAudit(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, reason, errMsg string) {
	if pool == nil {
		return
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events
			(actor_id, project_id, environment_id, operation_id, action, resource_kind, resource_name, outcome, metadata, actor_type)
		SELECT o.actor_id, o.project_id, o.environment_id, o.id, o.action, o.resource_kind, o.resource_name, 'failure',
		       jsonb_build_object('reason', $2::text, 'error', left($3::text, 300), 'phase', 'operation'),
		       CASE WHEN o.actor_id = '00000000-0000-0000-0000-000000000000'::uuid THEN 'system' ELSE 'user' END
		  FROM operations o
		 WHERE o.id = $1
		   AND NOT EXISTS (
			SELECT 1 FROM audit_events a
			 WHERE a.operation_id = o.id AND a.outcome = 'failure'
		   )
	`, id, reason, errMsg); err != nil {
		log.Warn().Err(err).Str("operation", id.String()).Msg("audit: failure row insert failed")
	}
}
