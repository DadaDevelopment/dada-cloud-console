package api

import (
	"bufio"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// clientReassembleTokens mirrors the browser SSE consumer in
// frontend/components/agent-chat-panel.tsx: it accumulates the "data:" lines of
// each event block (without stripping a leading space, since the gin encoder
// adds none), joins them with "\n" on the terminating blank line, and
// concatenates the token deltas. It exists to prove the wire framing
// round-trips deltas that contain newlines and significant whitespace.
func clientReassembleTokens(raw string) string {
	var sb strings.Builder
	currentEvent := "message"
	var dataLines []string
	flush := func() {
		if currentEvent == "token" {
			sb.WriteString(strings.Join(dataLines, "\n"))
		}
		currentEvent = "message"
		dataLines = nil
	}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}
	flush()
	return sb.String()
}

// TestWriteSSEEvent_TokenRoundTripsWhitespaceAndNewlines proves the standard
// gin sse framing preserves multi-line and whitespace-heavy assistant deltas
// end to end. The prior hand-rolled "data: %s" form dropped the tail of any
// delta that contained a newline and the client trim glued words together.
func TestWriteSSEEvent_TokenRoundTripsWhitespaceAndNewlines(t *testing.T) {
	deltas := []string{
		"Привет! Я помогаю", " ответить на вопросы,",
		"\n\n- развёртываниями\n", " и другими ресурсами", " ", "в проекте.",
	}
	want := strings.Join(deltas, "")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	for _, d := range deltas {
		writeSSEEvent(c, rec, "token", d)
	}

	got := clientReassembleTokens(rec.Body.String())
	if got != want {
		t.Fatalf("token round-trip mismatch:\n want %q\n  got %q", want, got)
	}
}
