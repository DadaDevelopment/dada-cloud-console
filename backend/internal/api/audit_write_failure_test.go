package api

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestWriteAuditRowSurfacesUnresolvableFailure covers the incident this file
// exists for: a live user held twelve SessionStart rows and zero rows for
// every write-action they actually performed, because writeAuditRow
// (audit.go) returned bare on any Postgres error that was not one of the
// three known-resolvable foreign key violations. The failure left no log
// line, no metric, and obviously no row -- "audit_events lost a row" was
// indistinguishable from "the user never did that".
//
// actor_id is a foreign key writeAuditRow's retry loop does not know how to
// resolve (only project/environment/operation are handled), so a request
// carrying an actor that does not exist in users is the real failure mode
// this test drives: the insert fails, the loop's default branch is taken, and
// the row is dropped by construction, not by a stub.
func TestWriteAuditRowSurfacesUnresolvableFailure(t *testing.T) {
	pool := testAuditPool(t)
	ctx := context.Background()

	ghostActor := uuid.New()
	action := "CreateProject"
	counter := metrics.AuditWriteFailuresCollectorForTest(action, "fk_unresolved")
	before := testutil.ToFloat64(counter)

	writeAuditRow(ctx, pool, ghostActor, auditEntry{
		Action:       action,
		ResourceKind: "Project",
		ResourceName: "ghost-actor-project",
		Outcome:      auditOutcomeSuccess,
	})

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE actor_id = $1`, ghostActor,
	).Scan(&count); err != nil {
		t.Fatalf("query audit_events: %v", err)
	}
	if count != 0 {
		t.Fatalf("a row for a non-existent actor must not exist, found %d", count)
	}

	after := testutil.ToFloat64(counter)
	if after != before+1 {
		t.Fatalf("dropped write must be counted: before=%v after=%v", before, after)
	}
}
