# DadaAgent Cloud-Task — dada-cloud (cloud) side Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the console fire an autonomous DadaAgent task from an app chip, mint the repo + service tokens, submit+execute the intent, receive status/artifacts via webhook, and show it live.

**Architecture:** New `cloud_tasks` table + REST handlers in the existing Gin backend. A curated catalog maps `task_type → skill + param resolver`. Backend mints a short-lived GitHub App install token and a Keycloak client-credentials token, calls DadaAgent `POST /v1/agentsync/intents` + `/execute`, stores the row, and updates it from a JWKS-gated webhook callback. Frontend renders chips + a status card.

**Tech Stack:** Go 1.22 + Gin + pgx/PostgreSQL; Next.js 16 / React 19; Keycloak (realm `master`, `id.dada-tuda.ru`); GitHub App `argocd-dada`.

## Global Constraints

- No source comments — Go doc-comments / TS docstrings only (user rule).
- Migrations: numbered forward-only, `IF NOT EXISTS`, end with `GRANT ... TO dada;`.
- Handler authz on every mutating route: `auth.GetClaims` → `h.effectiveRole` (`pgx.ErrNoRows`→404) → `canWrite`→403.
- Secrets (install token, metrika token, client secret): TLS only, never logged, never persisted to git.
- Trunk-based: commit on `main`, push after every commit. No feature branches.
- This plan = MVP phase 1 (single task `yandex-metrika-goals`). Phases 2-5 deferred (see spec).
- Keycloak issuer `https://id.dada-tuda.ru/realms/master`; token URL `<issuer>/protocol/openid-connect/token`.

---

### Task 1: Migration — `cloud_tasks` table

**Files:**
- Create: `backend/migrations/025_cloud_tasks.sql`

**Interfaces:**
- Produces: table `cloud_tasks(id, project_id, environment_id, app_name, git_repo_id, task_type, intent_id, workflow_id, status, pr_url, artifacts jsonb, error, actor_id, created_at, updated_at)`.

- [ ] **Step 1: Write the migration**

```sql
-- 025_cloud_tasks.sql
-- DadaAgent cloud-task integration: one row per fired task.
-- A task is imperative (runs on the agent), NOT in the operations/gitops machine.
-- Forward-only, additive, idempotent.

CREATE TABLE IF NOT EXISTS cloud_tasks (
    id             UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id     UUID         NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_id UUID         NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    app_name       VARCHAR(255) NOT NULL,
    git_repo_id    UUID         REFERENCES git_repos(id) ON DELETE SET NULL,
    task_type      VARCHAR(100) NOT NULL,
    intent_id      VARCHAR(255),
    workflow_id    VARCHAR(255),
    status         VARCHAR(20)  NOT NULL DEFAULT 'running'
                   CHECK (status IN ('running','completed','failed','canceled')),
    pr_url         VARCHAR(1000),
    artifacts      JSONB        NOT NULL DEFAULT '[]',
    error          TEXT,
    actor_id       UUID         NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cloud_tasks_project_app
    ON cloud_tasks(project_id, app_name);
CREATE INDEX IF NOT EXISTS idx_cloud_tasks_running
    ON cloud_tasks(status) WHERE status = 'running';
CREATE UNIQUE INDEX IF NOT EXISTS idx_cloud_tasks_intent
    ON cloud_tasks(intent_id) WHERE intent_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON cloud_tasks TO dada;
```

- [ ] **Step 2: Apply against a scratch DB and verify**

Run: `psql "$DATABASE_URL" -f backend/migrations/025_cloud_tasks.sql && psql "$DATABASE_URL" -c '\d cloud_tasks'`
Expected: table prints with the 3 indexes. (If `users(id)` PK differs, match the FK to the real users PK column — check `001_*.sql`.)

- [ ] **Step 3: Commit**

```bash
git add backend/migrations/025_cloud_tasks.sql
git commit -m "feat(cloud-task): add cloud_tasks table (migration 025)"
git push
```

---

### Task 2: Config fields

**Files:**
- Modify: `backend/internal/config/config.go` (Config struct after the Grafana block ~line 131; `Load()` after the Grafana section ~line 222)

**Interfaces:**
- Produces: `cfg.DadaAgentBaseURL`, `cfg.KeycloakTokenURL`, `cfg.CloudAgentClientID`, `cfg.CloudAgentClientSecret`, `cfg.CloudTaskCallbackURL`, `cfg.GithubAppID`, `cfg.GithubAppPrivateKey`, `cfg.MetrikaOAuthToken`.

- [ ] **Step 1: Add fields to the Config struct**

```go
	DadaAgentBaseURL       string // DADA_AGENT_BASE_URL (e.g. http://dadagent.agent.svc:8080)
	KeycloakTokenURL       string // KEYCLOAK_TOKEN_URL (issuer + /protocol/openid-connect/token)
	CloudAgentClientID     string // CLOUD_AGENT_CLIENT_ID (Keycloak SA client dada-cloud-backend)
	CloudAgentClientSecret string // CLOUD_AGENT_CLIENT_SECRET
	CloudTaskCallbackURL   string // CLOUD_TASK_CALLBACK_URL (public webhook URL the agent calls back)
	GithubAppID            string // GITHUB_APP_ID (numeric app id of argocd-dada)
	GithubAppPrivateKey    string // GITHUB_APP_PRIVATE_KEY (PEM; PKCS1/PKCS8)
	MetrikaOAuthToken      string // METRIKA_OAUTH_TOKEN (Yandex Metrika mgmt API token)
```

- [ ] **Step 2: Wire them in `Load()`**

```go
		DadaAgentBaseURL:       getEnv("DADA_AGENT_BASE_URL", ""),
		KeycloakTokenURL:       getEnv("KEYCLOAK_TOKEN_URL", ""),
		CloudAgentClientID:     getEnv("CLOUD_AGENT_CLIENT_ID", ""),
		CloudAgentClientSecret: getEnv("CLOUD_AGENT_CLIENT_SECRET", ""),
		CloudTaskCallbackURL:   getEnv("CLOUD_TASK_CALLBACK_URL", ""),
		GithubAppID:            getEnv("GITHUB_APP_ID", ""),
		GithubAppPrivateKey:    getEnv("GITHUB_APP_PRIVATE_KEY", ""),
		MetrikaOAuthToken:      getEnv("METRIKA_OAUTH_TOKEN", ""),
```

- [ ] **Step 3: Build**

Run: `cd backend && go build ./...`
Expected: builds clean.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/config/config.go
git commit -m "feat(cloud-task): config for dadagent, keycloak SA, github app, metrika"
git push
```

---

### Task 3: GitHub App install-token minting

**Files:**
- Create: `backend/internal/github/installtoken.go`
- Create: `backend/internal/github/installtoken_test.go`

**Interfaces:**
- Consumes: `cfg.GithubAppID`, `cfg.GithubAppPrivateKey`.
- Produces: `func MintInstallToken(ctx context.Context, appID, privateKeyPEM string, installationID int64) (token string, expiresAt time.Time, err error)`.

> If a mint function already exists from connect-repo work, reuse it and skip this task. Search `backend/internal/api/gitrepos.go` + `backend/internal/github/` for `installation` token minting first.

- [ ] **Step 1: Write the failing test (JWT shape)**

```go
package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestBuildAppJWT_IsValidRS256(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der := x509.MarshalPKCS1PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})

	tok, err := buildAppJWT("12345", string(pemBytes))
	if err != nil {
		t.Fatalf("buildAppJWT: %v", err)
	}
	parsed, err := jwt.Parse(tok, func(t *jwt.Token) (any, error) { return &key.PublicKey, nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("token invalid: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["iss"] != "12345" {
		t.Fatalf("iss = %v, want 12345", claims["iss"])
	}
}
```

- [ ] **Step 2: Run it — fails (no `buildAppJWT`)**

Run: `cd backend && go test ./internal/github/ -run TestBuildAppJWT -v`
Expected: compile error / FAIL.

- [ ] **Step 3: Implement**

```go
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// buildAppJWT signs the short-lived GitHub App JWT (iss=appID, 9-min expiry, RS256).
func buildAppJWT(appID, privateKeyPEM string) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return "", fmt.Errorf("parse app private key: %w", err)
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
}

// MintInstallToken exchanges the App JWT for a short-lived installation access
// token (~1h) scoped to one installation. The token is a secret: never log it.
func MintInstallToken(ctx context.Context, appID, privateKeyPEM string, installationID int64) (string, time.Time, error) {
	appJWT, err := buildAppJWT(appID, privateKeyPEM)
	if err != nil {
		return "", time.Time{}, err
	}
	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github install token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", time.Time{}, fmt.Errorf("github install token: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", time.Time{}, err
	}
	return out.Token, out.ExpiresAt, nil
}
```

- [ ] **Step 4: Run — passes**

Run: `cd backend && go test ./internal/github/ -run TestBuildAppJWT -v`
Expected: PASS. (Add `github.com/golang-jwt/jwt/v5` to go.mod if absent: `go get github.com/golang-jwt/jwt/v5`.)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/github/ backend/go.mod backend/go.sum
git commit -m "feat(cloud-task): mint short-lived GitHub App install tokens"
git push
```

---

### Task 4: Keycloak client-credentials token source

**Files:**
- Create: `backend/internal/dadagent/token.go`
- Create: `backend/internal/dadagent/token_test.go`

**Interfaces:**
- Consumes: `cfg.KeycloakTokenURL`, `cfg.CloudAgentClientID`, `cfg.CloudAgentClientSecret`.
- Produces: `type TokenSource struct{...}`; `func NewTokenSource(tokenURL, clientID, secret string) *TokenSource`; `func (ts *TokenSource) Token(ctx context.Context) (string, error)` (cached until ~30s before exp).

- [ ] **Step 1: Failing test — caches until expiry**

```go
package dadagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenSource_CachesToken(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "abc", "expires_in": 300})
	}))
	defer srv.Close()

	ts := NewTokenSource(srv.URL, "cid", "secret")
	for i := 0; i < 3; i++ {
		tok, err := ts.Token(context.Background())
		if err != nil || tok != "abc" {
			t.Fatalf("token=%q err=%v", tok, err)
		}
	}
	if hits != 1 {
		t.Fatalf("token endpoint hit %d times, want 1 (cached)", hits)
	}
}
```

- [ ] **Step 2: Run — fails**

Run: `cd backend && go test ./internal/dadagent/ -run TestTokenSource -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

```go
package dadagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenSource fetches and caches a Keycloak client-credentials access token.
type TokenSource struct {
	tokenURL string
	clientID string
	secret   string
	hc       *http.Client

	mu   sync.Mutex
	tok  string
	exp  time.Time
}

func NewTokenSource(tokenURL, clientID, secret string) *TokenSource {
	return &TokenSource{
		tokenURL: tokenURL, clientID: clientID, secret: secret,
		hc: &http.Client{Timeout: 15 * time.Second},
	}
}

// Token returns a cached token, refreshing when within 30s of expiry.
func (ts *TokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tok != "" && time.Now().Before(ts.exp.Add(-30*time.Second)) {
		return ts.tok, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {ts.clientID},
		"client_secret": {ts.secret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := ts.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("keycloak token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("keycloak token: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	ts.tok = out.AccessToken
	ts.exp = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	return ts.tok, nil
}
```

- [ ] **Step 4: Run — passes**

Run: `cd backend && go test ./internal/dadagent/ -run TestTokenSource -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/dadagent/token.go backend/internal/dadagent/token_test.go
git commit -m "feat(cloud-task): keycloak client-credentials token source"
git push
```

---

### Task 5: DadaAgent client (submit + execute + file proxy)

**Files:**
- Create: `backend/internal/dadagent/client.go`
- Create: `backend/internal/dadagent/client_test.go`

**Interfaces:**
- Consumes: `*TokenSource` (Task 4); `cfg.DadaAgentBaseURL`.
- Produces:
  - `type IntentRequest struct{ IntentID, Summary, TaskType, CoreLoopImpact string; PrimaryPillar string; VisiblePrimitives []string; KPIHypothesis []KPI; CloudPayload map[string]any }`
  - `type SubmitResult struct{ WorkflowID string }`
  - `func New(baseURL string, ts *TokenSource) *Client`
  - `func (c *Client) SubmitIntent(ctx, IntentRequest) (SubmitResult, error)` → `POST /v1/agentsync/intents`
  - `func (c *Client) ExecuteIntent(ctx, intentID string) error` → `POST /v1/agentsync/intents/{id}/execute`
  - `func (c *Client) GetFile(ctx, fileID string) (io.ReadCloser, string, error)` → `GET /v1/files/{id}` (proxy)

> NOTE: DadaAgent `IntentSubmitRequest` requires `core_loop_impact` (min 10 chars), `primary_pillar`, `visible_primitives` (≥1), `kpi_hypothesis` (≥1). Supply safe defaults (see Task 8 builder). `CloudPayload` rides in the new `cloud_payload` field the agent adds (agent-side plan Task 2).

- [ ] **Step 1: Failing test — submit sends bearer + correct body**

```go
package dadagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubmitIntent_PostsBearerAndBody(t *testing.T) {
	var gotAuth, gotPath string
	var body map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agentsync/intents", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": true, "execution_mode": "auto",
			"workflow": map[string]any{"workflow_id": "wf-1"},
		})
	})
	agent := httptest.NewServer(mux)
	defer agent.Close()

	kc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 300})
	}))
	defer kc.Close()

	c := New(agent.URL, NewTokenSource(kc.URL, "cid", "sec"))
	res, err := c.SubmitIntent(context.Background(), IntentRequest{
		IntentID: "int-1", Summary: "do thing", TaskType: "yandex-metrika-goals",
		CoreLoopImpact: "instrument site", PrimaryPillar: "growth",
		VisiblePrimitives: []string{"web"}, KPIHypothesis: []KPI{{Name: "conv", Direction: "up"}},
		CloudPayload: map[string]any{"cloud_task_id": "ct-1"},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if res.WorkflowID != "wf-1" {
		t.Fatalf("workflow_id=%q want wf-1", res.WorkflowID)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if gotPath != "/v1/agentsync/intents" || body["task_type"] != "yandex-metrika-goals" {
		t.Fatalf("path=%q body=%v", gotPath, body)
	}
}
```

- [ ] **Step 2: Run — fails**

Run: `cd backend && go test ./internal/dadagent/ -run TestSubmitIntent -v`
Expected: FAIL.

- [ ] **Step 3: Implement client**

```go
package dadagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type KPI struct {
	Name      string `json:"name"`
	Direction string `json:"direction"`
}

type IntentRequest struct {
	IntentID          string         `json:"intent_id"`
	Summary           string         `json:"summary"`
	TaskType          string         `json:"task_type"`
	Priority          string         `json:"priority"`
	CoreLoopImpact    string         `json:"core_loop_impact"`
	PrimaryPillar     string         `json:"primary_pillar"`
	VisiblePrimitives []string       `json:"visible_primitives"`
	KPIHypothesis     []KPI          `json:"kpi_hypothesis"`
	CloudPayload      map[string]any `json:"cloud_payload"`
}

type SubmitResult struct{ WorkflowID string }

type Client struct {
	baseURL string
	ts      *TokenSource
	hc      *http.Client
}

func New(baseURL string, ts *TokenSource) *Client {
	if baseURL == "" || ts == nil {
		return nil
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), ts: ts, hc: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) auth(ctx context.Context, req *http.Request) error {
	tok, err := c.ts.Token(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (c *Client) SubmitIntent(ctx context.Context, in IntentRequest) (SubmitResult, error) {
	if in.Priority == "" {
		in.Priority = "medium"
	}
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/agentsync/intents", bytes.NewReader(b))
	if err != nil {
		return SubmitResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.auth(ctx, req); err != nil {
		return SubmitResult{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("dadagent submit: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return SubmitResult{}, fmt.Errorf("dadagent submit: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Workflow struct {
			WorkflowID string `json:"workflow_id"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{WorkflowID: out.Workflow.WorkflowID}, nil
}

func (c *Client) ExecuteIntent(ctx context.Context, intentID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/agentsync/intents/"+intentID+"/execute", nil)
	if err != nil {
		return err
	}
	if err := c.auth(ctx, req); err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("dadagent execute: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("dadagent execute: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// GetFile proxies an artifact byte stream from the agent. Caller closes the reader.
func (c *Client) GetFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/files/"+fileID, nil)
	if err != nil {
		return nil, "", err
	}
	if err := c.auth(ctx, req); err != nil {
		return nil, "", err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("dadagent getfile: %w", err)
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, "", fmt.Errorf("dadagent getfile: status %d", resp.StatusCode)
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}
```

- [ ] **Step 4: Run — passes**

Run: `cd backend && go test ./internal/dadagent/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/dadagent/client.go backend/internal/dadagent/client_test.go
git commit -m "feat(cloud-task): dadagent client (submit/execute/getfile)"
git push
```

---

### Task 6: Catalog registry + metrika param resolver

**Files:**
- Create: `backend/internal/cloudtask/catalog.go`
- Create: `backend/internal/cloudtask/catalog_test.go`

**Interfaces:**
- Consumes: `cfg.MetrikaOAuthToken`.
- Produces:
  - `type Entry struct{ TaskType, SkillID, Label, Summary string; AppliesTo func(kind string) bool; ResolveParams func(cfg ResolverCfg) (map[string]any, error) }`
  - `type ResolverCfg struct{ MetrikaOAuthToken string }`
  - `func Catalog() []Entry`; `func Lookup(taskType string) (Entry, bool)`

- [ ] **Step 1: Failing test**

```go
package cloudtask

import "testing"

func TestCatalog_MetrikaEntry(t *testing.T) {
	e, ok := Lookup("yandex-metrika-goals")
	if !ok {
		t.Fatal("metrika entry missing")
	}
	if !e.AppliesTo("web") || e.AppliesTo("database") {
		t.Fatal("AppliesTo should be web-only")
	}
	params, err := e.ResolveParams(ResolverCfg{MetrikaOAuthToken: "tok"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if params["metrika_oauth_token"] != "tok" {
		t.Fatalf("token not propagated: %v", params)
	}
	goals, _ := params["goals"].([]map[string]string)
	if len(goals) != 4 {
		t.Fatalf("want 4 default goals, got %d", len(goals))
	}
}

func TestCatalog_MetrikaResolve_RequiresToken(t *testing.T) {
	e, _ := Lookup("yandex-metrika-goals")
	if _, err := e.ResolveParams(ResolverCfg{}); err == nil {
		t.Fatal("expected error when token missing")
	}
}
```

- [ ] **Step 2: Run — fails**

Run: `cd backend && go test ./internal/cloudtask/ -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
package cloudtask

import "fmt"

// ResolverCfg carries the server-side secrets a task's param resolver may need.
type ResolverCfg struct {
	MetrikaOAuthToken string
}

// Entry is one curated cloud-task: which agent skill runs, where it surfaces,
// and how the cloud resolves its params server-side (no user form in MVP).
type Entry struct {
	TaskType      string
	SkillID       string
	Label         string
	Summary       string
	AppliesTo     func(kind string) bool
	ResolveParams func(cfg ResolverCfg) (map[string]any, error)
}

func isWeb(kind string) bool { return kind == "web" || kind == "App" || kind == "app" }

var defaultMetrikaGoals = []map[string]string{
	{"name": "Отправка формы", "identifier": "form_submit"},
	{"name": "Заполнил контактные данные", "identifier": "form_start"},
	{"name": "Клик по CTA", "identifier": "cta_contact_click"},
	{"name": "Клик по мессенджеру или телефону", "identifier": "messenger_click"},
}

// Catalog is the curated cloud-task set. Adding a task = one Entry here +
// a matching cloud-task-tagged skill on the agent.
func Catalog() []Entry {
	return []Entry{
		{
			TaskType: "yandex-metrika-goals",
			SkillID:  "yandex-metrika-goals",
			Label:    "Yandex Metrika + goals",
			Summary:  "Wire Yandex Metrika counter and conversion goals into the app, open a PR.",
			AppliesTo: isWeb,
			ResolveParams: func(cfg ResolverCfg) (map[string]any, error) {
				if cfg.MetrikaOAuthToken == "" {
					return nil, fmt.Errorf("METRIKA_OAUTH_TOKEN not configured")
				}
				return map[string]any{
					"metrika_oauth_token": cfg.MetrikaOAuthToken,
					"goals":               defaultMetrikaGoals,
				}, nil
			},
		},
	}
}

func Lookup(taskType string) (Entry, bool) {
	for _, e := range Catalog() {
		if e.TaskType == taskType {
			return e, true
		}
	}
	return Entry{}, false
}
```

- [ ] **Step 4: Run — passes**

Run: `cd backend && go test ./internal/cloudtask/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/cloudtask/
git commit -m "feat(cloud-task): curated catalog + metrika param resolver"
git push
```

---

### Task 7: cloud_tasks store (DB CRUD)

**Files:**
- Create: `backend/internal/api/cloud_tasks_store.go`

**Interfaces:**
- Produces (methods on `*Handler`, using `h.pool`):
  - `insertCloudTask(ctx, in cloudTaskInsert) (models.CloudTask, error)`
  - `getCloudTask(ctx, id uuid.UUID) (models.CloudTask, error)`
  - `listCloudTasks(ctx, projectID uuid.UUID, appName string) ([]models.CloudTask, error)`
  - `updateCloudTaskByIntent(ctx, intentID string, status, prURL string, artifacts []byte, errMsg string) error`
- Also create model `models.CloudTask` in `backend/internal/models/cloud_task.go`.

- [ ] **Step 1: Model**

```go
package models

import "time"

type CloudTaskArtifact struct {
	FileID string `json:"file_id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Kind   string `json:"kind"`
}

type CloudTask struct {
	ID            string              `json:"id"`
	ProjectID     string              `json:"project_id"`
	EnvironmentID string              `json:"environment_id"`
	AppName       string              `json:"app_name"`
	GitRepoID     *string             `json:"git_repo_id,omitempty"`
	TaskType      string              `json:"task_type"`
	IntentID      *string             `json:"intent_id,omitempty"`
	WorkflowID    *string             `json:"workflow_id,omitempty"`
	Status        string              `json:"status"`
	PRURL         *string             `json:"pr_url,omitempty"`
	Artifacts     []CloudTaskArtifact `json:"artifacts"`
	Error         *string             `json:"error,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
}
```

- [ ] **Step 2: Store functions**

```go
package api

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"dada-cloud/backend/internal/models"
)

type cloudTaskInsert struct {
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	AppName       string
	GitRepoID     *uuid.UUID
	TaskType      string
	IntentID      string
	WorkflowID    string
	ActorID       uuid.UUID
}

const cloudTaskCols = `id, project_id, environment_id, app_name, git_repo_id, task_type,
	intent_id, workflow_id, status, pr_url, artifacts, error, created_at, updated_at`

func scanCloudTask(row pgx.Row) (models.CloudTask, error) {
	var t models.CloudTask
	var artifacts []byte
	if err := row.Scan(&t.ID, &t.ProjectID, &t.EnvironmentID, &t.AppName, &t.GitRepoID,
		&t.TaskType, &t.IntentID, &t.WorkflowID, &t.Status, &t.PRURL, &artifacts,
		&t.Error, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	if len(artifacts) > 0 {
		_ = json.Unmarshal(artifacts, &t.Artifacts)
	}
	return t, nil
}

func (h *Handler) insertCloudTask(ctx context.Context, in cloudTaskInsert) (models.CloudTask, error) {
	row := h.pool.QueryRow(ctx,
		`INSERT INTO cloud_tasks (project_id, environment_id, app_name, git_repo_id, task_type, intent_id, workflow_id, actor_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+cloudTaskCols,
		in.ProjectID, in.EnvironmentID, in.AppName, in.GitRepoID, in.TaskType, in.IntentID, in.WorkflowID, in.ActorID)
	return scanCloudTask(row)
}

func (h *Handler) getCloudTask(ctx context.Context, id uuid.UUID) (models.CloudTask, error) {
	return scanCloudTask(h.pool.QueryRow(ctx, `SELECT `+cloudTaskCols+` FROM cloud_tasks WHERE id=$1`, id))
}

func (h *Handler) listCloudTasks(ctx context.Context, projectID uuid.UUID, appName string) ([]models.CloudTask, error) {
	rows, err := h.pool.Query(ctx, `SELECT `+cloudTaskCols+` FROM cloud_tasks WHERE project_id=$1 AND app_name=$2 ORDER BY created_at DESC`, projectID, appName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.CloudTask
	for rows.Next() {
		t, err := scanCloudTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (h *Handler) updateCloudTaskByIntent(ctx context.Context, intentID, status, prURL string, artifacts []byte, errMsg string) error {
	_, err := h.pool.Exec(ctx,
		`UPDATE cloud_tasks SET
		   status = COALESCE(NULLIF($2,''), status),
		   pr_url = COALESCE(NULLIF($3,''), pr_url),
		   artifacts = CASE WHEN $4::jsonb IS NULL THEN artifacts ELSE $4::jsonb END,
		   error = COALESCE(NULLIF($5,''), error),
		   updated_at = NOW()
		 WHERE intent_id = $1`,
		intentID, status, prURL, artifacts, errMsg)
	return err
}
```

- [ ] **Step 3: Build**

Run: `cd backend && go build ./...`
Expected: clean. (Fix the module import path `dada-cloud/backend/...` to match `go.mod` module name.)

- [ ] **Step 4: Commit**

```bash
git add backend/internal/models/cloud_task.go backend/internal/api/cloud_tasks_store.go
git commit -m "feat(cloud-task): CloudTask model + DB store"
git push
```

---

### Task 8: cloud_tasks API handlers (create/list/get/artifact proxy)

**Files:**
- Create: `backend/internal/api/cloud_tasks.go`
- Modify: `backend/internal/api/router.go` (authed group)
- Modify: `backend/internal/api/handler.go` (add `dadagent *dadagent.Client` field + wiring)
- Test: `backend/internal/api/cloud_tasks_test.go`

**Interfaces:**
- Consumes: Tasks 3-7 (`github.MintInstallToken`, `dadagent.Client`, `cloudtask.Lookup`, store fns), `h.effectiveRole`, `canWrite`.
- Produces handlers: `CreateCloudTask`, `ListCloudTasks`, `GetCloudTask`, `ProxyCloudTaskArtifact`.

- [ ] **Step 1: Add the agent client to Handler**

In `handler.go`, add field `dadagent *dadagent.Client` and build it where other clients are built:
```go
	var agentClient *dadagent.Client
	if cfg.DadaAgentBaseURL != "" && cfg.CloudAgentClientID != "" {
		ts := dadagent.NewTokenSource(cfg.KeycloakTokenURL, cfg.CloudAgentClientID, cfg.CloudAgentClientSecret)
		agentClient = dadagent.New(cfg.DadaAgentBaseURL, ts)
	}
	h.dadagent = agentClient
```

- [ ] **Step 2: Handlers**

```go
package api

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"dada-cloud/backend/internal/auth"
	"dada-cloud/backend/internal/cloudtask"
	"dada-cloud/backend/internal/dadagent"
	gh "dada-cloud/backend/internal/github"
)

type createCloudTaskRequest struct {
	TaskType string `json:"task_type"`
}

// CreateCloudTask mints repo + agent params, submits + executes a DadaAgent intent.
func (h *Handler) CreateCloudTask(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}
	if h.dadagent == nil {
		respondError(c, http.StatusServiceUnavailable, "dadagent integration not configured")
		return
	}

	var req createCloudTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	entry, ok := cloudtask.Lookup(req.TaskType)
	if !ok {
		respondError(c, http.StatusBadRequest, "unknown task_type")
		return
	}

	repo, instID, err := h.resolveGitRepo(c.Request.Context(), projectID, envID, appName)
	if err != nil {
		respondError(c, http.StatusBadRequest, "no connected git repo for app")
		return
	}

	token, _, err := gh.MintInstallToken(c.Request.Context(), h.cfg.GithubAppID, h.cfg.GithubAppPrivateKey, instID)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to mint install token")
		return
	}
	params, err := entry.ResolveParams(cloudtask.ResolverCfg{MetrikaOAuthToken: h.cfg.MetrikaOAuthToken})
	if err != nil {
		respondError(c, http.StatusFailedDependency, err.Error())
		return
	}

	intentID := uuid.NewString()
	in := dadagent.IntentRequest{
		IntentID:          intentID,
		Summary:           entry.Summary + " (" + appName + ")",
		TaskType:          entry.TaskType,
		CoreLoopImpact:    "Cloud-fired task to improve " + appName + " growth instrumentation.",
		PrimaryPillar:     "growth",
		VisiblePrimitives: []string{"web"},
		KPIHypothesis:     []dadagent.KPI{{Name: "conversion_goals", Direction: "up"}},
		CloudPayload: map[string]any{
			"cloud_task_id": "", // set below after row insert
			"skill_id":      entry.SkillID,
			"repo":          map[string]any{"provider": "github", "full_name": repo, "install_token": token},
			"params":        params,
			"callback":      map[string]any{"url": h.cfg.CloudTaskCallbackURL},
		},
	}

	gitRepoUUID := instUUIDPtr(c.Request.Context(), h, projectID, envID, appName)
	row, err := h.insertCloudTask(c.Request.Context(), cloudTaskInsert{
		ProjectID: projectID, EnvironmentID: envID, AppName: appName,
		GitRepoID: gitRepoUUID, TaskType: entry.TaskType, IntentID: intentID,
		ActorID: claims.UserID,
	})
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record cloud task")
		return
	}
	in.CloudPayload["cloud_task_id"] = row.ID

	res, err := h.dadagent.SubmitIntent(c.Request.Context(), in)
	if err != nil {
		_ = h.updateCloudTaskByIntent(c.Request.Context(), intentID, "failed", "", nil, err.Error())
		respondError(c, http.StatusBadGateway, "agent submit failed")
		return
	}
	_ = h.setCloudTaskWorkflow(c.Request.Context(), intentID, res.WorkflowID)
	if err := h.dadagent.ExecuteIntent(c.Request.Context(), intentID); err != nil {
		_ = h.updateCloudTaskByIntent(c.Request.Context(), intentID, "failed", "", nil, err.Error())
		respondError(c, http.StatusBadGateway, "agent execute failed")
		return
	}
	row.WorkflowID = &res.WorkflowID
	c.JSON(http.StatusAccepted, gin.H{"cloud_task": row})
}

func (h *Handler) ListCloudTasks(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "membership check failed")
		return
	}
	tasks, err := h.listCloudTasks(c.Request.Context(), projectID, c.Param("appName"))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "list failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"cloud_tasks": tasks})
}

func (h *Handler) GetCloudTask(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	id, err := uuid.Parse(c.Param("taskId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	t, err := h.getCloudTask(c.Request.Context(), id)
	if err != nil {
		respondNotFound(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cloud_task": t})
}

// ProxyCloudTaskArtifact streams an artifact from the agent (source of truth).
func (h *Handler) ProxyCloudTaskArtifact(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if h.dadagent == nil {
		respondError(c, http.StatusServiceUnavailable, "agent not configured")
		return
	}
	body, ctype, err := h.dadagent.GetFile(c.Request.Context(), c.Param("fileId"))
	if err != nil {
		respondError(c, http.StatusBadGateway, "artifact fetch failed")
		return
	}
	defer body.Close()
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	c.Status(http.StatusOK)
	c.Header("Content-Type", ctype)
	_, _ = io.Copy(c.Writer, body)
}
```

> Implement the small helpers referenced above next to the store (Task 7): `resolveGitRepo(ctx, projectID, envID, appName) (repoFullName string, installationID int64, err error)` reading `git_repos` joined to `git_app_installations`; `instUUIDPtr(...)` returning the `git_repos.id`; `setCloudTaskWorkflow(ctx, intentID, workflowID)` doing `UPDATE cloud_tasks SET workflow_id=$2 WHERE intent_id=$1`. These are thin SQL reads/writes mirroring Task 7.

- [ ] **Step 3: Register routes** (`router.go`, inside the authed `api` group)

```go
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/cloud-tasks", h.ListCloudTasks)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/cloud-tasks", h.CreateCloudTask)
		api.GET("/projects/:projectId/cloud-tasks/:taskId", h.GetCloudTask)
		api.GET("/projects/:projectId/cloud-tasks/:taskId/artifacts/:fileId", h.ProxyCloudTaskArtifact)
```

- [ ] **Step 4: Handler test (create path, httptest agent)**

Write `cloud_tasks_test.go` spinning a fake agent + fake keycloak + fake GitHub install endpoint (override the GitHub base via a package var or interface if needed for testability); assert a `cloud_tasks` row is inserted with `status=running` and the agent saw both `/intents` and `/execute`. (If GitHub minting is hard to stub, factor `MintInstallToken`'s base URL behind a package var defaulting to `https://api.github.com`.)

- [ ] **Step 5: Build + test + commit**

Run: `cd backend && go build ./... && go test ./internal/api/ -run CloudTask -v`
Expected: PASS.
```bash
git add backend/internal/api/cloud_tasks.go backend/internal/api/cloud_tasks_test.go backend/internal/api/router.go backend/internal/api/handler.go
git commit -m "feat(cloud-task): create/list/get/artifact-proxy handlers + routes"
git push
```

---

### Task 9: Webhook callback intake (JWKS-gated)

**Files:**
- Create: `backend/internal/api/webhooks_dadagent.go`
- Modify: `backend/internal/api/router.go` (OUTSIDE the auth group)
- Test: `backend/internal/api/webhooks_dadagent_test.go`

**Interfaces:**
- Consumes: `auth.NewKeycloakVerifier`/`Verify` (existing `auth/oidc.go`), `h.updateCloudTaskByIntent` (Task 7).
- Produces: `DadaAgentWebhook` handler; verifies a Keycloak bearer whose client/azp is `dada-agent`.

- [ ] **Step 1: Failing test — rejects wrong/absent bearer, accepts valid + updates row**

Write a test that builds a `DadaAgentWebhook` with an injected fake verifier (interface `tokenVerifier{ Verify(ctx, raw) (*auth.KeycloakClaims, error) }`); assert: missing Authorization → 401; claims with `azp != dada-agent` → 403; valid → 200 and `updateCloudTaskByIntent` called with mapped status.

- [ ] **Step 2: Implement**

```go
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"dada-cloud/backend/internal/auth"
)

type dadaAgentCallback struct {
	CloudTaskID string `json:"cloud_task_id"`
	IntentID    string `json:"intent_id"`
	WorkflowID  string `json:"workflow_id"`
	Event       string `json:"event"`
	Status      string `json:"status"`
	PRURL       string `json:"pr_url"`
	Artifacts   []struct {
		FileID string `json:"file_id"`
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Kind   string `json:"kind"`
	} `json:"artifacts"`
	Error string `json:"error"`
}

// mapAgentStatus folds agent task status into the cloud_tasks status enum.
func mapAgentStatus(event, status string) string {
	switch {
	case event == "completed" || status == "completed":
		return "completed"
	case event == "failed" || status == "failed":
		return "failed"
	default:
		return "running"
	}
}

// DadaAgentWebhook ingests agent status/artifact callbacks. Bearer-gated by JWKS;
// only the agent's own client (azp=dada-agent) is accepted. Idempotent updates.
func (h *Handler) DadaAgentWebhook(c *gin.Context) {
	raw := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if raw == "" || raw == c.GetHeader("Authorization") {
		respondUnauthorized(c)
		return
	}
	claims, err := h.agentVerifier.Verify(c.Request.Context(), raw)
	if err != nil {
		respondUnauthorized(c)
		return
	}
	if claims.AuthorizedParty != "dada-agent" && !hasClient(claims, "dada-agent") {
		respondForbidden(c)
		return
	}

	var cb dadaAgentCallback
	if err := c.ShouldBindJSON(&cb); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if cb.IntentID == "" {
		respondError(c, http.StatusBadRequest, "missing intent_id")
		return
	}
	var artifactsJSON []byte
	if len(cb.Artifacts) > 0 {
		artifactsJSON, _ = json.Marshal(cb.Artifacts)
	}
	if err := h.updateCloudTaskByIntent(c.Request.Context(), cb.IntentID,
		mapAgentStatus(cb.Event, cb.Status), cb.PRURL, artifactsJSON, cb.Error); err != nil {
		respondError(c, http.StatusInternalServerError, "update failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
```

> `h.agentVerifier` is an `*auth.KeycloakVerifier` built once at startup (`auth.NewKeycloakVerifier(ctx, cfg.KeycloakIssuer, false, "", "dada-agent")`). Add the field to `Handler` + build in `handler.go`. `AuthorizedParty`/`hasClient`: confirm the field name on `auth.KeycloakClaims` (the explore showed `KeycloakClaims` exists; add an `azp` claim + a `resource_access` client check if not already parsed — small extension to `oidc.go`).

- [ ] **Step 3: Register route OUTSIDE auth group** (`router.go`, near the public git callback)

```go
	r.POST("/api/v1/webhooks/dadagent", h.DadaAgentWebhook)
```

- [ ] **Step 4: Build + test + commit**

Run: `cd backend && go build ./... && go test ./internal/api/ -run Webhook -v`
Expected: PASS.
```bash
git add backend/internal/api/webhooks_dadagent.go backend/internal/api/router.go backend/internal/api/handler.go backend/internal/auth/oidc.go
git commit -m "feat(cloud-task): JWKS-gated dadagent webhook callback intake"
git push
```

---

### Task 10: Frontend types + api group

**Files:**
- Modify: `frontend/lib/types.ts`
- Modify: `frontend/lib/api.ts`

**Interfaces:**
- Produces: `CloudTask`, `CloudTaskArtifact`, `CloudTasksResponse`, `CloudTaskResponse`, `CreateCloudTaskResponse`; `cloudTasksApi`.

- [ ] **Step 1: Types**

```typescript
export interface CloudTaskArtifact {
  file_id: string;
  name: string;
  size: number;
  kind: string;
}

export interface CloudTask {
  id: string;
  project_id: string;
  environment_id: string;
  app_name: string;
  task_type: string;
  intent_id?: string;
  workflow_id?: string;
  status: "running" | "completed" | "failed" | "canceled";
  pr_url?: string;
  artifacts: CloudTaskArtifact[];
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface CloudTasksResponse { cloud_tasks: CloudTask[] }
export interface CloudTaskResponse { cloud_task: CloudTask }
export interface CreateCloudTaskResponse { cloud_task: CloudTask }
```

- [ ] **Step 2: API group**

```typescript
export const cloudTasksApi = {
  list: (projectId: string, envId: string, appName: string) =>
    apiFetch<CloudTasksResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/cloud-tasks`),

  create: (projectId: string, envId: string, appName: string, taskType: string) =>
    apiFetch<CreateCloudTaskResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/cloud-tasks`,
      { method: "POST", body: { task_type: taskType } }),

  get: (projectId: string, taskId: string) =>
    apiFetch<CloudTaskResponse>(`/api/v1/projects/${projectId}/cloud-tasks/${taskId}`),

  artifactUrl: (projectId: string, taskId: string, fileId: string) =>
    `/api/v1/projects/${projectId}/cloud-tasks/${taskId}/artifacts/${fileId}`,
};
```

- [ ] **Step 3: Typecheck + commit**

Run: `cd frontend && npx tsc --noEmit`
Expected: clean.
```bash
git add frontend/lib/types.ts frontend/lib/api.ts
git commit -m "feat(cloud-task): frontend types + api client"
git push
```

---

### Task 11: Frontend — task chips + live status card on app page

**Files:**
- Create: `frontend/components/cloud-task/cloud-task-panel.tsx`
- Modify: `frontend/app/(console)/projects/[projectId]/apps/[appName]/page.tsx`
- Modify (i18n): `frontend/lib/i18n/console/*` (add `cloudTasks.*` keys — match existing console-lang-toggle structure)

**Interfaces:**
- Consumes: `cloudTasksApi`, `CloudTask`.
- Produces: `<CloudTaskPanel projectId envId appName appKind canMutate />`.

- [ ] **Step 1: Panel component (chip grid + running cards, 3s poll)**

```tsx
"use client";

import { useCallback, useEffect, useState } from "react";
import { cloudTasksApi } from "@/lib/api";
import type { CloudTask } from "@/lib/types";

const CATALOG: { taskType: string; label: string; appliesTo: (k: string) => boolean }[] = [
  { taskType: "yandex-metrika-goals", label: "Yandex Metrika + goals", appliesTo: (k) => k === "web" || k === "App" },
];

export function CloudTaskPanel(props: {
  projectId: string; envId: string; appName: string; appKind: string; canMutate: boolean;
}) {
  const { projectId, envId, appName, appKind, canMutate } = props;
  const [tasks, setTasks] = useState<CloudTask[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    void cloudTasksApi.list(projectId, envId, appName)
      .then((d) => setTasks(d.cloud_tasks ?? []))
      .catch((e) => setError(e instanceof Error ? e.message : "load failed"));
  }, [projectId, envId, appName]);

  useEffect(() => { load(); }, [load]);

  useEffect(() => {
    if (!tasks.some((t) => t.status === "running")) return;
    const id = setInterval(load, 3000);
    return () => clearInterval(id);
  }, [tasks, load]);

  const run = async (taskType: string) => {
    setBusy(taskType); setError(null);
    try {
      const { cloud_task } = await cloudTasksApi.create(projectId, envId, appName, taskType);
      setTasks((prev) => [cloud_task, ...prev]);
    } catch (e) {
      setError(e instanceof Error ? e.message : "run failed");
    } finally {
      setBusy(null);
    }
  };

  const chips = CATALOG.filter((e) => e.appliesTo(appKind));

  return (
    <section className="space-y-4">
      <h3 className="text-sm font-semibold text-gray-700">Agent tasks</h3>
      {error && <p className="text-sm text-red-600">{error}</p>}
      <div className="flex flex-wrap gap-2">
        {chips.map((e) => (
          <button
            key={e.taskType}
            disabled={!canMutate || busy !== null}
            onClick={() => run(e.taskType)}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm disabled:opacity-50"
          >
            {busy === e.taskType ? "Starting…" : `Run: ${e.label}`}
          </button>
        ))}
      </div>
      <ul className="space-y-2">
        {tasks.map((t) => (
          <li key={t.id} className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium text-gray-900">{t.task_type}</span>
              <StatusBadge status={t.status} />
            </div>
            {t.error && <p className="mt-1 text-xs text-red-600">{t.error}</p>}
            {t.pr_url && (
              <a href={t.pr_url} target="_blank" rel="noreferrer"
                 className="mt-2 inline-block text-sm font-medium text-blue-600 hover:underline">
                View PR →
              </a>
            )}
            {t.artifacts.length > 0 && (
              <ul className="mt-2 space-y-1">
                {t.artifacts.map((a) => (
                  <li key={a.file_id}>
                    <a className="text-sm text-gray-700 hover:text-blue-600"
                       href={cloudTasksApi.artifactUrl(projectId, t.id, a.file_id)} target="_blank" rel="noreferrer">
                      {a.name} ({Math.round(a.size / 1024)} KB)
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function StatusBadge({ status }: { status: CloudTask["status"] }) {
  const color =
    status === "completed" ? "bg-green-100 text-green-700" :
    status === "failed" ? "bg-red-100 text-red-700" :
    status === "canceled" ? "bg-gray-100 text-gray-600" :
    "bg-blue-100 text-blue-700";
  return <span className={`rounded-full px-2 py-0.5 text-xs font-semibold ${color}`}>{status}</span>;
}
```

- [ ] **Step 2: Mount on the app page**

In `apps/[appName]/page.tsx`, below the spec cards, render:
```tsx
<CloudTaskPanel
  projectId={projectId}
  envId={envId ?? ""}
  appName={appName}
  appKind={isCompose ? "compose" : "web"}
  canMutate={canMutate(role)}
/>
```
(Import it; reuse the page's existing `projectId`/`envId`/`appName`/`role`/`isCompose` bindings shown in the explore.)

- [ ] **Step 3: Verify in the running app**

Run the dev server (via the project's run mechanism / `intelij.execute_run_configuration` per CLAUDE.md), open a web app detail page, confirm the chip renders and disables while busy. Screenshot for proof.

- [ ] **Step 4: Typecheck + commit**

Run: `cd frontend && npx tsc --noEmit`
Expected: clean.
```bash
git add frontend/components/cloud-task/ "frontend/app/(console)/projects/[projectId]/apps/[appName]/page.tsx" frontend/lib/i18n/console/
git commit -m "feat(cloud-task): app-page chips + live status/PR/artifacts card"
git push
```

---

## Out of scope for this plan (deferred)

- Polling fallback if callbacks prove lossy (callback-only here).
- Param forms / custom goals / custom counter (server-side resolution only).
- Catalog growth (second/third task types).
- Live metrika token from `crossplane-system` secret (using `METRIKA_OAUTH_TOKEN` config instead — see Global Constraints).
- Keycloak clients `dada-cloud-backend` + `dada-agent` provisioning (argo-infra gitops, out-of-repo prerequisite).

## Self-review notes

- Spec D1–D6 each map to a task: D1→T3+T8, D2→T6, D3→T9+T11, D4→T4+T9, D5→T6, D6→T5(GetFile)+T8(proxy).
- Verify exact `go.mod` module path and substitute for `dada-cloud/backend/...` imports.
- Verify `auth.KeycloakClaims` exposes `azp`/`resource_access`; extend `oidc.go` if not (called out in T9).
- Confirm `git_repos`/`git_app_installations` give a numeric `installation_id` for minting (T8 `resolveGitRepo`).
