package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"jenkins aborted", errBuildAborted, true},
		{"wrapped aborted", fmt.Errorf("bridge: %w", errBuildAborted), true},
		{"ingress 503", errors.New(`github contents: status 503`), true},
		{"resolve build number 503", errors.New("resolve build number: queue item 6201: 503 Service Temporarily Unavailable"), true},
		{"context deadline", errors.New("poll build: context deadline exceeded"), true},
		{"connection refused", errors.New("dial tcp: connection refused"), true},
		{"real build failure", errors.New("jenkins build #27 result FAILURE"), false},
		{"context canceled not retryable here", context.Canceled, false},
		{"nexus confirm mismatch", errors.New("image not found in nexus"), false},
		{"watch budget expired with number", &watchBudgetExpired{number: 319, err: context.DeadlineExceeded}, false},
		{"watch budget expired no number", &watchBudgetExpired{number: 0, err: context.DeadlineExceeded}, false},
		{"wrapped watch budget expired", fmt.Errorf("execute: %w", &watchBudgetExpired{number: 320, err: context.DeadlineExceeded}), false},
		{"bare poll deadline still retryable", errors.New("poll build: context deadline exceeded"), true},
	}
	for _, tc := range cases {
		if got := isRetryable(tc.err); got != tc.want {
			t.Errorf("%s: isRetryable(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

func TestWatchBudgetExpiredError(t *testing.T) {
	withNumber := &watchBudgetExpired{number: 319, err: context.DeadlineExceeded}
	if !strings.Contains(withNumber.Error(), "#319") {
		t.Errorf("Error() = %q, want it to name jenkins build #319", withNumber.Error())
	}
	if !errors.Is(withNumber, context.DeadlineExceeded) {
		t.Error("errors.Is(withNumber, context.DeadlineExceeded) = false, want true (Unwrap must expose the cause)")
	}

	noNumber := &watchBudgetExpired{number: 0, err: context.DeadlineExceeded}
	if strings.Contains(noNumber.Error(), "#0") {
		t.Errorf("Error() = %q, should not print a fake build number when none resolved", noNumber.Error())
	}
}

func TestIsWatchBudgetExceeded(t *testing.T) {
	t.Run("budget expired, parent alive", func(t *testing.T) {
		parent := context.Background()
		budget, cancel := context.WithTimeout(parent, time.Millisecond)
		defer cancel()
		<-budget.Done()
		err := budget.Err()
		if !isWatchBudgetExceeded(err, budget, parent) {
			t.Error("want true: budget context deadline exceeded while parent is still alive")
		}
	})

	t.Run("parent also canceled", func(t *testing.T) {
		parentCtx, parentCancel := context.WithCancel(context.Background())
		budget, cancel := context.WithTimeout(parentCtx, time.Millisecond)
		defer cancel()
		<-budget.Done()
		parentCancel()
		err := budget.Err()
		if isWatchBudgetExceeded(err, budget, parentCtx) {
			t.Error("want false: parent is done too, this is a shutdown/cancel, not a watch-budget expiry")
		}
	})

	t.Run("error is not a deadline", func(t *testing.T) {
		parent := context.Background()
		budget, cancel := context.WithTimeout(parent, time.Minute)
		defer cancel()
		if isWatchBudgetExceeded(errors.New("connection refused"), budget, parent) {
			t.Error("want false: err does not carry context.DeadlineExceeded")
		}
	})

	t.Run("nil error", func(t *testing.T) {
		parent := context.Background()
		budget, cancel := context.WithTimeout(parent, time.Minute)
		defer cancel()
		if isWatchBudgetExceeded(nil, budget, parent) {
			t.Error("want false: nil error")
		}
	})
}

func TestIsJenkinsVerdict(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"classified failure", &classifiedFailure{code: buildFailDockerfileBuild, detail: "step 4 failed", err: errors.New("jenkins build #7 result FAILURE")}, true},
		{"wrapped classified failure", fmt.Errorf("attach: %w", &classifiedFailure{code: buildFailDockerfileBuild, err: errors.New("boom")}), true},
		{"aborted", errBuildAborted, true},
		{"transport deadline", context.DeadlineExceeded, false},
		{"transport refused", errors.New("dial tcp: connection refused"), false},
	}
	for _, tc := range cases {
		if got := isJenkinsVerdict(tc.err); got != tc.want {
			t.Errorf("%s: isJenkinsVerdict(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

func TestIsPlatformFailureWatchBudget(t *testing.T) {
	err := &watchBudgetExpired{number: 319, err: context.DeadlineExceeded}
	if !isPlatformFailure(err) {
		t.Error("watchBudgetExpired must classify as a platform failure, not a user-code failure")
	}
	msg, reason := failureMessageAndReason(err)
	if reason != buildFailPlatformError {
		t.Errorf("failureMessageAndReason reason = %q, want %q", reason, buildFailPlatformError)
	}
	if !strings.Contains(msg, "#319") {
		t.Errorf("failureMessageAndReason msg = %q, want it to name jenkins build #319", msg)
	}
}
