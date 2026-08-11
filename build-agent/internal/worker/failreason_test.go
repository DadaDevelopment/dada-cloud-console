package worker

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
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

// TestFailureReasonCutIsRuneSafe: the reason is lifted from a build log, which
// is routinely Russian. A cut at a byte offset lands inside a multi-byte rune
// and ships a broken sequence into the failure email.
func TestFailureReasonCutIsRuneSafe(t *testing.T) {
	long := strings.Repeat("ошибка ", 200)
	got := failureReason(errors.New(long))
	if !utf8.ValidString(got) {
		t.Errorf("failureReason produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("long reason must be marked as truncated: %q", got)
	}
	if n := utf8.RuneCountInString(strings.TrimSuffix(got, "…")); n != failureReasonMaxLen {
		t.Errorf("truncated to %d runes, want %d", n, failureReasonMaxLen)
	}
}

// TestFailureReasonKeepsShortTextIntact guards the common case: a one-line
// cause under the cap must reach the reader unchanged, with no ellipsis.
func TestFailureReasonKeepsShortTextIntact(t *testing.T) {
	if got := failureReason(errors.New("npm ci exited 1\nnpm ERR! code 1")); got != "npm ci exited 1" {
		t.Errorf("got %q, want first line only", got)
	}
}
