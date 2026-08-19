package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestClassifyAutofixResolution_AppliedVsNoChangeVsFailed pins the three-way
// split TriggerAutofix's launch audit could not make: a run only counts as
// applied when it actually opened a pull request. A run that reports
// "completed" with no PR produced nothing a human can act on and must not
// collapse into the same success as a run that did -- that collapse is the
// exact shape of the lifecoachrussia@yandex.ru prod incident, where
// TriggerAutofix's launch-time audit read as success while the app stayed
// crash-looping with no patch ever applied.
func TestClassifyAutofixResolution_AppliedVsNoChangeVsFailed(t *testing.T) {
	cases := []struct {
		name        string
		tr          cloudTaskTransition
		wantOutcome string
		wantVerdict autofixResolutionVerdict
	}{
		{
			name:        "completed with a PR is applied",
			tr:          cloudTaskTransition{NewStatus: "completed", PRURL: "https://github.com/o/r/pull/1"},
			wantOutcome: auditOutcomeSuccess,
			wantVerdict: autofixVerdictApplied,
		},
		{
			name:        "completed with no PR is no_change, not success",
			tr:          cloudTaskTransition{NewStatus: "completed"},
			wantOutcome: auditOutcomeFailure,
			wantVerdict: autofixVerdictNoChange,
		},
		{
			name:        "failed run is failed",
			tr:          cloudTaskTransition{NewStatus: "failed", Error: "install token expired"},
			wantOutcome: auditOutcomeFailure,
			wantVerdict: autofixVerdictFailed,
		},
		{
			name:        "canceled run is failed",
			tr:          cloudTaskTransition{NewStatus: "canceled"},
			wantOutcome: auditOutcomeFailure,
			wantVerdict: autofixVerdictFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, verdict := classifyAutofixResolution(tc.tr)
			if outcome != tc.wantOutcome {
				t.Errorf("outcome = %q, want %q", outcome, tc.wantOutcome)
			}
			if verdict != tc.wantVerdict {
				t.Errorf("verdict = %q, want %q", verdict, tc.wantVerdict)
			}
		})
	}
}

// TestRecordAutofixResolution_NoChangeIsQueryableSeparatelyFromApplied is the
// real-DB regression test for the prod incident: a completed run that opened
// no pull request must land in audit_events as its own no_change verdict, not
// under the same success outcome TriggerAutofix's launch audit uses, so an
// admin filtering "successful auto-fixes" cannot mistake one for the other.
func TestRecordAutofixResolution_NoChangeIsQueryableSeparatelyFromApplied(t *testing.T) {
	pool := autofixGuardPool(t)
	projectID, envID, actorID := seedAutofixTarget(t, pool)
	h := &Handler{pool: pool}
	ctx := context.Background()

	tr := cloudTaskTransition{
		Matched:       true,
		ID:            uuid.New(),
		ProjectID:     projectID,
		EnvironmentID: envID,
		AppName:       "fonbet-value",
		TaskType:      "autofix",
		ActorID:       actorID,
		OldStatus:     "running",
		NewStatus:     "completed",
	}
	h.recordAutofixResolution(ctx, tr)

	var outcome, verdict string
	err := pool.QueryRow(ctx,
		`SELECT outcome, metadata->>'verdict' FROM audit_events
		  WHERE project_id=$1 AND action=$2 AND resource_name=$3
		  ORDER BY created_at DESC LIMIT 1`,
		projectID, auditActionResolveAutofix, "fonbet-value").Scan(&outcome, &verdict)
	if err != nil {
		t.Fatalf("read back ResolveAutofix row: %v", err)
	}
	if outcome != auditOutcomeFailure {
		t.Errorf("outcome = %q, want %q -- a run with no PR must not read as success", outcome, auditOutcomeFailure)
	}
	if verdict != string(autofixVerdictNoChange) {
		t.Errorf("verdict = %q, want %q", verdict, autofixVerdictNoChange)
	}
}
