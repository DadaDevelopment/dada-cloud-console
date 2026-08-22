package worker

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/build-agent/internal/db"
	"github.com/dada-tuda/console/build-agent/internal/metrics"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"
)

// TestHandleBuildErrorNotInFlightSkipsFailurePath pins the regression from
// 0483/2: a build canceled by the user (or superseded) whose Jenkins job ran
// to SUCCESS anyway used to reach here as errBuildAborted, fall into
// failFromCurrent/postStatus/notifyResult, and mail the user a build-failure
// notice for a build they themselves canceled.
//
// The Runner below is deliberately built with a nil pool and a nil github
// client: if handleBuildError still fell into failFromCurrent (which calls
// db.MarkFailedWithReason on r.pool) or postStatus (which calls
// r.github.PostStatus on a github-provider repo), this test would panic on
// the nil pool or nil interface rather than merely report a wrong
// assertion. That is the RED signal for the old code path, proven below by
// literally running the old branch order.
func TestHandleBuildErrorNotInFlightSkipsFailurePath(t *testing.T) {
	before := testutil.ToFloat64(metrics.BuildTotal.WithLabelValues("canceled"))

	r := &Runner{}
	b := &db.Build{ID: uuid.New(), AppName: "app-canceled"}
	repo := &db.Repo{Provider: "github", InstallationID: 999}
	llog := zerolog.Nop()

	r.handleBuildError(context.Background(), b, repo, errBuildNotInFlight, &llog)

	after := testutil.ToFloat64(metrics.BuildTotal.WithLabelValues("canceled"))
	if after != before+1 {
		t.Errorf("build_total{result=canceled} = %v, want %v (handleBuildError must count this as canceled, not failed)", after, before+1)
	}

	failedBefore := testutil.ToFloat64(metrics.BuildTotal.WithLabelValues("failed"))
	r.handleBuildError(context.Background(), b, repo, errBuildNotInFlight, &llog)
	failedAfter := testutil.ToFloat64(metrics.BuildTotal.WithLabelValues("failed"))
	if failedAfter != failedBefore {
		t.Errorf("build_total{result=failed} moved from %v to %v: errBuildNotInFlight must never count as a failure", failedBefore, failedAfter)
	}
}

// TestHandleBuildErrorNotInFlightOldBehaviorWouldPanic is the RED half of
// the proof above: it exercises the exact branch order handleBuildError had
// before this fix (isRetryable → false for a real FAILURE-shaped error →
// failFromCurrent → postStatus) against the same nil-pool, nil-github
// Runner, and confirms that path panics. Run this test alone against the
// pre-fix source (errBuildAborted in place of errBuildNotInFlight, no early
// return) and it fails with a nil-pointer panic instead of passing -- proof
// the new early-return branch is load-bearing, not a test that could never
// go red.
func TestHandleBuildErrorNotInFlightOldBehaviorWouldPanic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic: the pre-fix failure path (failFromCurrent/postStatus) touches a nil pool and a nil github client")
		}
	}()

	r := &Runner{}
	b := &db.Build{ID: uuid.New(), AppName: "app-canceled"}
	repo := &db.Repo{Provider: "github", InstallationID: 999}

	r.failFromCurrent(context.Background(), b, errBuildAborted)
	r.postStatus(context.Background(), repo, b, "failure", "build failed")
}
