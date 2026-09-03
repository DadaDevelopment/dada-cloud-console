package tggateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// mediaDownloadTimeout bounds one file download. Media files can be a few
// dozen MB on slow networks; the cap exists so a stuck transfer cannot hold
// a batch (and its chat's run) forever. A timed-out download degrades to an
// attachment without FilePath -- the message still flows.
const mediaDownloadTimeout = 20 * time.Second

// MediaDownloader fetches an attachment's bytes from Telegram and caches
// them on local disk. The Bot API flow is two-step: getFile resolves a
// file_id to a relative file_path, then the bytes live at
// <baseURL>/file/bot<token>/<file_path>.
type MediaDownloader interface {
	Download(ctx context.Context, token string, att *TelegramAttachment, messageID int64) (localPath string, err error)
}

type telegramMediaDownloader struct {
	tg      TelegramClient
	baseURL string
	dir     string
	http    *http.Client
}

// NewMediaDownloader builds a downloader caching files under dir (created
// on demand). baseURL empty -> https://api.telegram.org.
func NewMediaDownloader(tg TelegramClient, baseURL, dir string) MediaDownloader {
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	return &telegramMediaDownloader{
		tg:      tg,
		baseURL: strings.TrimRight(baseURL, "/"),
		dir:     dir,
		http:    &http.Client{Timeout: mediaDownloadTimeout},
	}
}

func (d *telegramMediaDownloader) Download(ctx context.Context, token string, att *TelegramAttachment, messageID int64) (string, error) {
	if att == nil || att.FileID == "" {
		return "", fmt.Errorf("media download: no file_id")
	}

	filePath, err := d.tg.GetFilePath(ctx, token, att.FileID)
	if err != nil {
		return "", fmt.Errorf("media download: getFile: %w", err)
	}

	url := fmt.Sprintf("%s/file/bot%s/%s", d.baseURL, token, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("media download: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("media download: status %d", resp.StatusCode)
	}

	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return "", fmt.Errorf("media download: mkdir: %w", err)
	}
	local := filepath.Join(d.dir, fmt.Sprintf("%d_%s", messageID, filepath.Base(filePath)))
	out, err := os.Create(local)
	if err != nil {
		return "", fmt.Errorf("media download: create: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", fmt.Errorf("media download: write: %w", err)
	}
	return local, nil
}

// stubResolvers are the zero-config fallback: availability flags stay false
// and the A2A context says so explicitly instead of inventing content. When
// TG_MEDIA_GATEWAY_* env is set, runPollerDebounced swaps these for the real
// AI resolvers (media_ai.go) -- the swap point is resolverHooks below.
func stubTranscribe(ctx context.Context, att *TelegramAttachment) (string, bool) {
	return "", false
}

func stubDescribe(ctx context.Context, att *TelegramAttachment) (string, bool) {
	return "", false
}

// resolverHooks are the active STT/vision functions. Package-level because
// the pipeline is per-poller but the config is per-process; tests override
// them and restore with defer.
var (
	transcribeHook transcribeFn = stubTranscribe
	describeHook   describeFn   = stubDescribe
)

// resolveAttachment runs the pipeline for one message's attachment:
// download to the cache, then STT for voice/video_note, vision for images.
// Every stage degrades independently -- a download failure still lets the
// message flow with an unavailable marker.
func resolveAttachment(ctx context.Context, downloader MediaDownloader, token string, att *TelegramAttachment, messageID int64) {
	if att == nil {
		return
	}
	if path, err := downloader.Download(ctx, token, att, messageID); err == nil {
		att.FilePath = path
	}

	switch att.Kind {
	case "voice", "video_note":
		att.Transcript, att.TranscriptAvailable = transcribeHook(ctx, att)
	case "image":
		att.Description, att.DescriptionAvailable = describeHook(ctx, att)
	}
}
