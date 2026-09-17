package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

func envValue(env []renderer.ManagedAgentEnvVar, name string) (string, bool) {
	for _, e := range env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

func TestSyncLangfusePrompt_PublishesChangedTextAndLinksVersion(t *testing.T) {
	var posted map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/v2/prompts/support":
			if r.URL.Query().Get("label") != "production" {
				t.Errorf("label query = %q", r.URL.Query().Get("label"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"version": 3, "type": "text", "prompt": "old text"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/v2/prompts":
			_ = json.NewDecoder(r.Body).Decode(&posted)
			_ = json.NewEncoder(w).Encode(map[string]any{"version": 4, "type": "text", "prompt": posted["prompt"]})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	spec := renderer.ManagedAgentSpec{
		Name:          "support",
		Prompt:        "new text",
		PromptVersion: "2026-09-17.v1",
		Env: []renderer.ManagedAgentEnvVar{
			{Name: "LANGFUSE_HOST", Value: srv.URL},
			{Name: "LANGFUSE_PUBLIC_KEY", Value: "pk"},
			{Name: "LANGFUSE_SECRET_KEY", Value: "sk"},
			{Name: "LANGFUSE_PROMPT_NAME", Value: "support"},
			{Name: "LANGFUSE_PROMPT_VERSION", Value: "3"},
			{Name: "OTHER", Value: "keep"},
		},
	}
	syncLangfusePrompt(context.Background(), &spec)

	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("pk:sk")); gotAuth != want {
		t.Fatalf("auth = %q, want %q", gotAuth, want)
	}
	if posted["prompt"] != "new text" || posted["type"] != "text" || posted["commitMessage"] != "2026-09-17.v1" {
		t.Fatalf("posted = %v", posted)
	}
	if labels, _ := posted["labels"].([]any); len(labels) != 1 || labels[0] != "production" {
		t.Fatalf("labels = %v", posted["labels"])
	}
	if v, _ := envValue(spec.Env, "LANGFUSE_PROMPT_VERSION"); v != "4" {
		t.Fatalf("LANGFUSE_PROMPT_VERSION = %q, want 4", v)
	}
	if v, _ := envValue(spec.Env, "LANGFUSE_PROMPT_NAME"); v != "support" {
		t.Fatalf("LANGFUSE_PROMPT_NAME = %q", v)
	}
	if v, _ := envValue(spec.Env, "OTHER"); v != "keep" {
		t.Fatalf("OTHER lost: %v", spec.Env)
	}
	if len(spec.Env) != 6 {
		t.Fatalf("env has %d entries, want 6 (stale link replaced, not duplicated): %v", len(spec.Env), spec.Env)
	}
}

func TestSyncLangfusePrompt_UnchangedTextReusesVersionAndOTLPBasicAuth(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		if u, p, ok := r.BasicAuth(); !ok || u != "pk-lf-1" || p != "sk-lf-2" {
			t.Errorf("basic auth = %q %q %v", u, p, ok)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": 7, "type": "text", "prompt": "same"})
	}))
	defer srv.Close()

	basic := base64.StdEncoding.EncodeToString([]byte("pk-lf-1:sk-lf-2"))
	spec := renderer.ManagedAgentSpec{
		Name:   "support",
		Prompt: "same",
		Env: []renderer.ManagedAgentEnvVar{
			{Name: "LANGFUSE_BASE_URL", Value: srv.URL + "/"},
			{Name: "OTEL_EXPORTER_OTLP_HEADERS", Value: "authorization=Basic%20" + basic},
		},
	}
	syncLangfusePrompt(context.Background(), &spec)

	if posts != 0 {
		t.Fatalf("identical text created %d new version(s)", posts)
	}
	if v, _ := envValue(spec.Env, "LANGFUSE_PROMPT_VERSION"); v != "7" {
		t.Fatalf("LANGFUSE_PROMPT_VERSION = %q, want 7", v)
	}
}

func TestSyncLangfusePrompt_FirstVersionWhenPromptUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, `{"message":"Prompt not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": 1, "type": "text", "prompt": "fresh"})
	}))
	defer srv.Close()

	spec := renderer.ManagedAgentSpec{
		Name:   "support",
		Prompt: "fresh",
		Env: []renderer.ManagedAgentEnvVar{
			{Name: "LANGFUSE_HOST", Value: srv.URL},
			{Name: "LANGFUSE_PUBLIC_KEY", Value: "pk"},
			{Name: "LANGFUSE_SECRET_KEY", Value: "sk"},
		},
	}
	syncLangfusePrompt(context.Background(), &spec)
	if v, _ := envValue(spec.Env, "LANGFUSE_PROMPT_VERSION"); v != "1" {
		t.Fatalf("LANGFUSE_PROMPT_VERSION = %q, want 1", v)
	}
}

func TestSyncLangfusePrompt_FailureDropsStaleLinkAndKeepsSave(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	spec := renderer.ManagedAgentSpec{
		Name:   "support",
		Prompt: "text",
		Env: []renderer.ManagedAgentEnvVar{
			{Name: "LANGFUSE_HOST", Value: srv.URL},
			{Name: "LANGFUSE_PUBLIC_KEY", Value: "pk"},
			{Name: "LANGFUSE_SECRET_KEY", Value: "sk"},
			{Name: "LANGFUSE_PROMPT_VERSION", Value: "2"},
		},
	}
	syncLangfusePrompt(context.Background(), &spec)
	if _, ok := envValue(spec.Env, "LANGFUSE_PROMPT_VERSION"); ok {
		t.Fatalf("stale prompt link survived a failed publish: %v", spec.Env)
	}
	if len(spec.Env) != 3 {
		t.Fatalf("env = %v", spec.Env)
	}
}

func TestSyncLangfusePrompt_NoCredentialsOnlyStripsLink(t *testing.T) {
	spec := renderer.ManagedAgentSpec{
		Name:   "support",
		Prompt: "text",
		Env: []renderer.ManagedAgentEnvVar{
			{Name: "LANGFUSE_PROMPT_NAME", Value: "support"},
			{Name: "LANGFUSE_PROMPT_VERSION", Value: "2"},
			{Name: "FOO", Value: "bar"},
		},
	}
	syncLangfusePrompt(context.Background(), &spec)
	if len(spec.Env) != 1 || spec.Env[0].Name != "FOO" {
		t.Fatalf("env = %v", spec.Env)
	}
}
