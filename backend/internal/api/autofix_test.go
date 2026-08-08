package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTriggerAutofix_NoClaims_401(t *testing.T) {
	h := &Handler{}
	c, rec := newCloudTaskCtx(t, http.MethodPost, `{"error":"boom"}`,
		gin.Params{{Key: "projectId", Value: "00000000-0000-0000-0000-000000000001"},
			{Key: "envId", Value: "00000000-0000-0000-0000-000000000002"},
			{Key: "appName", Value: "web"}}, false)
	h.TriggerAutofix(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

func TestTriggerAutofix_BadProjectID_NoPanic(t *testing.T) {
	h := &Handler{}
	c, rec := newCloudTaskCtx(t, http.MethodPost, `{"error":"boom"}`,
		gin.Params{{Key: "projectId", Value: "not-a-uuid"}}, false)
	h.TriggerAutofix(c)
	if rec.Code == 0 {
		t.Fatal("handler did not write a response")
	}
}

func TestFormatBuildFailureSummary(t *testing.T) {
	msg := "npm ci failed"
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := formatBuildFailureSummary("main", "abcdef1234567890", &msg, &ts, "", nil)
	want := "Build failed on branch main (commit abcdef123456): npm ci failed [failed at 2026-01-02T03:04:05Z]"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatBuildFailureSummary_NoMessageNoTimestamp(t *testing.T) {
	got := formatBuildFailureSummary("main", "short", nil, nil, "", nil)
	want := "Build failed on branch main (commit short)"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestShortSHA(t *testing.T) {
	if got := shortSHA("abcdefghijklmnop"); got != "abcdefghijkl" {
		t.Fatalf("got %q", got)
	}
	if got := shortSHA("short"); got != "short" {
		t.Fatalf("got %q", got)
	}
}

// TestFormatBuildFailureSummary_CarriesCause pins the fact the auto-fix agent
// depends on: the summary must name why the build failed. It used to carry the
// branch, the commit and the timestamp only, so the agent was asked to fix a
// failure it was never told the cause of -- which is why the one real run in
// production had nothing to work from.
func TestFormatBuildFailureSummary_CarriesCause(t *testing.T) {
	detail := "dockerfile_build_failed: [4/7] RUN pip install -r requirements.txt: ERROR: No matching distribution found for sqlite3"
	got := formatBuildFailureSummary("main", "abcdef1234567890", nil, nil, "dockerfile_build_failed", &detail)
	want := "Build failed on branch main (commit abcdef123456)\n" +
		"Failure reason: dockerfile_build_failed\n" +
		"Cause: [4/7] RUN pip install -r requirements.txt: ERROR: No matching distribution found for sqlite3"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuildFailureCause(t *testing.T) {
	plain := "boom"
	prefixed := "build_failed: boom"
	empty := "   "
	cases := []struct {
		name    string
		reason  string
		message *string
		want    string
	}{
		{"nothing persisted", "", nil, ""},
		{"blank message is not a cause", "", &empty, ""},
		{"reason alone still names the class", "no_dockerfile", nil, "Failure reason: no_dockerfile"},
		{"message alone", "", &plain, "Cause: boom"},
		{"prefix is not repeated", "build_failed", &prefixed, "Failure reason: build_failed\nCause: boom"},
		{"unprefixed message survives", "build_failed", &plain, "Failure reason: build_failed\nCause: boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildFailureCause(tc.reason, tc.message); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
