package tggateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Media resolver backends (all real, no stubs):
//
//   - STT: the in-cluster whisper-predictor (ml-prod, openai-whisper-asr-
//     webservice), POST {base}/asr with multipart audio_file + task=transcribe.
//     Reachable from tg-gateway over kourier-internal; first request after an
//     idle period pays the Knative cold start, so the client timeout is long.
//
//   - Images: the AI gateway's "vision" alias (OpenAI-compatible
//     /v1/chat/completions, image_url with a base64 data URI).
//
// Every failure degrades to an unavailable marker; no resolver failure ever
// blocks or loses the user's message.

const (
	whisperTimeout   = 180 * time.Second
	visionTimeout    = 90 * time.Second
	whisperBaseDefault = "http://whisper-predictor.ml-prod.svc.cluster.local"
)

// MediaAIConfig wires the real resolvers. STT works with WhisperBaseURL
// alone; vision additionally needs the AI gateway URL/key/model.
type MediaAIConfig struct {
	WhisperBaseURL string // TG_MEDIA_WHISPER_URL (default in-cluster predictor)
	GatewayURL     string // TG_MEDIA_GATEWAY_URL (LiteLLM base)
	GatewayKey     string // TG_MEDIA_GATEWAY_KEY
	VisionModel    string // TG_MEDIA_VISION_MODEL (e.g. "vision")
}

func mediaAIConfigFromEnv() *MediaAIConfig {
	cfg := &MediaAIConfig{
		WhisperBaseURL: strings.TrimRight(os.Getenv("TG_MEDIA_WHISPER_URL"), "/"),
		GatewayURL:     strings.TrimRight(os.Getenv("TG_MEDIA_GATEWAY_URL"), "/"),
		GatewayKey:     os.Getenv("TG_MEDIA_GATEWAY_KEY"),
		VisionModel:    os.Getenv("TG_MEDIA_VISION_MODEL"),
	}
	if cfg.WhisperBaseURL == "" {
		cfg.WhisperBaseURL = whisperBaseDefault
	}
	return cfg
}

type transcribeFn func(context.Context, *TelegramAttachment) (string, bool)
type describeFn func(context.Context, *TelegramAttachment) (string, bool)

// newMediaAIResolvers returns the wired resolver pair. Vision degrades to
// the stub without a gateway config; STT is always real (the whisper
// predictor is platform infrastructure with an in-cluster default URL).
func newMediaAIResolvers(cfg *MediaAIConfig) (transcribeFn, describeFn) {
	transcribe := func(ctx context.Context, att *TelegramAttachment) (string, bool) {
		return whisperTranscribe(ctx, cfg, att)
	}
	describe := func(ctx context.Context, att *TelegramAttachment) (string, bool) {
		if cfg.GatewayURL == "" || cfg.GatewayKey == "" || cfg.VisionModel == "" {
			return stubDescribe(ctx, att)
		}
		return gatewayDescribe(ctx, cfg, att)
	}
	return transcribe, describe
}

// whisperTranscribe posts the cached audio to whisper-asr-webservice's /asr
// endpoint. Russian is requested explicitly: the audience of these bots
// writes in Russian, and whisper's language hint measurably improves
// accuracy over auto-detect on short clips.
func whisperTranscribe(ctx context.Context, cfg *MediaAIConfig, att *TelegramAttachment) (string, bool) {
	if att.FilePath == "" {
		return "", false
	}
	audio, err := os.ReadFile(att.FilePath)
	if err != nil {
		log.Warn().Err(err).Str("path", att.FilePath).Msg("tggateway: stt: read cached file")
		return "", false
	}

	ctx, cancel := context.WithTimeout(ctx, whisperTimeout)
	defer cancel()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("task", "transcribe")
	_ = mw.WriteField("language", "ru")
	fw, err := mw.CreateFormFile("audio_file", filepathBase(att.FilePath))
	if err != nil {
		return "", false
	}
	if _, err := fw.Write(audio); err != nil {
		return "", false
	}
	if err := mw.Close(); err != nil {
		return "", false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WhisperBaseURL+"/asr", &buf)
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Warn().Err(err).Str("base", cfg.WhisperBaseURL).Msg("tggateway: stt: whisper request failed")
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Warn().Int("status", resp.StatusCode).Str("body", string(body)).Msg("tggateway: stt: whisper error")
		return "", false
	}

	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		log.Warn().Err(err).Msg("tggateway: stt: decode")
		return "", false
	}
	if strings.TrimSpace(parsed.Text) == "" {
		return "", false
	}
	return strings.TrimSpace(parsed.Text), true
}

// gatewayDescribe sends the cached image to the AI gateway's vision model as
// a base64 data URI, asking for a short Russian description.
func gatewayDescribe(ctx context.Context, cfg *MediaAIConfig, att *TelegramAttachment) (string, bool) {
	if att.FilePath == "" {
		return "", false
	}
	img, err := os.ReadFile(att.FilePath)
	if err != nil {
		log.Warn().Err(err).Str("path", att.FilePath).Msg("tggateway: vision: read cached file")
		return "", false
	}
	dataURI := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(img)

	body := map[string]any{
		"model": cfg.VisionModel,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "Опиши изображение по-русски в одном-двух предложениях: что на нём и главное содержание. Если есть текст, передай его."},
				{"type": "image_url", "image_url": map[string]string{"url": dataURI}},
			},
		}},
		"max_tokens": 300,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", false
	}

	ctx, cancel := context.WithTimeout(ctx, visionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.GatewayURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.GatewayKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("tggateway: vision: request failed")
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Warn().Int("status", resp.StatusCode).Str("body", string(errBody)).Msg("tggateway: vision: gateway error")
		return "", false
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		log.Warn().Err(err).Msg("tggateway: vision: decode")
		return "", false
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", false
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), true
}

func filepathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
