package agentruntime

import (
	"strings"
	"testing"
	"time"
)

func TestRenderMessageTemporal(t *testing.T) {
	m := Message{Role: "assistant", Content: "plain"}
	if got := renderMessage(m, time.Now()); got != "assistant: plain\n" {
		t.Fatalf("assistant render: %q", got)
	}
}

func TestRenderMessageLinksWithTitles(t *testing.T) {
	m := Message{Role: "user", Content: "посмотри сюда", Entities: []any{
		map[string]any{"url": "https://a.example", "title": "A Title"},
		map[string]any{"url": "https://b.example"},
		"not-a-map",
		map[string]any{"no-url": true},
		map[string]any{"url": ""},
	}}
	got := renderMessage(m, time.Now())
	if !strings.Contains(got, "user: посмотри сюда\n") {
		t.Fatalf("message line expected first, got:\n%s", got)
	}
	if !strings.Contains(got, "[link] https://a.example (A Title)\n") {
		t.Fatalf("link with title must render, got:\n%s", got)
	}
	if !strings.Contains(got, "[link] https://b.example\n") {
		t.Fatalf("bare link must render, got:\n%s", got)
	}
	if strings.Count(got, "[link]") != 2 {
		t.Fatalf("non-map / url-less entities must be skipped, got:\n%s", got)
	}
}

func TestLinksToEntities(t *testing.T) {
	if got := linksToEntities(nil); got != nil {
		t.Fatalf("nil links -> nil, got %v", got)
	}
	got := linksToEntities([]RuntimeLinkMeta{
		{URL: "https://a.example", Title: "A"},
		{URL: "", Title: "skip"},
		{URL: "https://b.example"},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(got))
	}
	a := got[0].(map[string]any)
	if a["url"] != "https://a.example" || a["title"] != "A" {
		t.Fatalf("entity a wrong: %v", a)
	}
	b := got[1].(map[string]any)
	if _, has := b["title"]; has {
		t.Fatalf("empty title must be omitted: %v", b)
	}
}
