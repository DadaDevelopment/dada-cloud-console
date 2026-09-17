package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// Langfuse prompt publishing is push-only: git stays the source of truth for
// the prompt text and its version string, and every save mirrors that text into
// the agent's Langfuse project as a text prompt named after the agent with the
// production label. The resulting integer version is handed to the runtime as
// LANGFUSE_PROMPT_NAME / LANGFUSE_PROMPT_VERSION so each generation links to
// the prompt version it ran on. Nothing is ever read back from Langfuse into
// git, and a Langfuse failure never fails the save: the agent then runs
// without a prompt link, which is what it did before this existed.
const (
	langfusePromptNameEnv    = "LANGFUSE_PROMPT_NAME"
	langfusePromptVersionEnv = "LANGFUSE_PROMPT_VERSION"
	langfusePublicKeyEnv     = "LANGFUSE_PUBLIC_KEY"
	langfuseSecretKeyEnv     = "LANGFUSE_SECRET_KEY"
	langfuseHostEnv          = "LANGFUSE_HOST"
	langfuseBaseURLEnv       = "LANGFUSE_BASE_URL"
	otelHeadersEnv           = "OTEL_EXPORTER_OTLP_HEADERS"
	langfuseDefaultHost      = "https://cloud.langfuse.com"
	langfusePromptLabel      = "production"
	langfusePromptsPath      = "/api/public/v2/prompts"
)

var langfusePromptHTTP = &http.Client{Timeout: 15 * time.Second}

type langfuseCredentials struct {
	host      string
	publicKey string
	secretKey string
}

// langfuseCredentialsFrom reads the agent's own Langfuse keys out of its env.
// The explicit LANGFUSE_* pair wins; an agent that only carries the OTLP basic
// auth header still yields the same pair, because that header is exactly
// base64(publicKey:secretKey).
func langfuseCredentialsFrom(env []renderer.ManagedAgentEnvVar) (langfuseCredentials, bool) {
	var creds langfuseCredentials
	creds.host = langfuseDefaultHost
	var otelHeaders string
	for _, e := range env {
		switch e.Name {
		case langfusePublicKeyEnv:
			creds.publicKey = strings.TrimSpace(e.Value)
		case langfuseSecretKeyEnv:
			creds.secretKey = strings.TrimSpace(e.Value)
		case langfuseHostEnv, langfuseBaseURLEnv:
			if v := strings.TrimRight(strings.TrimSpace(e.Value), "/"); v != "" {
				creds.host = v
			}
		case otelHeadersEnv:
			otelHeaders = e.Value
		}
	}
	if creds.publicKey == "" || creds.secretKey == "" {
		pk, sk := basicAuthFromOTLPHeaders(otelHeaders)
		if creds.publicKey == "" {
			creds.publicKey = pk
		}
		if creds.secretKey == "" {
			creds.secretKey = sk
		}
	}
	return creds, creds.publicKey != "" && creds.secretKey != ""
}

func basicAuthFromOTLPHeaders(raw string) (string, string) {
	for _, pair := range strings.Split(raw, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "authorization") {
			continue
		}
		if decoded, err := url.QueryUnescape(strings.TrimSpace(value)); err == nil {
			value = decoded
		}
		scheme, token, ok := strings.Cut(strings.TrimSpace(value), " ")
		if !ok || !strings.EqualFold(scheme, "Basic") {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(token))
		if err != nil {
			continue
		}
		pk, sk, ok := strings.Cut(string(decoded), ":")
		if ok {
			return pk, sk
		}
	}
	return "", ""
}

// withoutLangfusePromptLink drops the prompt link the previous save wrote. A
// link that outlives the text it pointed at would label the new prompt's
// traces with the old version, which is worse than no link.
func withoutLangfusePromptLink(env []renderer.ManagedAgentEnvVar) []renderer.ManagedAgentEnvVar {
	out := env[:0:0]
	for _, e := range env {
		if e.Name == langfusePromptNameEnv || e.Name == langfusePromptVersionEnv {
			continue
		}
		out = append(out, e)
	}
	return out
}

// syncLangfusePrompt mirrors spec.Prompt into Langfuse and rewrites the
// prompt-link env pair on the spec. It is safe to call without credentials
// (the stale link is still dropped) and it never returns an error: the caller
// is a git write that must succeed regardless of a third-party SaaS.
func syncLangfusePrompt(ctx context.Context, spec *renderer.ManagedAgentSpec) {
	spec.Env = withoutLangfusePromptLink(spec.Env)
	if strings.TrimSpace(spec.Prompt) == "" {
		return
	}
	creds, ok := langfuseCredentialsFrom(spec.Env)
	if !ok {
		return
	}
	version, err := ensureLangfusePromptVersion(ctx, creds, spec.Name, spec.Prompt, spec.PromptVersion)
	if err != nil {
		log.Printf("langfuse prompt: agent %s: %v (saving without prompt link)", spec.Name, err)
		return
	}
	spec.Env = append(spec.Env,
		renderer.ManagedAgentEnvVar{Name: langfusePromptNameEnv, Value: spec.Name},
		renderer.ManagedAgentEnvVar{Name: langfusePromptVersionEnv, Value: strconv.Itoa(version)},
	)
}

type langfusePromptResponse struct {
	Version int    `json:"version"`
	Prompt  string `json:"prompt"`
	Type    string `json:"type"`
}

// ensureLangfusePromptVersion returns the version whose text equals prompt,
// creating a new one only when the production version differs. Langfuse
// versions are append-only, so re-publishing identical text on every save
// would bump the version without any change behind it.
func ensureLangfusePromptVersion(ctx context.Context, creds langfuseCredentials, name, prompt, commitMessage string) (int, error) {
	current, found, err := getLangfusePrompt(ctx, creds, name)
	if err != nil {
		return 0, err
	}
	if found && current.Type == "text" && current.Prompt == prompt {
		return current.Version, nil
	}
	body := map[string]any{
		"name":   name,
		"type":   "text",
		"prompt": prompt,
		"labels": []string{langfusePromptLabel},
	}
	if commitMessage != "" {
		body["commitMessage"] = commitMessage
	}
	var created langfusePromptResponse
	if err := langfuseJSON(ctx, creds, http.MethodPost, langfusePromptsPath, body, &created); err != nil {
		return 0, err
	}
	if created.Version == 0 {
		return 0, fmt.Errorf("create prompt %q: response carries no version", name)
	}
	return created.Version, nil
}

func getLangfusePrompt(ctx context.Context, creds langfuseCredentials, name string) (langfusePromptResponse, bool, error) {
	var out langfusePromptResponse
	path := langfusePromptsPath + "/" + url.PathEscape(name) + "?label=" + langfusePromptLabel
	err := langfuseJSON(ctx, creds, http.MethodGet, path, nil, &out)
	if err == nil {
		return out, true, nil
	}
	var status *langfuseStatusError
	if asLangfuseStatus(err, &status) && status.code == http.StatusNotFound {
		return out, false, nil
	}
	return out, false, err
}

type langfuseStatusError struct {
	code int
	body string
}

func (e *langfuseStatusError) Error() string {
	return fmt.Sprintf("langfuse: status %d: %s", e.code, e.body)
}

func asLangfuseStatus(err error, target **langfuseStatusError) bool {
	if e, ok := err.(*langfuseStatusError); ok {
		*target = e
		return true
	}
	return false
}

func langfuseJSON(ctx context.Context, creds langfuseCredentials, method, path string, in any, out any) error {
	var payload io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("langfuse: encode %s %s: %w", method, path, err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, creds.host+path, payload)
	if err != nil {
		return fmt.Errorf("langfuse: build %s %s: %w", method, path, err)
	}
	req.SetBasicAuth(creds.publicKey, creds.secretKey)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := langfusePromptHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("langfuse: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return &langfuseStatusError{code: resp.StatusCode, body: strings.TrimSpace(string(raw))}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("langfuse: decode %s %s: %w", method, path, err)
	}
	return nil
}
