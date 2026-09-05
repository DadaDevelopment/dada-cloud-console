package agentruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const structuredReplyFormat = "referral_reply_v1"

// The model chooses the response act; runtime renders the standard question
// from durable state. This is not a regex classifier of customer intent.
func renderReplyPlan(raw string, state RuntimeState) (string, error) {
	var p struct {
		Kind       string   `json:"kind"`
		Paragraphs []string `json:"paragraphs,omitempty"`
		Question   string   `json:"question,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewBufferString(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return "", fmt.Errorf("invalid reply plan")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("trailing reply content")
	}
	switch p.Kind {
	case "qualification":
		if len(p.Paragraphs) > 0 || p.Question != "" {
			return "", fmt.Errorf("qualification accepts no generated prose")
		}
		for _, row := range []struct{ key, text string }{{"experience", "У вас уже есть опыт торговли на форексе?"}, {"target", "На какой доход в месяц вы ориентируетесь?"}, {"blocker", "Что вам сейчас нужно, чтобы двигаться дальше?"}} {
			if f, ok := state.ReportedFacts[row.key]; !ok || strings.TrimSpace(f.Value) == "" {
				return row.text, nil
			}
		}
		return "", fmt.Errorf("qualification already complete")
	case "answer", "instruction", "offer":
		if p.Question != "" || len(p.Paragraphs) < 1 || len(p.Paragraphs) > 3 {
			return "", fmt.Errorf("expected one to three paragraphs")
		}
		for i, para := range p.Paragraphs {
			para = strings.TrimSpace(para)
			if para == "" || utf8.RuneCountInString(para) > 350 || strings.ContainsAny(para, "\n\r?？") {
				return "", fmt.Errorf("paragraph must be short declarative text")
			}
			p.Paragraphs[i] = strings.NewReplacer("—", "-", "–", "-").Replace(para)
		}
		return strings.Join(p.Paragraphs, "\n\n"), nil
	case "clarification":
		q := strings.TrimSpace(p.Question)
		if len(p.Paragraphs) > 0 || utf8.RuneCountInString(q) > 240 || !strings.HasSuffix(q, "?") || strings.Count(q, "?") != 1 || strings.ContainsAny(q, "\n\r") {
			return "", fmt.Errorf("expected one clarification question")
		}
		return strings.NewReplacer("—", "-", "–", "-").Replace(q), nil
	default:
		return "", fmt.Errorf("unknown reply act")
	}
}
