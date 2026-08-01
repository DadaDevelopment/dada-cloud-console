package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/llmchat"
	"github.com/dada-tuda/console/backend/internal/logsearch"
)

func TestBuildLogLines(t *testing.T) {
	cases := []struct {
		name    string
		entries []logsearch.LogEntry
		want    []string
	}{
		{
			name:    "empty",
			entries: nil,
			want:    nil,
		},
		{
			name: "reverses newest-first search order into chronological order",
			entries: []logsearch.LogEntry{
				{Timestamp: "2026-08-01T10:00:02Z", Message: "panic: boom"},
				{Timestamp: "2026-08-01T10:00:01Z", Message: "starting worker"},
				{Timestamp: "2026-08-01T10:00:00Z", Message: "boot"},
			},
			want: []string{
				"2026-08-01T10:00:00Z boot",
				"2026-08-01T10:00:01Z starting worker",
				"2026-08-01T10:00:02Z panic: boom",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildLogLines(tc.entries)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("buildLogLines() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestCollapseRepeatedBlocks(t *testing.T) {
	t.Run("5 identical 4-line traceback cycles collapse to one block plus marker", func(t *testing.T) {
		cycleMessages := []string{
			"panic: nil pointer dereference",
			"goroutine 1 [running]:",
			"main.run()",
			"/app/main.go:42 +0x1a",
		}
		var lines []string
		ts := 0
		for rep := 0; rep < 5; rep++ {
			for _, msg := range cycleMessages {
				lines = append(lines, fmt.Sprintf("2026-08-01T10:00:%02dZ %s", ts, msg))
				ts++
			}
		}
		got := collapseRepeatedBlocks(lines)
		if len(got) != 5 {
			t.Fatalf("expected 4 cycle lines + 1 marker = 5 lines, got %d: %#v", len(got), got)
		}
		marker := got[4]
		if !strings.Contains(marker, "ещё 4 раз") || !strings.Contains(marker, "крашлуп") {
			t.Fatalf("expected marker to say 'ещё 4 раз' and mention крашлуп, got %q", marker)
		}
		if !strings.Contains(marker, "4 строк") {
			t.Fatalf("expected marker to report a 4-line period, got %q", marker)
		}
	})

	t.Run("non-repeating logs are left completely unchanged", func(t *testing.T) {
		lines := []string{
			"2026-08-01T10:00:00Z boot",
			"2026-08-01T10:00:01Z connecting to db",
			"2026-08-01T10:00:02Z listening on :8080",
			"2026-08-01T10:00:03Z request GET /health",
		}
		got := collapseRepeatedBlocks(lines)
		if !reflect.DeepEqual(got, lines) {
			t.Fatalf("collapseRepeatedBlocks() = %#v, want unchanged %#v", got, lines)
		}
	})

	t.Run("a trailing partial cycle is not lost", func(t *testing.T) {
		lines := []string{
			"2026-08-01T10:00:00Z panic: boom",
			"2026-08-01T10:00:01Z stack trace line",
			"2026-08-01T10:00:02Z panic: boom",
			"2026-08-01T10:00:03Z stack trace line",
			"2026-08-01T10:00:04Z panic: boom",
		}
		got := collapseRepeatedBlocks(lines)
		last := got[len(got)-1]
		if !strings.Contains(last, "panic: boom") {
			t.Fatalf("expected the trailing partial repeat to survive in the output, got %#v", got)
		}
		joined := strings.Join(got, "\n")
		if !strings.Contains(joined, "крашлуп") {
			t.Fatalf("expected the full pair to still be collapsed with a marker, got %#v", got)
		}
	})

	t.Run("excerpt built from collapsed text contains the marker", func(t *testing.T) {
		cycle := []string{"error: connection refused", "retrying in 5s"}
		var lines []string
		for rep := 0; rep < 6; rep++ {
			for j, msg := range cycle {
				lines = append(lines, fmt.Sprintf("2026-08-01T10:%02d:%02dZ %s", rep, j, msg))
			}
		}
		collapsed := collapseRepeatedBlocks(lines)
		excerpt := lastLines(collapsed, diagnoseExcerptLines)
		found := false
		for _, l := range excerpt {
			if strings.Contains(l, "крашлуп") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected the user-facing excerpt to contain the collapse marker, got %#v", excerpt)
		}
	})

	t.Run("a single repeating line (period 1) also collapses", func(t *testing.T) {
		lines := []string{
			"2026-08-01T10:00:00Z connection refused, retrying",
			"2026-08-01T10:00:01Z connection refused, retrying",
			"2026-08-01T10:00:02Z connection refused, retrying",
			"2026-08-01T10:00:03Z connection refused, retrying",
		}
		got := collapseRepeatedBlocks(lines)
		if len(got) != 2 {
			t.Fatalf("expected 1 line + 1 marker = 2 lines, got %d: %#v", len(got), got)
		}
		if !strings.Contains(got[1], "ещё 3 раз") || !strings.Contains(got[1], "1 строк") {
			t.Fatalf("expected marker for a 1-line period repeated 4 times total, got %q", got[1])
		}
	})
}

func TestTruncateLogLines(t *testing.T) {
	lines := []string{"aaaaa", "bbbbb", "ccccc", "ddddd"}
	cases := []struct {
		name     string
		lines    []string
		maxChars int
		want     []string
	}{
		{
			name:     "empty input",
			lines:    nil,
			maxChars: 100,
			want:     nil,
		},
		{
			name:     "budget covers everything",
			lines:    lines,
			maxChars: 1000,
			want:     lines,
		},
		{
			name:     "budget keeps only the most recent (tail) lines",
			lines:    lines,
			maxChars: 12,
			want:     []string{"ccccc", "ddddd"},
		},
		{
			name:     "budget too small for even the last line still keeps it",
			lines:    lines,
			maxChars: 1,
			want:     []string{"ddddd"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateLogLines(tc.lines, tc.maxChars)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("truncateLogLines(%v, %d) = %#v, want %#v", tc.lines, tc.maxChars, got, tc.want)
			}
		})
	}
}

func TestLastLines(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	cases := []struct {
		name string
		n    int
		want []string
	}{
		{name: "n larger than input returns a copy of everything", n: 10, want: []string{"a", "b", "c", "d", "e"}},
		{name: "n smaller than input keeps only the tail", n: 2, want: []string{"d", "e"}},
		{name: "n zero returns empty", n: 0, want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lastLines(lines, tc.n)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lastLines(%v, %d) = %#v, want %#v", lines, tc.n, got, tc.want)
			}
		})
	}
}

func TestBuildDiagnoseMessages(t *testing.T) {
	cases := []struct {
		name          string
		appName       string
		reason        string
		logText       string
		wantSubstring []string
		noSubstring   []string
	}{
		{
			name:          "with a known health-alert reason",
			appName:       "my-api",
			reason:        "CrashLoopBackOff",
			logText:       "panic: nil pointer",
			wantSubstring: []string{"my-api", "CrashLoopBackOff", "panic: nil pointer"},
		},
		{
			name:          "without a reason, no reason line is added",
			appName:       "my-api",
			reason:        "",
			logText:       "boot ok",
			wantSubstring: []string{"my-api", "boot ok"},
			noSubstring:   []string{"Причина сбоя"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messages := buildDiagnoseMessages(tc.appName, tc.reason, tc.logText)
			if len(messages) != 2 {
				t.Fatalf("expected exactly 2 messages (system+user), got %d", len(messages))
			}
			if messages[0].Role != "system" || !strings.Contains(messages[0].Content, "логах") {
				t.Fatalf("system message missing grounding instruction: %+v", messages[0])
			}
			if messages[1].Role != "user" {
				t.Fatalf("second message must be role=user, got %q", messages[1].Role)
			}
			for _, s := range tc.wantSubstring {
				if !strings.Contains(messages[1].Content, s) {
					t.Fatalf("user message %q missing expected substring %q", messages[1].Content, s)
				}
			}
			for _, s := range tc.noSubstring {
				if strings.Contains(messages[1].Content, s) {
					t.Fatalf("user message %q should not contain %q when reason is empty", messages[1].Content, s)
				}
			}
		})
	}
}

func TestNoLogsDiagnosis(t *testing.T) {
	cases := []struct {
		name       string
		reason     string
		wantSubstr []string
	}{
		{
			name:       "no reason known either -- purely honest, no invented cause",
			reason:     "",
			wantSubstr: []string{"Логов не нашлось"},
		},
		{
			name:       "known reason is surfaced but labeled as platform-detected, not log-grounded",
			reason:     "OOMKilled",
			wantSubstr: []string{"Логов не нашлось", "OOMKilled"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := noLogsDiagnosis(tc.reason)
			for _, s := range tc.wantSubstr {
				if !strings.Contains(got, s) {
					t.Fatalf("noLogsDiagnosis(%q) = %q, missing expected substring %q", tc.reason, got, s)
				}
			}
		})
	}
}

// TestDiagnoseCoreNoLogsSkipsGateway verifies the "razobratsya" contract's most
// important safety property: with zero log entries, diagnoseCore must never
// call the LLM (h.agentChatLLM is left nil here -- a real call would panic)
// and must return the honest no-logs diagnosis with an empty excerpt.
func TestDiagnoseCoreNoLogsSkipsGateway(t *testing.T) {
	h := &Handler{}
	diagnosis, excerpt, err := h.diagnoseCore(context.Background(), "my-api", "CrashLoopBackOff", nil, "user-1")
	if err != nil {
		t.Fatalf("diagnoseCore returned an error on the no-logs path: %v", err)
	}
	if len(excerpt) != 0 {
		t.Fatalf("expected an empty log_excerpt on the no-logs path, got %v", excerpt)
	}
	if !strings.Contains(diagnosis, "Логов не нашлось") || !strings.Contains(diagnosis, "CrashLoopBackOff") {
		t.Fatalf("no-logs diagnosis %q did not surface the honest no-logs message with the known reason", diagnosis)
	}
}

// TestDiagnoseCoreWithLogsCallsGateway verifies the grounded path: given log
// entries, diagnoseCore calls through to a stubbed AI gateway, truncates and
// excerpts the lines actually sent, and returns the model's answer verbatim
// (trimmed).
func TestDiagnoseCoreWithLogsCallsGateway(t *testing.T) {
	var sawUserContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []llmchat.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode stub gateway request: %v", err)
		}
		for _, m := range body.Messages {
			if m.Role == "user" {
				sawUserContent = m.Content
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"причина: OOM\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	h := &Handler{agentChatLLM: llmchat.New(srv.URL, "test-key", "test-model")}
	entries := []logsearch.LogEntry{
		{Timestamp: "2026-08-01T10:00:01Z", Message: "OOMKilled: container exceeded memory limit"},
		{Timestamp: "2026-08-01T10:00:00Z", Message: "boot ok"},
	}

	diagnosis, excerpt, err := h.diagnoseCore(context.Background(), "my-api", "OOMKilled", entries, "user-1")
	if err != nil {
		t.Fatalf("diagnoseCore returned an unexpected error: %v", err)
	}
	if diagnosis != "причина: OOM" {
		t.Fatalf("diagnosis = %q, want the stubbed gateway content", diagnosis)
	}
	if len(excerpt) != 2 {
		t.Fatalf("expected 2 excerpt lines (both entries fit the budget), got %v", excerpt)
	}
	if !strings.Contains(sawUserContent, "OOMKilled: container exceeded memory limit") {
		t.Fatalf("gateway request body did not include the log line it should have been grounded in: %q", sawUserContent)
	}
}

func TestFetchDiagnoseLogsFallsBackToWiderWindow(t *testing.T) {
	var sinceValues []string
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode stub ES request: %v", err)
		}
		sinceValues = append(sinceValues, fmt.Sprintf("%v", reqBody["query"]))
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			fmt.Fprint(w, `{"hits":{"total":{"value":0},"hits":[]}}`)
			return
		}
		fmt.Fprint(w, `{"hits":{"total":{"value":1},"hits":[{"_source":{"@timestamp":"2026-07-31T10:00:00Z","message":"panic: fallback window hit","kubernetes":{"namespace_name":"ns-1"}}}]}}`)
	}))
	defer srv.Close()

	h := &Handler{infraLogsearch: logsearch.New(srv.URL, "", "test-index")}
	entries := h.fetchDiagnoseLogs(context.Background(), "ns-1", "my-api")

	if callCount != 2 {
		t.Fatalf("expected exactly 2 search calls (recent window empty, then fallback window), got %d", callCount)
	}
	if len(entries) != 1 || entries[0].Message != "panic: fallback window hit" {
		t.Fatalf("expected the fallback window's single entry to be returned, got %+v", entries)
	}
}

func TestFetchDiagnoseLogsNoHitsInEitherWindow(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"hits":{"total":{"value":0},"hits":[]}}`)
	}))
	defer srv.Close()

	h := &Handler{infraLogsearch: logsearch.New(srv.URL, "", "test-index")}
	entries := h.fetchDiagnoseLogs(context.Background(), "ns-1", "my-api")

	if callCount != 2 {
		t.Fatalf("expected both windows to be tried, got %d calls", callCount)
	}
	if len(entries) != 0 {
		t.Fatalf("expected zero entries when both windows are empty, got %+v", entries)
	}

	diagnosis, excerpt, err := h.diagnoseCore(context.Background(), "my-api", "", entries, "user-1")
	if err != nil {
		t.Fatalf("diagnoseCore returned an unexpected error: %v", err)
	}
	if len(excerpt) != 0 {
		t.Fatalf("expected empty excerpt for the full no-logs path, got %v", excerpt)
	}
	if !strings.Contains(diagnosis, "Логов не нашлось") {
		t.Fatalf("expected the honest no-logs diagnosis, got %q", diagnosis)
	}
}

func TestFetchDiagnoseLogsNoNamespaceSkipsSearch(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"hits":{"total":{"value":0},"hits":[]}}`)
	}))
	defer srv.Close()

	h := &Handler{infraLogsearch: logsearch.New(srv.URL, "", "test-index")}
	entries := h.fetchDiagnoseLogs(context.Background(), "", "my-api")
	if called {
		t.Fatalf("expected no search call for a VM app with no k8s namespace")
	}
	if entries != nil {
		t.Fatalf("expected nil entries for a VM app with no k8s namespace, got %+v", entries)
	}
}
