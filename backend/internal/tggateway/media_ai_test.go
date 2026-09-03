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

func TestWhisperTranscribe_Success(t *testing.T) {
	var gotTask, gotLang, gotFile string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/asr" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("multipart: %v", err)
			return
		}
		gotTask = r.FormValue("task")
		gotLang = r.FormValue("language")
		f, _, err := r.FormFile("audio_file")
		if err != nil {
			t.Errorf("audio_file field: %v", err)
			return
		}
		defer f.Close()
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		gotFile = string(buf[:n])
		w.Write([]byte(`{"text": "привет, это тест"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	att := &TelegramAttachment{Kind: "voice", FilePath: writeTempFile(t, dir, "1.ogg", "OggS-fake")}

	cfg := &MediaAIConfig{WhisperBaseURL: srv.URL}
	got, ok := whisperTranscribe(context.Background(), cfg, att)
	if !ok || got != "привет, это тест" {
		t.Fatalf("transcript wrong: ok=%v %q", ok, got)
	}
	if gotTask != "transcribe" || gotLang != "ru" {
		t.Fatalf("task/language fields wrong: task=%q lang=%q", gotTask, gotLang)
	}
	if gotFile != "OggS-fake" {
		t.Fatalf("audio bytes must ride the multipart file field, got %q", gotFile)
	}
}

func TestWhisperTranscribe_Failures(t *testing.T) {
	dir := t.TempDir()
	cfg := &MediaAIConfig{WhisperBaseURL: "http://127.0.0.1:1"}
	att := &TelegramAttachment{Kind: "voice", FilePath: filepath.Join(dir, "missing.ogg")}
	if _, ok := whisperTranscribe(context.Background(), cfg, att); ok {
		t.Fatal("unreachable whisper must yield unavailable")
	}

	att2 := &TelegramAttachment{Kind: "voice", FilePath: writeTempFile(t, dir, "2.ogg", "x")}
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer errSrv.Close()
	if _, ok := whisperTranscribe(context.Background(), &MediaAIConfig{WhisperBaseURL: errSrv.URL}, att2); ok {
		t.Fatal("whisper 500 must yield unavailable")
	}
	if _, ok := whisperTranscribe(context.Background(), cfg, &TelegramAttachment{Kind: "voice"}); ok {
		t.Fatal("empty FilePath must yield unavailable without a request")
	}
}

func TestGatewayDescribe_Success(t *testing.T) {
	var gotImage, gotModel bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		gotModel = body["model"] == "vision"
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

	cfg := &MediaAIConfig{GatewayURL: srv.URL, GatewayKey: "sk", VisionModel: "vision"}
	got, ok := gatewayDescribe(context.Background(), cfg, att)
	if !ok || got != "Скриншот формы регистрации." {
		t.Fatalf("description wrong: ok=%v %q", ok, got)
	}
	if !gotImage || !gotModel {
		t.Fatalf("request shape wrong: image=%v model=%v", gotImage, gotModel)
	}
}

// TestNewMediaAIResolvers_PartialConfig pins the wiring semantics: whisper is
// always real (in-cluster default), vision degrades to the stub without a
// gateway config.
func TestNewMediaAIResolvers_PartialConfig(t *testing.T) {
	cfg := &MediaAIConfig{WhisperBaseURL: "http://127.0.0.1:1"}
	tr, de := newMediaAIResolvers(cfg)

	att := &TelegramAttachment{Kind: "voice", FilePath: "/nonexistent"}
	if _, ok := tr(context.Background(), att); ok {
		t.Fatal("unreachable whisper still yields unavailable, but it must be the REAL transcriber function in the pair")
	}

	img := &TelegramAttachment{Kind: "image", FilePath: "/nonexistent"}
	if _, ok := de(context.Background(), img); ok {
		t.Fatal("no gateway config -> vision stub -> unavailable")
	}
}
