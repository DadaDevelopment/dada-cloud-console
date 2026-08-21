package api

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// volatileFailureTokens matches the parts of a build error message that change
// on every run even when the failure is literally the same one: ISO-8601
// timestamps (npm writes its debug log as
// /root/.npm/_logs/2026-08-21T13_59_54_133Z-debug-0.log, both the "_" and the
// ":" spellings occur), uuids, and long hex ids (image/layer digests).
//
// tarotreaderhimu@gmail.com failed three times in a row on 2026-08-21 with the
// byte-identical npm install error, and an exact string comparison of
// error_message found zero repeats -- only the embedded log timestamp differed.
// Any repeat counter built on the raw field undercounts such streaks by 100%.
var volatileFailureTokens = []*regexp.Regexp{
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}[:_]\d{2}[:_]\d{2}[._]?\d*Z?`),
	regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`),
	regexp.MustCompile(`(?i)\b[0-9a-f]{12,}\b`),
}

// normalizeFailureDetail replaces every per-run token in a failure detail with
// a fixed placeholder so two runs of the same failure compare equal.
func normalizeFailureDetail(detail string) string {
	for _, re := range volatileFailureTokens {
		detail = re.ReplaceAllString(detail, "\x01")
	}
	return detail
}

// buildFailureRecord carries the columns countFailureRepeat needs from one
// build row, decoupled from the full build struct so the pure counting logic
// below never depends on database or JSON concerns.
type buildFailureRecord struct {
	Status       string
	FailReason   *string
	ErrorMessage *string
}

// failureSignature normalizes a build's failure into a comparable string so
// two builds can be judged "the same failure" or not.
//
// tarotreaderhimu@gmail.com hit dockerfile_build_failed three times in ten
// minutes on 2026-08-21 and, having no way to tell the third attempt from the
// first, created a database instead of fixing npm install -- the product gave
// zero signal that the same wall was being hit again. This is the building
// block for that signal: it mirrors frontend/lib/build-failure.ts's
// buildFailureDetail contract (error_message is stored as "<fail_reason>: "
// plus detail, and the detail alone is what varies meaningfully) and folds
// fail_reason back in so two different reasons with coincidentally similar
// detail text never compare equal.
//
// A build with neither fail_reason nor error_message returns "" on purpose:
// such builds carry no information to compare, so the caller must treat them
// as never matching anything, including each other.
func failureSignature(failReason *string, errorMessage *string) string {
	reason := ""
	if failReason != nil {
		reason = strings.TrimSpace(*failReason)
	}
	message := ""
	if errorMessage != nil {
		message = strings.TrimSpace(*errorMessage)
	}
	detail := message
	if reason != "" {
		prefix := reason + ": "
		if strings.HasPrefix(message, prefix) {
			detail = strings.TrimSpace(message[len(prefix):])
		}
	}
	if reason == "" && detail == "" {
		return ""
	}
	return reason + "\x00" + normalizeFailureDetail(detail)
}

// countFailureRepeat counts how many consecutive failures, ending at
// history[0], share the same failure. history must be ordered newest first
// and history[0] is the build being reported on; the count it returns
// includes that build, so a first-time failure reports 1 and a third
// identical one in a row reports 3.
//
// The walk stops at the first build that is not status=="failed", whose
// signature differs from history[0]'s, or whose signature is empty (an
// incomparable failure never extends -- and never starts -- a streak with
// anything, including a build that looks identical by coincidence).
func countFailureRepeat(history []buildFailureRecord) int {
	if len(history) == 0 || history[0].Status != "failed" {
		return 0
	}
	currentSig := failureSignature(history[0].FailReason, history[0].ErrorMessage)
	if currentSig == "" {
		return 1
	}
	count := 1
	for _, b := range history[1:] {
		if b.Status != "failed" {
			break
		}
		sig := failureSignature(b.FailReason, b.ErrorMessage)
		if sig == "" || sig != currentSig {
			break
		}
		count++
	}
	return count
}

// recentBuildFailuresBefore loads the app's build history immediately before
// createdAt/buildID (status, fail_reason, error_message), newest first,
// capped at 10 rows -- enough to resolve any realistic repeat streak without
// scanning an app's entire build history for every GetBuild call.
func (h *Handler) recentBuildFailuresBefore(ctx context.Context, environmentID uuid.UUID, appName string, createdAt time.Time, buildID uuid.UUID) ([]buildFailureRecord, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT status, fail_reason, error_message
		   FROM builds
		  WHERE environment_id = $1 AND app_name = $2
		    AND (created_at < $3 OR (created_at = $3 AND id < $4))
		  ORDER BY created_at DESC, id DESC
		  LIMIT 10`,
		environmentID, appName, createdAt, buildID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []buildFailureRecord
	for rows.Next() {
		var rec buildFailureRecord
		if err := rows.Scan(&rec.Status, &rec.FailReason, &rec.ErrorMessage); err != nil {
			return nil, err
		}
		history = append(history, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return history, nil
}

// buildRepeatCount resolves the repeat_count for a single failed build by
// querying its immediate predecessors. Callers must only invoke this for
// status=="failed" builds; it does not check status itself.
func (h *Handler) buildRepeatCount(ctx context.Context, environmentID uuid.UUID, appName string, createdAt time.Time, buildID uuid.UUID, failReason, errorMessage *string) (int, error) {
	prior, err := h.recentBuildFailuresBefore(ctx, environmentID, appName, createdAt, buildID)
	if err != nil {
		return 0, err
	}
	history := append([]buildFailureRecord{{Status: "failed", FailReason: failReason, ErrorMessage: errorMessage}}, prior...)
	return countFailureRepeat(history), nil
}

// annotateBuildRepeatCounts fills RepeatCount on every failed build in an
// already-fetched, newest-first history slice in a single pass, so listing an
// app's builds never issues one lookup per row.
func annotateBuildRepeatCounts(builds []build) {
	for i := range builds {
		if builds[i].Status != "failed" {
			continue
		}
		history := make([]buildFailureRecord, 0, len(builds)-i)
		for _, b := range builds[i:] {
			history = append(history, buildFailureRecord{Status: b.Status, FailReason: b.FailReason, ErrorMessage: b.ErrorMessage})
		}
		builds[i].RepeatCount = countFailureRepeat(history)
	}
}
