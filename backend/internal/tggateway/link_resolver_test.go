package tggateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUTF16RangeToByteRange_ASCII(t *testing.T) {
	text := "look at https://example.com/x now"
	start, end, ok := utf16RangeToByteRange(text, 8, 21)
	if !ok {
		t.Fatal("expected ok")
	}
	if got := text[start:end]; got != "https://example.com/x" {
		t.Fatalf("got %q", got)
	}
}

func TestUTF16RangeToByteRange_EmojiPrefix(t *testing.T) {
	text := "👀 https://example.com"
	start, end, ok := utf16RangeToByteRange(text, 3, 19)
	if !ok {
		t.Fatal("expected ok")
	}
	if got := text[start:end]; got != "https://example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestUTF16RangeToByteRange_CJKPrefix(t *testing.T) {
	text := "看看这个 https://example.com/页面"
	start, end, ok := utf16RangeToByteRange(text, 5, 22)
	if !ok {
		t.Fatal("expected ok")
	}
	if got := text[start:end]; got != "https://example.com/页面" {
		t.Fatalf("got %q", got)
	}
}

func TestUTF16RangeToByteRange_RejectsMidRuneAndOutOfRange(t *testing.T) {
	if _, _, ok := utf16RangeToByteRange("👀x", 1, 2); ok {
		t.Fatal("offset inside a surrogate pair must be rejected")
	}
	if _, _, ok := utf16RangeToByteRange("short", 0, 999); ok {
		t.Fatal("range past the text must be rejected")
	}
	if _, _, ok := utf16RangeToByteRange("short", -1, 2); ok {
		t.Fatal("negative offset must be rejected")
	}
}

func TestLinkEntities_KeepsOnlyURLTypes(t *testing.T) {
	text := "see https://a.example and hidden link"
	entities := linkEntities(text, []TelegramEntity{
		{Type: "url", Offset: 4, Length: 17},
		{Type: "bold", Offset: 0, Length: 3},
		{Type: "text_link", Offset: 27, Length: 5, URL: "https://b.example"},
		{Type: "text_link", Offset: 0, Length: 3},
	})
	if len(entities) != 2 {
		t.Fatalf("expected 2 link entities (url + text_link with url), got %d", len(entities))
	}
	if entities[0].URL != "https://a.example" {
		t.Fatalf("url entity must resolve from text, got %q", entities[0].URL)
	}
	if entities[1].URL != "https://b.example" {
		t.Fatalf("text_link entity must keep its url, got %q", entities[1].URL)
	}
}

func TestFetchTitle_ParsesTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><TITLE>  Example\nSite  </TITLE></head></html>"))
	}))
	defer srv.Close()

	f := NewLinkTitleFetcher()
	got := f.FetchTitle(context.Background(), srv.URL)
	if got != "Example Site" {
		t.Fatalf("expected collapsed title %q, got %q", "Example Site", got)
	}
}

func TestFetchTitle_FailuresYieldEmpty(t *testing.T) {
	f := NewLinkTitleFetcher()

	if got := f.FetchTitle(context.Background(), "http://127.0.0.1:1/nope"); got != "" {
		t.Fatalf("unreachable URL must yield empty title, got %q", got)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"not":"html"}`))
	}))
	defer srv.Close()
	if got := f.FetchTitle(context.Background(), srv.URL); got != "" {
		t.Fatalf("non-HTML must yield empty title, got %q", got)
	}
}

func TestEnrichEntities_DeduplicatesAndSkipsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<title>dup</title>"))
	}))
	defer srv.Close()

	entities := []TelegramEntity{
		{Type: "url", URL: srv.URL},
		{Type: "url", URL: srv.URL},
		{Type: "text_link", URL: ""},
	}
	got := enrichEntities(context.Background(), NewLinkTitleFetcher(), entities)
	if len(got) != 1 {
		t.Fatalf("expected dedup to 1 link, got %d", len(got))
	}
	if got[0].Title != "dup" {
		t.Fatalf("expected title dup, got %q", got[0].Title)
	}
}
