package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// boxUpMeasuredColdStartSeconds is the cold start measured in production on
// 2026-08-04: a box that reached ready in 175814ms after the pool came up empty.
//
// It is a constant in the test rather than a comment because it is what both
// bounds below have to clear. A change that drops either of them under this
// number reintroduces a door that cannot serve the request the product exists
// for, and the failure should name the number rather than a preference.
const boxUpMeasuredColdStartSeconds = 176

// TestBoxUpWaitBoundsClearAMeasuredColdStart guards the ceiling that made the
// synchronous door unusable.
//
// Both the default and the maximum were 120. A pool hit answers in seconds, so
// the number looked generous, but the production warm target is one: the second
// caller in a minute builds a body on the spot, and that takes about three
// minutes. The door answered them 400 "wait_seconds must be between 0 and 120",
// which is a rejection no value of wait_seconds could have avoided.
func TestBoxUpWaitBoundsClearAMeasuredColdStart(t *testing.T) {
	if boxUpDefaultWaitSeconds <= boxUpMeasuredColdStartSeconds {
		t.Errorf("boxUpDefaultWaitSeconds = %d, must exceed the measured cold start of %ds: "+
			"a caller who sends no wait_seconds must still be able to receive a cold-started box",
			boxUpDefaultWaitSeconds, boxUpMeasuredColdStartSeconds)
	}
	if boxUpMaxWaitSeconds < boxUpDefaultWaitSeconds {
		t.Errorf("boxUpMaxWaitSeconds = %d is below the default of %d, so the default is unreachable",
			boxUpMaxWaitSeconds, boxUpDefaultWaitSeconds)
	}
}

// TestClassifyBoxUpFailure_ColdStartIsNeverReportedAsPoolExhausted is the
// customer-facing half of the split ErrColdStart introduced in the box package.
//
// "pool_exhausted" is a dead end: it says the product is full, and nothing the
// caller does changes that. A cold start that ran past the caller's own bound is
// the opposite — the cluster had room, and a longer wait or a plain retry gets
// the box. Reporting the first when the second is true is how a first-time user
// concludes the product does not work.
func TestClassifyBoxUpFailure_ColdStartIsNeverReportedAsPoolExhausted(t *testing.T) {
	timedOut := box.Reject(box.ReasonColdStart,
		errors.Join(box.ErrColdStart, context.DeadlineExceeded))

	status, reason, advice := classifyBoxUpFailure(timedOut, false, 240)
	if status != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d: the caller's own bound expired", status, http.StatusGatewayTimeout)
	}
	if reason != "cold_start_timeout" {
		t.Errorf("reason = %q, want cold_start_timeout", reason)
	}
	if reason == "pool_exhausted" {
		t.Error("a cold-start timeout is reported as pool_exhausted")
	}
	if !strings.Contains(advice, "wait_seconds") {
		t.Errorf("advice = %q, must name the parameter that fixes this", advice)
	}
	if !strings.Contains(advice, "DELETE /projects/{projectId}/boxes/{boxName}") {
		t.Errorf("advice = %q, must name the delete: the failed box keeps its name and the retry hits 409", advice)
	}
}

// TestClassifyBoxUpFailure_RealExhaustionKeepsItsName is the other side of the
// split: the reason must still exist and still be reachable, or the alert that
// watches genuine capacity refusals would go quiet for the wrong reason.
func TestClassifyBoxUpFailure_RealExhaustionKeepsItsName(t *testing.T) {
	status, reason, _ := classifyBoxUpFailure(box.Reject(box.ReasonPoolExhausted, box.ErrPoolExhausted), false, 240)
	if status != http.StatusServiceUnavailable || reason != "pool_exhausted" {
		t.Fatalf("classify = (%d, %q), want (503, pool_exhausted)", status, reason)
	}
}

// TestRecordAuditDetached_SurvivesTheCallerHangingUp is the regression for the
// audit rows that were never written.
//
// The failures worth reading in the single-call door are the slow ones, and by
// the time they resolve the caller has usually given up — which cancels
// c.Request.Context(), which is the context the INSERT was executed on, which
// means pgx refuses it and the row is never written. Live evidence: a box
// carrying an error_message (written through a background context) while
// audit_events held nothing at all for the same failure.
//
// The control half of this test writes through recordAudit with the same
// cancelled context and asserts NOTHING lands, so the test fails if the
// detaching is removed rather than silently passing on a healthy context.
func TestRecordAuditDetached_SurvivesTheCallerHangingUp(t *testing.T) {
	pool := testBoxAuditPool(t)
	h := &Handler{pool: pool}
	actorID := seedBoxAuditActor(t, pool)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	livingName := "box-up-detached-" + uuid.NewString()[:8]
	t.Cleanup(func() { dropSeededAudit(pool, models.ResourceKindBox, livingName) })
	h.recordAuditDetached(cancelled, actorID, auditEntry{
		Action:       models.ActionBoxUp,
		ResourceKind: models.ResourceKindBox,
		ResourceName: livingName,
		Outcome:      auditOutcomeFailure,
		Metadata:     map[string]any{"reason": "cold_start_timeout"},
	})
	if countBoxAuditRows(t, pool, livingName) != 1 {
		t.Fatal("the rejection recorded after the caller hung up did not reach audit_events; " +
			"the one failure the trail exists to explain is the one it cannot")
	}

	lostName := "box-up-attached-" + uuid.NewString()[:8]
	t.Cleanup(func() { dropSeededAudit(pool, models.ResourceKindBox, lostName) })
	h.recordAudit(cancelled, actorID, auditEntry{
		Action:       models.ActionBoxUp,
		ResourceKind: models.ResourceKindBox,
		ResourceName: lostName,
		Outcome:      auditOutcomeFailure,
	})
	if countBoxAuditRows(t, pool, lostName) != 0 {
		t.Skip("this database accepts writes on a cancelled context, so the control half proves nothing here")
	}
}

func testBoxAuditPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping box audit DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedBoxAuditActor(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	suffix := uuid.NewString()[:8]
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, email, password_hash, display_name, keycloak_sub)
		 VALUES ($1, $2, '', 'box audit test', $3) RETURNING id`,
		"boxaudit-"+suffix, "boxaudit-"+suffix+"@example.test", uuid.NewString(),
	).Scan(&id); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	t.Cleanup(func() { dropSeededUser(pool, id) })
	return id
}

func countBoxAuditRows(t *testing.T, pool *pgxpool.Pool, resourceName string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE resource_name = $1`, resourceName).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}
