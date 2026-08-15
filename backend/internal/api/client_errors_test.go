package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func postClientErrorWithHandler(h *Handler, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/client-errors", h.ReportClientError)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/client-errors", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "test-agent/1.0")
	r.ServeHTTP(rec, req)
	return rec
}

func postClientError(body string) *httptest.ResponseRecorder {
	return postClientErrorWithHandler(&Handler{}, body)
}

func TestReportClientError(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"valid", `{"message":"boom","stack":"at x","url":"https://console/x","kind":"react"}`},
		{"empty message", `{"message":"","stack":"x"}`},
		{"malformed json", `{not json`},
		{"missing kind defaults", `{"message":"hooks.map is not a function"}`},
		{"oversized message capped", `{"message":"` + strings.Repeat("A", 5000) + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postClientError(tc.body)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("code=%d want 204 (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// waitForErrorShownRow polls ux_events for the row recordErrorShownEvent
// writes in a detached goroutine after ReportClientError already answered
// 204. The insert races the test, so this bounds that race instead of
// sleeping a fixed guess.
func waitForErrorShownRow(t *testing.T, pool *pgxpool.Pool, message string) (path, target string, props map[string]string, found bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var propsRaw []byte
		err := pool.QueryRow(context.Background(),
			`SELECT path, target, props FROM ux_events
			 WHERE event_type = 'error_shown' AND props->>'message' = $1
			 ORDER BY received_at DESC LIMIT 1`,
			message,
		).Scan(&path, &target, &propsRaw)
		if err == nil {
			if uerr := json.Unmarshal(propsRaw, &props); uerr != nil {
				t.Fatalf("unmarshal props: %v", uerr)
			}
			return path, target, props, true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return "", "", nil, false
}

func countErrorShownRows(t *testing.T, pool *pgxpool.Pool, message string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ux_events WHERE event_type = 'error_shown' AND props->>'message' = $1`,
		message,
	).Scan(&n); err != nil {
		t.Fatalf("count error_shown rows: %v", err)
	}
	return n
}

// TestReportClientErrorPersistsUxEvent is the test that can actually fail:
// ReportClientError always answers 204 regardless of what it persisted, so
// asserting on the status code alone (as TestReportClientError above does)
// proves nothing about the new behaviour. This asserts on the row itself.
func TestReportClientErrorPersistsUxEvent(t *testing.T) {
	pool := testAuditPool(t)
	h := &Handler{pool: pool}

	message := "boom-" + uuidSuffix(t)
	t.Cleanup(func() { deleteErrorShownRows(t, pool, message) })
	body := `{"message":"` + message + `","stack":"at x (secret.js:42)","url":"https://console.dada-tuda.ru/apps/123?token=shh#frag","kind":"window"}`

	rec := postClientErrorWithHandler(h, body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d want 204 (body=%s)", rec.Code, rec.Body.String())
	}

	path, target, props, found := waitForErrorShownRow(t, pool, message)
	if !found {
		t.Fatal("expected exactly one error_shown row to appear, found none")
	}
	if target != "window" {
		t.Fatalf("target=%q want %q (the report's kind)", target, "window")
	}
	if path != "/apps/123" {
		t.Fatalf("path=%q want %q (pathname only, no query)", path, "/apps/123")
	}
	if props["message"] != message {
		t.Fatalf("props.message=%q want %q", props["message"], message)
	}
	if props["ua"] != "test-agent/1.0" {
		t.Fatalf("props.ua=%q want %q", props["ua"], "test-agent/1.0")
	}
	if _, hasStack := props["stack"]; hasStack {
		t.Fatal("props must never carry the stack trace -- that stays in the log line only")
	}
	if strings.Contains(props["url"], "token=shh") || strings.Contains(path, "token=shh") {
		t.Fatalf("query string leaked into stored data: path=%q props.url=%q", path, props["url"])
	}
	if strings.Contains(props["url"], "#frag") {
		t.Fatalf("fragment leaked into props.url: %q", props["url"])
	}

	if got := countErrorShownRows(t, pool, message); got != 1 {
		t.Fatalf("expected exactly 1 row for this message, got %d", got)
	}
}

// TestReportClientErrorQueryStringNeverReachesStorage covers the same query
// leak concern with a plainer URL, matching the required case: "the query
// string in url does not reach path or props".
func TestReportClientErrorQueryStringNeverReachesStorage(t *testing.T) {
	pool := testAuditPool(t)
	h := &Handler{pool: pool}

	message := "qs-leak-" + uuidSuffix(t)
	t.Cleanup(func() { deleteErrorShownRows(t, pool, message) })
	body := `{"message":"` + message + `","url":"https://console.dada-tuda.ru/settings?apikey=verysecret","kind":"react"}`

	rec := postClientErrorWithHandler(h, body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d want 204", rec.Code)
	}

	path, _, props, found := waitForErrorShownRow(t, pool, message)
	if !found {
		t.Fatal("expected the row to be written")
	}
	if path != "/settings" {
		t.Fatalf("path=%q want %q", path, "/settings")
	}
	if props["url"] != "https://console.dada-tuda.ru/settings" {
		t.Fatalf("props.url=%q must have its query string stripped", props["url"])
	}
}

// TestReportClientErrorEmptyMessageWritesNoRow covers the required negative
// case: an empty message must still return 204 (unchanged behaviour) but
// must NOT produce an error_shown row.
func TestReportClientErrorEmptyMessageWritesNoRow(t *testing.T) {
	pool := testAuditPool(t)
	h := &Handler{pool: pool}

	marker := "empty-msg-marker-" + uuidSuffix(t)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM ux_events WHERE event_type = 'error_shown' AND props->>'stack' = $1`, marker,
		); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	body := `{"message":"","stack":"` + marker + `","url":"https://console/x"}`

	rec := postClientErrorWithHandler(h, body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d want 204", rec.Code)
	}

	time.Sleep(150 * time.Millisecond)
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ux_events WHERE event_type = 'error_shown' AND props->>'stack' = $1`,
		marker,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("an empty message must not write a row, got %d", n)
	}
}

// deleteErrorShownRows removes the row(s) this test wrote. Jenkins runs these
// real-DB tests against the same database that serves prod (see
// reference-jenkins-runs-real-db-tests), so an untidied insert here is not a
// throwaway -- it is a permanent stray row in the production ux_events table.
func deleteErrorShownRows(t *testing.T, pool *pgxpool.Pool, message string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM ux_events WHERE event_type = 'error_shown' AND props->>'message' = $1`, message,
	); err != nil {
		t.Logf("cleanup: %v", err)
	}
}

func uuidSuffix(t *testing.T) string {
	t.Helper()
	return time.Now().UTC().Format("20060102T150405.000000000")
}

func TestClampLen(t *testing.T) {
	if got := clampLen("abcdef", 3); got != "abc" {
		t.Fatalf("clampLen=%q want abc", got)
	}
	if got := clampLen("ab", 10); got != "ab" {
		t.Fatalf("clampLen=%q want ab", got)
	}
}
