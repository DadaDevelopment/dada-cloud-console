package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"
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
	}
	for _, tc := range cases {
		if got := isRetryable(tc.err); got != tc.want {
			t.Errorf("%s: isRetryable(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}
