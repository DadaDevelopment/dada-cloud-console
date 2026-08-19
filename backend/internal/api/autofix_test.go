package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dada-tuda/console/backend/internal/logsearch"
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

// TestSearchAutofixLogsUsesErrorLevelWhenPresent pins the happy path: a
// structured-log app that actually has ERROR-level hits in the recent window
// must not fall through to the noisier unfiltered tiers, and the level
// filter must be present on the wire.
func TestSearchAutofixLogsUsesErrorLevelWhenPresent(t *testing.T) {
	callCount := 0
	var sawLevelFilter bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode stub ES request: %v", err)
		}
		body, _ := json.Marshal(reqBody)
		if strings.Contains(string(body), `"app.level.keyword":"ERROR"`) {
			sawLevelFilter = true
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"hits":{"total":{"value":1},"hits":[{"_source":{"@timestamp":"2026-08-19T10:00:00Z","message":"structured ERROR entry"}}]}}`)
	}))
	defer srv.Close()

	h := &Handler{infraLogsearch: logsearch.New(srv.URL, "", "test-index")}
	entries, err := h.searchAutofixLogs(context.Background(), []string{"ns-1"}, "my-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected exactly 1 search call (ERROR level hit on first try), got %d", callCount)
	}
	if !sawLevelFilter {
		t.Fatalf("expected the first search to carry the ERROR level filter")
	}
	if len(entries) != 1 || entries[0].Message != "structured ERROR entry" {
		t.Fatalf("expected the ERROR-level entry to be returned, got %+v", entries)
	}
}

// TestSearchAutofixLogsFallsBackWhenLevelFieldMissing is the regression test
// for the crash-loop case: an uncaught Python/Node exception prints to stdout
// with no level field at all, so the ERROR-level term query never matches
// (see logsearch.buildQuery / autofix.go fetchAutofixLogs doc comment). The
// second attempt, same window but no level filter, must find it.
func TestSearchAutofixLogsFallsBackWhenLevelFieldMissing(t *testing.T) {
	callCount := 0
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode stub ES request: %v", err)
		}
		body, _ := json.Marshal(reqBody)
		bodies = append(bodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			fmt.Fprint(w, `{"hits":{"total":{"value":0},"hits":[]}}`)
			return
		}
		fmt.Fprint(w, `{"hits":{"total":{"value":1},"hits":[{"_source":{"@timestamp":"2026-08-19T09:00:00Z","message":"ModuleNotFoundError: No module named 'app'"}}]}}`)
	}))
	defer srv.Close()

	h := &Handler{infraLogsearch: logsearch.New(srv.URL, "", "test-index")}
	entries, err := h.searchAutofixLogs(context.Background(), []string{"ns-1"}, "gulyaev-ai-core")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected exactly 2 search calls (ERROR level empty, then unfiltered recent window hit), got %d", callCount)
	}
	if strings.Contains(bodies[0], `"app.level.keyword":"ERROR"`) == false {
		t.Fatalf("expected first attempt to carry the ERROR level filter, body: %s", bodies[0])
	}
	if strings.Contains(bodies[1], `"app.level.keyword"`) {
		t.Fatalf("expected second attempt to drop the level filter entirely, body: %s", bodies[1])
	}
	if len(entries) != 1 || entries[0].Message != "ModuleNotFoundError: No module named 'app'" {
		t.Fatalf("expected the unfiltered recent-window entry to be returned, got %+v", entries)
	}
}

// TestSearchAutofixLogsFallsBackToWideWindowWhenRecentIsEmpty covers the
// third tier: both recent-window attempts (filtered and unfiltered) come back
// empty, so the wide unfiltered fallback window must be tried.
func TestSearchAutofixLogsFallsBackToWideWindowWhenRecentIsEmpty(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount < 3 {
			fmt.Fprint(w, `{"hits":{"total":{"value":0},"hits":[]}}`)
			return
		}
		fmt.Fprint(w, `{"hits":{"total":{"value":1},"hits":[{"_source":{"@timestamp":"2026-08-18T09:00:00Z","message":"crash from yesterday"}}]}}`)
	}))
	defer srv.Close()

	h := &Handler{infraLogsearch: logsearch.New(srv.URL, "", "test-index")}
	entries, err := h.searchAutofixLogs(context.Background(), []string{"ns-1"}, "my-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected exactly 3 search calls (ERROR filtered, unfiltered recent, unfiltered fallback), got %d", callCount)
	}
	if len(entries) != 1 || entries[0].Message != "crash from yesterday" {
		t.Fatalf("expected the fallback-window entry to be returned, got %+v", entries)
	}
}

// TestSearchAutofixLogsAllTiersEmpty covers the case where every tier legitimately
// has nothing: it must return zero entries and no error, not synthesize a result.
func TestSearchAutofixLogsAllTiersEmpty(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"hits":{"total":{"value":0},"hits":[]}}`)
	}))
	defer srv.Close()

	h := &Handler{infraLogsearch: logsearch.New(srv.URL, "", "test-index")}
	entries, err := h.searchAutofixLogs(context.Background(), []string{"ns-1"}, "my-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected all 3 tiers to be tried, got %d calls", callCount)
	}
	if len(entries) != 0 {
		t.Fatalf("expected zero entries when every tier is empty, got %+v", entries)
	}
}

// TestSearchAutofixLogsSearchErrorIsReported covers the "any failure returns
// an error" branch: a broken search client must surface an error naming which
// attempt failed, not silently return empty results (fetchAutofixLogs is the
// one that turns that error into "" for the caller -- this pins the error
// itself is not swallowed one layer too early).
func TestSearchAutofixLogsSearchErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"boom"}`)
	}))
	defer srv.Close()

	h := &Handler{infraLogsearch: logsearch.New(srv.URL, "", "test-index")}
	entries, err := h.searchAutofixLogs(context.Background(), []string{"ns-1"}, "my-api")
	if err == nil {
		t.Fatalf("expected an error from a failing search client, got entries=%+v", entries)
	}
	if !strings.Contains(err.Error(), "recent window") {
		t.Fatalf("expected the error to name which attempt failed, got %q", err.Error())
	}
	if entries != nil {
		t.Fatalf("expected nil entries alongside the error, got %+v", entries)
	}
}

// TestFetchAutofixLogsNoInfraClientReturnsEmpty pins the existing best-effort
// contract at the outer fetchAutofixLogs layer: with no infra logsearch client
// configured, it must return "" without touching the (nil) DB pool.
func TestFetchAutofixLogsNoInfraClientReturnsEmpty(t *testing.T) {
	h := &Handler{}
	if got := h.fetchAutofixLogs(context.Background(), uuid.Nil, uuid.Nil, "my-api"); got != "" {
		t.Fatalf("expected empty string with no infra logsearch client, got %q", got)
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
