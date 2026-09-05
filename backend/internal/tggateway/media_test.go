package tggateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// unmarshalMessage decodes a JSON message object into tgMessage, for tests
// that exercise the media/entity parsing without a full Bot API server.
func unmarshalMessage(t *testing.T, jsonMsg string, m *tgMessage) error {
	t.Helper()
	return json.Unmarshal([]byte(jsonMsg), m)
}

func TestMediaAttachment_Voice(t *testing.T) {
	m := &tgMessage{}
	if err := unmarshalMessage(t, `{"voice":{"file_id":"fv","duration":34,"mime_type":"audio/ogg","file_size":9000}}`, m); err != nil {
		t.Fatal(err)
	}
	att := mediaAttachment(m)
	if att == nil || att.Kind != "voice" || att.FileID != "fv" || att.DurationSec != 34 || att.MimeType != "audio/ogg" {
		t.Fatalf("voice attachment wrong: %+v", att)
	}
}

func TestMediaAttachment_PhotoPicksLargest(t *testing.T) {
	m := &tgMessage{}
	if err := unmarshalMessage(t, `{"photo":[{"file_id":"small","width":90,"height":90},{"file_id":"mid","width":320},{"file_id":"big","width":1280,"file_size":500000}]}`, m); err != nil {
		t.Fatal(err)
	}
	att := mediaAttachment(m)
	if att == nil || att.Kind != "image" || att.FileID != "big" {
		t.Fatalf("photo must pick the last (largest) size, got %+v", att)
	}
}

func TestMediaAttachment_Document(t *testing.T) {
	m := &tgMessage{}
	if err := unmarshalMessage(t, `{"document":{"file_id":"fd","file_name":"report.pdf","mime_type":"application/pdf","file_size":12345}}`, m); err != nil {
		t.Fatal(err)
	}
	att := mediaAttachment(m)
	if att == nil || att.Kind != "document" || att.FileName != "report.pdf" || att.SizeBytes != 12345 {
		t.Fatalf("document attachment wrong: %+v", att)
	}
}

func TestMediaAttachment_None(t *testing.T) {
	m := &tgMessage{}
	if err := unmarshalMessage(t, `{"text":"just words"}`, m); err != nil {
		t.Fatal(err)
	}
	if att := mediaAttachment(m); att != nil {
		t.Fatalf("text message must have no attachment, got %+v", att)
	}
}

// fileTelegram serves getFile from the fake API server URL.
type fileTelegram struct{ fakeTelegram }

func (fileTelegram) GetFilePath(_ context.Context, _, _ string) (string, error) {
	return "voice/file_1.ogg", nil
}

func TestMediaDownloader_DownloadsAndCaches(t *testing.T) {
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OggS-fake-bytes"))
	}))
	defer fileSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botTOK/getFile" {
			w.Write([]byte(`{"ok":true,"result":{"file_path":"voice/file_1.ogg"}}`))
			return
		}
		if r.URL.Path == "/file/botTOK/voice/file_1.ogg" {
			w.Write([]byte("OggS-fake-bytes"))
			return
		}
		http.NotFound(w, r)
	}))
	defer apiSrv.Close()

	dl := NewMediaDownloader(fileTelegram{}, apiSrv.URL, t.TempDir())

	att := &TelegramAttachment{Kind: "voice", FileID: "fv1"}
	path, err := dl.Download(context.Background(), "TOK", att, 501)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if filepath.Base(path) != "501_file_1.ogg" {
		t.Fatalf("cache name must be messageID_fileName, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "OggS-fake-bytes" {
		t.Fatalf("cached bytes wrong: %q err=%v", data, err)
	}
}

func TestMediaDownloader_ErrorPaths(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer apiSrv.Close()

	dl := NewMediaDownloader(fakeTelegram{}, apiSrv.URL, t.TempDir())
	dl.(*telegramMediaDownloader).baseURL = apiSrv.URL

	if _, err := dl.Download(context.Background(), "TOK", &TelegramAttachment{Kind: "image", FileID: "x"}, 1); err == nil {
		t.Fatal("getFile failure must error")
	}
	if _, err := dl.Download(context.Background(), "TOK", &TelegramAttachment{Kind: "image"}, 1); err == nil {
		t.Fatal("missing file_id must error")
	}
}

func TestResolveAttachment_DegradesIndependently(t *testing.T) {
	att := &TelegramAttachment{Kind: "voice", FileID: "nope"}
	resolveAttachment(context.Background(), NewMediaDownloader(fakeTelegram{}, "http://127.0.0.1:1", t.TempDir()), "TOK", att, 9, stubTranscribe, stubDescribe)
	if att.FilePath != "" {
		t.Fatalf("failed download must leave FilePath empty, got %q", att.FilePath)
	}
	if att.TranscriptAvailable {
		t.Fatal("stub transcriber must not claim availability")
	}
}
