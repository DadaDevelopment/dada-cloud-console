package agentruntime

import (
	"strings"
	"unicode"
)

// A narrow courtesy completion, not an intent classifier. Ambiguous "done",
// agreement to a question, substantive batches and open loops always reach A2A.
func courtesyOnly(pending, history []Message, state RuntimeState) bool {
	if len(pending) == 0 {
		return false
	}
	for _, loop := range state.OpenLoops {
		if loop.Status == "open" {
			return false
		}
	}
	for _, m := range pending {
		if len(m.Attachments) > 0 || len(m.Entities) > 0 {
			return false
		}
		t := strings.ToLower(strings.TrimFunc(m.Content, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(".!…", r) }))
		switch t {
		case "спасибо", "спасибо большое", "благодарю", "ок, спасибо", "ок спасибо", "понял, спасибо", "поняла, спасибо", "👍", "thank you", "thanks":
		default:
			return false
		}
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			return strings.TrimSpace(history[i].Content) != "" && !strings.ContainsAny(history[i].Content, "?？")
		}
	}
	return false
}
