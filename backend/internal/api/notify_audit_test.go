package api

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRecordNotifySend_SuccessAndFailure pins the two rows support actually
// reads: "the owner was told" and "the send blew up, here is the transport
// error". Both must land under the same action so one query answers the
// question for every alerting path.
func TestRecordNotifySend_SuccessAndFailure(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, _ := seedOptimisticFixture(t, pool)
	h := &Handler{pool: pool}

	h.recordNotifySend(context.Background(), projectID, "VolumeAlert", "shop", alertSourceOwner, nil)
	outcome, reason, _ := lastAuditRow(t, pool, projectID, "SendNotification")
	if outcome != auditOutcomeSuccess {
		t.Fatalf("outcome = %q, want success", outcome)
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty on success", reason)
	}

	h.recordNotifySend(context.Background(), projectID, "AutofixFailed", "shop", alertSourceOperator, errors.New("smtp: 550 mailbox unavailable"))
	outcome, reason, _ = lastAuditRow(t, pool, projectID, "SendNotification")
	if outcome != auditOutcomeFailure {
		t.Fatalf("outcome = %q, want failure", outcome)
	}
	if reason != "send_failed" {
		t.Fatalf("reason = %q, want send_failed", reason)
	}
}

// TestRecordNotifySend_KeepsRecipientOutOfMetadata guards the 152-FZ boundary:
// the row says how the recipient was chosen, never who they are. A regression
// here quietly turns the audit journal into a mailing list.
func TestRecordNotifySend_KeepsRecipientOutOfMetadata(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, _ := seedOptimisticFixture(t, pool)
	h := &Handler{pool: pool}

	h.recordNotifySend(context.Background(), projectID, "AutoscaleCeiling", "shop", alertSourceOwner,
		errors.New("dial tcp smtp.example: connection refused for owner@example.com"))

	var meta map[string]any
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata FROM audit_events WHERE project_id = $1 AND action = 'SendNotification'
		  ORDER BY created_at DESC LIMIT 1`, projectID).Scan(&meta); err != nil {
		t.Fatalf("read audit metadata: %v", err)
	}
	for k, v := range meta {
		if k == "detail" {
			continue
		}
		if s, ok := v.(string); ok && strings.Contains(s, "@") {
			t.Fatalf("metadata key %q leaks an address: %q", k, s)
		}
	}
	if _, ok := meta["recipient_source"]; !ok {
		t.Fatal("metadata lost recipient_source, which is what makes the row readable")
	}
}

// TestRecordNotifySend_TruncatesLongErrorSafely feeds a multi-byte error longer
// than the cap. A byte-slice cut here would split a rune and hand Postgres
// invalid UTF-8, failing the very row meant to record a failure.
func TestRecordNotifySend_TruncatesLongErrorSafely(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, _ := seedOptimisticFixture(t, pool)
	h := &Handler{pool: pool}

	long := strings.Repeat("ошибка", 200)
	h.recordNotifySend(context.Background(), projectID, "VolumeAlert", "shop", alertSourceOwner, errors.New(long))

	var meta map[string]any
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata FROM audit_events WHERE project_id = $1 AND action = 'SendNotification'
		  ORDER BY created_at DESC LIMIT 1`, projectID).Scan(&meta); err != nil {
		t.Fatalf("read audit metadata: %v", err)
	}
	detail, _ := meta["detail"].(string)
	if got := len([]rune(detail)); got != notifySendErrorMaxLen {
		t.Fatalf("detail runes = %d, want %d", got, notifySendErrorMaxLen)
	}
}
