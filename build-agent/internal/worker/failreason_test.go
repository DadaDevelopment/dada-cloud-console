package worker

import (
	"errors"
	"fmt"
	"testing"
)

// TestFailureMessageAndReasonNeverEmpty is the contract this file exists to
// hold: a build that reached `failed` must carry a reason code. The column
// spent its first two weeks NULL on every path except "Jenkins returned
// FAILURE", and the console has no way to say anything useful about a code it
// never received -- it printed the raw Go error at the owner instead.
func TestFailureMessageAndReasonNeverEmpty(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"jenkins unreachable", fmt.Errorf("trigger jenkins build: %w", errors.New("status 503")), buildFailPlatformError},
		{"queue item never resolved", errors.New("resolve build number: gave up after 40 tries"), buildFailPlatformError},
		{"console stream dropped", fmt.Errorf("stream console: %w", errors.New("unexpected EOF")), buildFailPlatformError},
		{"build timed out", errors.New("poll build: context deadline exceeded"), buildFailPlatformError},
		{"nexus never confirmed the push", errors.New("image ghcr.io/x/y:tag not found in nexus"), buildFailPlatformError},
		{"repo row unreadable", fmt.Errorf("load repo: %w", errors.New("no rows in result set")), buildFailPlatformError},
		{"success bookkeeping failed", fmt.Errorf("finalize success: %w", errors.New("conn busy")), buildFailPlatformError},
		{"phase transition failed", fmt.Errorf("transition detecting→building: %w", errors.New("conn busy")), buildFailPlatformError},
		{"github app uninstalled", errors.New("installation 3500292 revoked and no live installation for owner \"acme\""), buildFailGitAuth},
		{"gitlab token gone", errors.New("gitlab repo missing token"), buildFailGitAuth},
		{"clone refused", fmt.Errorf("git creds: %w", errors.New("fatal: could not read Username for 'https://github.com'")), buildFailGitAuth},
		{"nothing recognized", errors.New("build produced no image or artifact markers"), buildFailGeneric},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, reason := failureMessageAndReason(tc.err)
			if reason == "" {
				t.Fatalf("reason is empty for %v -- the console falls back to raw error text", tc.err)
			}
			if reason != tc.want {
				t.Fatalf("reason = %q, want %q (err: %v)", reason, tc.want, tc.err)
			}
			if msg == "" {
				t.Fatalf("message is empty for %v", tc.err)
			}
		})
	}
}

// TestFailureMessageAndReasonKeepsConsoleClassification guards the precedence
// the text matching must not steal: when the Jenkins console already named a
// cause, that verdict wins over any signature found in the wrapped error.
func TestFailureMessageAndReasonKeepsConsoleClassification(t *testing.T) {
	classified := &classifiedFailure{
		code:   buildFailNoDockerfile,
		detail: "ERROR: framework 'dockerfile' has no template and repo ships no Dockerfile\nsecond line",
		err:    errors.New("jenkins build #12 result FAILURE"),
	}
	msg, reason := failureMessageAndReason(fmt.Errorf("attach: %w", classified))
	if reason != buildFailNoDockerfile {
		t.Fatalf("reason = %q, want %q", reason, buildFailNoDockerfile)
	}
	if want := buildFailNoDockerfile + ": ERROR: framework 'dockerfile' has no template and repo ships no Dockerfile"; msg != want {
		t.Fatalf("msg = %q, want %q", msg, want)
	}
}

// TestFailureMessageAndReasonNilCause pins the one case that legitimately has
// no reason: nothing failed, so nothing is written.
func TestFailureMessageAndReasonNilCause(t *testing.T) {
	msg, reason := failureMessageAndReason(nil)
	if msg != "" || reason != "" {
		t.Fatalf("nil cause produced msg=%q reason=%q, want both empty", msg, reason)
	}
}
