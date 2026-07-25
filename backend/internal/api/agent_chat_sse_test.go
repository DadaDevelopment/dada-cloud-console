package api

import (
	"bufio"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// clientReassembleTokens mirrors the browser SSE consumer in
// frontend/components/agent-chat-panel.tsx: it splits the stream on newlines,
// tracks the current event, and JSON-decodes each token frame's data payload
// back to the exact delta before concatenating. It exists to prove the wire
// framing round-trips deltas that contain newlines and significant whitespace.
func clientReassembleTokens(raw string) string {
	var sb strings.Builder
	currentEvent := "message"
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if currentEvent == "token" {
				var s string
				if err := json.Unmarshal([]byte(data), &s); err == nil {
					sb.WriteString(s)
				}
			}
		}
	}
	return sb.String()
}

func TestWriteSSEToken_RoundTripsWhitespaceAndNewlines(t *testing.T) {
	deltas := []string{
		"Привет! Я помогаю", " ответить на вопросы,",
		"\n\n- развёртываниями\n", " и другими ресурсами", " ", "в проекте.",
	}
	want := strings.Join(deltas, "")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	for _, d := range deltas {
		writeSSEToken(c, rec, d)
	}

	got := clientReassembleTokens(rec.Body.String())
	if got != want {
		t.Fatalf("token round-trip mismatch:\n want %q\n  got %q", want, got)
	}
}
