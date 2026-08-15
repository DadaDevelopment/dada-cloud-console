package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestWriteAuditRowSetsActorTypeAtInsert guards migration
// 124_audit_events_actor_type.sql / 125 / 126 and writeAuditRow's write path:
// actor_type must be decided by the writer, at the point of insert, not
// guessed later by a reader comparing actor_id to the nil uuid by hand.
//
// A human actor (recordAudit) must land as 'user'. The platform's own actor
// (recordSystemAudit, systemDeployActorID -- the seeded system user, all-zero
// uuid, migration 010_system_user.sql) must land as 'system', even though both
// rows carry the exact same action.
func TestWriteAuditRowSetsActorTypeAtInsert(t *testing.T) {
	pool := testAuditPool(t)
	actorID, projectID := seedAuditActor(t, pool)
	ctx := context.Background()

	action := "SetDatabaseTier" + uuid.NewString()[:8]
	h := &Handler{pool: pool}

	h.recordAudit(ctx, actorID, auditEntry{
		ProjectID:    projectID,
		Action:       action,
		ResourceKind: "ServiceDatabaseV2",
		ResourceName: "human-requested-tier-change",
		Outcome:      auditOutcomeSuccess,
	})
	h.recordSystemAudit(ctx, auditEntry{
		ProjectID:    projectID,
		Action:       action,
		ResourceKind: "ServiceDatabaseV2",
		ResourceName: "platform-requeued-tier-change",
		Outcome:      auditOutcomeSuccess,
	})
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE action = $1`, action)
	})

	var humanActorType string
	if err := pool.QueryRow(ctx,
		`SELECT actor_type FROM audit_events WHERE actor_id = $1 AND action = $2`,
		actorID, action,
	).Scan(&humanActorType); err != nil {
		t.Fatalf("read the human row's actor_type: %v", err)
	}
	if humanActorType != auditActorTypeUser {
		t.Fatalf("a row a person caused must be actor_type=%q, got %q", auditActorTypeUser, humanActorType)
	}

	var systemActorType string
	if err := pool.QueryRow(ctx,
		`SELECT actor_type FROM audit_events WHERE actor_id = $1 AND action = $2`,
		systemDeployActorID, action,
	).Scan(&systemActorType); err != nil {
		t.Fatalf("read the system row's actor_type: %v", err)
	}
	if systemActorType != auditActorTypeSystem {
		t.Fatalf("a row the platform wrote under systemDeployActorID must be actor_type=%q, got %q", auditActorTypeSystem, systemActorType)
	}
}

// TestUserActionSliceExcludesSystemRows is the regression this whole change
// exists to close: a SetDatabaseTier row db_tier_reconciler wrote under
// systemDeployActorID took first place in a TERMINAL-action breakdown and
// read as a user abandoning a database-tier change mid-flight, because that
// breakdown queried audit_events directly and nobody remembered to exclude
// the all-zero uuid.
//
// A "what did users do" slice built with the new actor_type column, instead
// of a hand-remembered actor_id comparison, must include the human's row and
// must not include the platform's -- even though both share the same action,
// resource_kind and outcome, and would be indistinguishable to a query that
// only looks at those columns.
func TestUserActionSliceExcludesSystemRows(t *testing.T) {
	pool := testAuditPool(t)
	actorID, projectID := seedAuditActor(t, pool)
	ctx := context.Background()

	action := "SetDatabaseTier" + uuid.NewString()[:8]
	h := &Handler{pool: pool}

	h.recordAudit(ctx, actorID, auditEntry{
		ProjectID:    projectID,
		Action:       action,
		ResourceKind: "ServiceDatabaseV2",
		ResourceName: "human-requested-tier-change",
		Outcome:      auditOutcomeSuccess,
	})
	h.recordSystemAudit(ctx, auditEntry{
		ProjectID:    projectID,
		Action:       action,
		ResourceKind: "ServiceDatabaseV2",
		ResourceName: "platform-requeued-tier-change",
		Outcome:      auditOutcomeSuccess,
	})
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE action = $1`, action)
	})

	rows, err := pool.Query(ctx,
		`SELECT actor_id FROM audit_events WHERE action = $1 AND actor_type = 'user'`,
		action,
	)
	if err != nil {
		t.Fatalf("query the user-only slice: %v", err)
	}
	defer rows.Close()

	var seen []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen = append(seen, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate rows: %v", err)
	}

	if len(seen) != 1 || seen[0] != actorID {
		t.Fatalf("the user-only slice must contain exactly the human actor %s, got %v", actorID, seen)
	}
	for _, id := range seen {
		if id == systemDeployActorID {
			t.Fatalf("the system actor must never appear in a slice filtered to actor_type='user'")
		}
	}
}
