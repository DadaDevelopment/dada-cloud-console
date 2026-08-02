package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// readLastBuildNotify returns the newest SendBuildNotification row for a
// project, which is what an operator asking "did we tell them?" would read.
func readLastBuildNotify(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID) (outcome string, meta map[string]any, envID *uuid.UUID) {
	t.Helper()
	var raw []byte
	err := pool.QueryRow(context.Background(),
		`SELECT outcome, metadata, environment_id FROM audit_events
		  WHERE project_id = $1 AND action = 'SendBuildNotification'
		  ORDER BY created_at DESC LIMIT 1`, projectID).Scan(&outcome, &raw, &envID)
	if err != nil {
		t.Fatalf("expected a SendBuildNotification row, got error: %v", err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	return outcome, meta, envID
}

// TestRecordBuildNotify_SuccessAndFailure pins both halves of the answer the
// build-agent logs used to lose on the next pod restart: the mail went out, or
// it did not and here is why.
func TestRecordBuildNotify_SuccessAndFailure(t *testing.T) {
	pool := testPool(t)
	projectID, envID := seedProjectEnv(t, pool, "small")
	buildID := uuid.New()

	RecordBuildNotify(context.Background(), pool, projectID, envID, buildID, "shop", "success", "", nil)
	outcome, meta, gotEnv := readLastBuildNotify(t, pool, projectID)
	if outcome != "success" {
		t.Fatalf("outcome = %q, want success", outcome)
	}
	if meta["build_status"] != "success" {
		t.Fatalf("build_status = %v, want success", meta["build_status"])
	}
	if gotEnv == nil || *gotEnv != envID {
		t.Fatalf("environment_id = %v, want %v", gotEnv, envID)
	}

	RecordBuildNotify(context.Background(), pool, projectID, envID, buildID, "shop", "failure", "send_failed",
		errors.New("smtp: 550 mailbox unavailable"))
	outcome, meta, _ = readLastBuildNotify(t, pool, projectID)
	if outcome != "failure" {
		t.Fatalf("outcome = %q, want failure", outcome)
	}
	if meta["reason"] != "send_failed" {
		t.Fatalf("reason = %v, want send_failed", meta["reason"])
	}
	if !strings.Contains(meta["detail"].(string), "550") {
		t.Fatalf("detail lost the transport error: %v", meta["detail"])
	}
}

// TestRecordBuildNotify_NoRecipientIsRecorded covers the branch that used to
// return in total silence: the project owner has no address, so nobody was
// ever going to hear about the failed build. That is the row support needs
// most, and it carries no address precisely because there is none.
func TestRecordBuildNotify_NoRecipientIsRecorded(t *testing.T) {
	pool := testPool(t)
	projectID, envID := seedProjectEnv(t, pool, "small")

	RecordBuildNotify(context.Background(), pool, projectID, envID, uuid.New(), "shop", "failure", "no_recipient", nil)

	outcome, meta, _ := readLastBuildNotify(t, pool, projectID)
	if outcome != "failure" {
		t.Fatalf("outcome = %q, want failure", outcome)
	}
	if meta["reason"] != "no_recipient" {
		t.Fatalf("reason = %v, want no_recipient", meta["reason"])
	}
	if _, ok := meta["detail"]; ok {
		t.Fatalf("no_recipient must carry no detail, got %v", meta["detail"])
	}
}

// TestRecordBuildNotify_TruncatesLongErrorSafely feeds a multi-byte error past
// the cap: a byte-slice cut would split a rune and hand Postgres invalid UTF-8,
// failing the very row meant to record the failure.
func TestRecordBuildNotify_TruncatesLongErrorSafely(t *testing.T) {
	pool := testPool(t)
	projectID, envID := seedProjectEnv(t, pool, "small")

	RecordBuildNotify(context.Background(), pool, projectID, envID, uuid.New(), "shop", "failure", "send_failed",
		errors.New(strings.Repeat("ошибка", 200)))

	_, meta, _ := readLastBuildNotify(t, pool, projectID)
	detail, _ := meta["detail"].(string)
	if got := len([]rune(detail)); got != buildNotifyErrorMaxLen {
		t.Fatalf("detail runes = %d, want %d", got, buildNotifyErrorMaxLen)
	}
}
