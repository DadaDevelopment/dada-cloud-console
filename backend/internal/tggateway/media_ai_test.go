package tggateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAITranscribe_Success(t *testing.T) {
	var gotModel, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/audio/transcriptions") {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("multipart: %v", err)
			return
		}
		gotModel = r.FormValue("model")
		w.Write([]byte(`{"text": "привет, это тест"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	att := &TelegramAttachment{Kind: "voice", FilePath: writeTempFile(t, dir, "1.ogg", "OggS-fake")}

	cfg := &MediaAIConfig{GatewayURL: srv.URL, GatewayKey: "sk-test", STTModel: "or-whisper"}
	got, ok := aiTranscribe(context.Background(), srv.Client(), cfg, att)
	if !ok || got != "привет, это тест" {
		t.Fatalf("transcript wrong: ok=%v %q", ok, got)
	}
	if gotModel != "or-whisper" {
		t.Fatalf("model field must be forwarded, got %q", gotModel)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("auth header wrong: %q", gotAuth)
	}
}

func TestAITranscribe_Failures(t *testing.T) {
	dir := t.TempDir()
	cfg := &MediaAIConfig{GatewayURL: "http://127.0.0.1:1", GatewayKey: "k", STTModel: "m"}
	att := &TelegramAttachment{Kind: "voice", FilePath: filepath.Join(dir, "missing.ogg")}
	if _, ok := aiTranscribe(context.Background(), http.DefaultClient, cfg, att); ok {
		t.Fatal("unreadable file must yield unavailable")
	}

	att2 := &TelegramAttachment{Kind: "voice", FilePath: writeTempFile(t, dir, "2.ogg", "x")}
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota", http.StatusTooManyRequests)
	}))
	defer errSrv.Close()
	cfg2 := &MediaAIConfig{GatewayURL: errSrv.URL, GatewayKey: "k", STTModel: "m"}
	if _, ok := aiTranscribe(context.Background(), errSrv.Client(), cfg2, att2); ok {
		t.Fatal("gateway error must yield unavailable")
	}
}

func TestAIDescribe_Success(t *testing.T) {
	var gotImage bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		msgs := body["messages"].([]any)
		content := msgs[0].(map[string]any)["content"].([]any)
		for _, part := range content {
			p := part.(map[string]any)
			if p["type"] == "image_url" {
				iu := p["image_url"].(map[string]any)
				u := iu["url"].(string)
				gotImage = strings.HasPrefix(u, "data:image/jpeg;base64,")
			}
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"Скриншот формы регистрации."}}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	att := &TelegramAttachment{Kind: "image", FilePath: writeTempFile(t, dir, "3.jpg", "fakejpeg")}

	cfg := &MediaAIConfig{GatewayURL: srv.URL, GatewayKey: "sk", VisionModel: "or-vision"}
	got, ok := aiDescribe(context.Background(), srv.Client(), cfg, att)
	if !ok || got != "Скриншот формы регистрации." {
		t.Fatalf("description wrong: ok=%v %q", ok, got)
	}
	if !gotImage {
		t.Fatal("image_url data URI must be present in the request")
	}
}

func TestNewMediaAIResolvers_ZeroConfigKeepsStubs(t *testing.T) {
	tr, de := newMediaAIResolvers(nil)
	att := &TelegramAttachment{Kind: "voice", FilePath: "/nonexistent"}
	if _, ok := tr(context.Background(), att); ok {
		t.Fatal("nil config must keep stub transcribe")
	}
	if _, ok := de(context.Background(), att); ok {
		t.Fatal("nil config must keep stub describe")
	}
}
