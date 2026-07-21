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
	got := formatBuildFailureSummary("main", "abcdef1234567890", &msg, &ts)
	want := "Build failed on branch main (commit abcdef123456): npm ci failed [failed at 2026-01-02T03:04:05Z]"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatBuildFailureSummary_NoMessageNoTimestamp(t *testing.T) {
	got := formatBuildFailureSummary("main", "short", nil, nil)
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
