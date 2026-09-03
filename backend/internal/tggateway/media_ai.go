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

// Media AI resolver caps. Both are generous for their task but bounded: a
// wedged AI gateway must not hold a chat's run forever (the run inherits
// these caps through the resolveAttachment call chain).
const (
	sttTimeout    = 120 * time.Second
	visionTimeout = 60 * time.Second
)

// MediaAIConfig enables the real STT/vision resolvers. All fields required:
// partial config keeps the stubs (zero-config = zero behavior change).
type MediaAIConfig struct {
	GatewayURL   string
	GatewayKey   string
	STTModel     string
	VisionModel  string
}

func mediaAIConfigFromEnv() *MediaAIConfig {
	cfg := &MediaAIConfig{
		GatewayURL:  strings.TrimRight(os.Getenv("TG_MEDIA_GATEWAY_URL"), "/"),
		GatewayKey:  os.Getenv("TG_MEDIA_GATEWAY_KEY"),
		STTModel:    os.Getenv("TG_MEDIA_STT_MODEL"),
		VisionModel: os.Getenv("TG_MEDIA_VISION_MODEL"),
	}
	if cfg.GatewayURL == "" || cfg.GatewayKey == "" {
		return nil
	}
	return cfg
}

// newMediaAIResolvers returns the real transcribe/describe pair when the env
// configures them, or the zero-config stubs otherwise.
func newMediaAIResolvers(cfg *MediaAIConfig) (transcribeFn, describeFn func(context.Context, *TelegramAttachment) (string, bool)) {
	if cfg == nil {
		return stubTranscribe, stubDescribe
	}
	client := &http.Client{}
	transcribe := func(ctx context.Context, att *TelegramAttachment) (string, bool) {
		return aiTranscribe(ctx, client, cfg, att)
	}
	describe := func(ctx context.Context, att *TelegramAttachment) (string, bool) {
		return aiDescribe(ctx, client, cfg, att)
	}
	return transcribe, describe
}

type transcribeFn func(context.Context, *TelegramAttachment) (string, bool)
type describeFn func(context.Context, *TelegramAttachment) (string, bool)

// aiTranscribe POSTs the cached audio to the OpenAI-compatible
// /v1/audio/transcriptions endpoint (multipart file + model).
func aiTranscribe(ctx context.Context, client *http.Client, cfg *MediaAIConfig, att *TelegramAttachment) (string, bool) {
	if att.FilePath == "" {
		return "", false
	}
	audio, err := os.ReadFile(att.FilePath)
	if err != nil {
		log.Warn().Err(err).Str("path", att.FilePath).Msg("tggateway: stt: read cached file")
		return "", false
	}

	ctx, cancel := context.WithTimeout(ctx, sttTimeout)
	defer cancel()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", cfg.STTModel)
	fw, err := mw.CreateFormFile("file", filepathBase(att.FilePath))
	if err != nil {
		return "", false
	}
	if _, err := fw.Write(audio); err != nil {
		return "", false
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.GatewayURL+"/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+cfg.GatewayKey)

	resp, err := client.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("tggateway: stt: request failed")
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Warn().Int("status", resp.StatusCode).Str("body", string(body)).Msg("tggateway: stt: gateway error")
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

// aiDescribe sends the cached image to the vision model as a data URI in an
// OpenAI-compatible chat completion, asking for a short Russian description.
func aiDescribe(ctx context.Context, client *http.Client, cfg *MediaAIConfig, att *TelegramAttachment) (string, bool) {
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
				{"type": "text", "text": "Опиши изображение по-русски в одном-двух предложениях: что на нём, главное содержание."},
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

	resp, err := client.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("tggateway: vision: request failed")
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Warn().Int("status", resp.StatusCode).Str("body", string(body)).Msg("tggateway: vision: gateway error")
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
