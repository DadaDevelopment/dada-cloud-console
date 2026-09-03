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

func TestRenderMessageAttachments(t *testing.T) {
	m := Message{Role: "user", Content: "", Attachments: []any{
		map[string]any{"kind": "voice", "duration_seconds": float64(34), "transcript": "ситуация такая"},
		map[string]any{"kind": "image"},
	}}
	got := renderMessage(m, time.Now())
	if !strings.Contains(got, "[voice 34s]: \"ситуация такая\"\n") {
		t.Fatalf("voice with transcript must render, got:\n%s", got)
	}
	if !strings.Contains(got, "[image]: [description unavailable]\n") {
		t.Fatalf("image without description must render unavailable, got:\n%s", got)
	}
}

func TestAttachmentToEntity(t *testing.T) {
	if got := attachmentToEntity(nil); got != nil {
		t.Fatalf("nil attachment -> nil, got %v", got)
	}
	got := attachmentToEntity(&RuntimeAttachment{
		Kind: "voice", FileID: "fv", DurationSec: 34, TranscriptAvailable: true, Transcript: "text",
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 attachment object, got %d", len(got))
	}
	obj := got[0].(map[string]any)
	if obj["kind"] != "voice" || obj["duration_seconds"] != 34 || obj["transcript"] != "text" {
		t.Fatalf("attachment object wrong: %v", obj)
	}
	if _, has := obj["description"]; has {
		t.Fatalf("unavailable description must be omitted: %v", obj)
	}
}

func TestRenderAttachmentVariants(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"kind": "voice", "duration_seconds": float64(34), "transcript": "привет"}, "[voice 34s]: \"привет\"\n"},
		{map[string]any{"kind": "voice", "duration_seconds": float64(12)}, "[voice 12s]: [transcription unavailable]\n"},
		{map[string]any{"kind": "image", "description": "скриншот формы"}, "[image]: скриншот формы\n"},
		{map[string]any{"kind": "image"}, "[image]: [description unavailable]\n"},
		{map[string]any{"kind": "document", "file_name": "report.pdf"}, "[document report.pdf]\n"},
		{map[string]any{"kind": "document"}, "[document unnamed]\n"},
		{map[string]any{"kind": "unknown"}, ""},
	}
	for i, c := range cases {
		if got := renderAttachment(c.in); got != c.want {
			t.Fatalf("case %d: got %q, want %q", i, got, c.want)
		}
	}
}
