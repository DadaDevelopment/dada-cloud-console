package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/tggatewayclient"
)

func telegramTestCtx(t *testing.T, method, path, body string, claims *auth.Claims, agentName string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	c, w := agentTestContext(t, method, path, body, claims)
	c.Params = gin.Params{{Key: "agentName", Value: agentName}}
	return c, w
}

// TestAgentTelegram_RequireAuth keeps the binding behind a login same as every
// other agent runtime endpoint.
func TestAgentTelegram_RequireAuth(t *testing.T) {
	h := &Handler{}
	for name, call := range map[string]func(*gin.Context){
		"bind":   h.BindAgentTelegram,
		"unbind": h.UnbindAgentTelegram,
		"get":    h.GetAgentTelegram,
	} {
		c, w := telegramTestCtx(t, "POST", "/agents/x/telegram", "{}", nil, "x")
		call(c)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want 401", name, w.Code)
		}
	}
}

// TestAgentTelegram_GatewayUnconfigured answers a disabled gateway with 503,
// not a 500 that reads as a broken console.
func TestAgentTelegram_GatewayUnconfigured(t *testing.T) {
	h := &Handler{}
	claims := testAgentClaims()
	for name, call := range map[string]func(*gin.Context){
		"bind":   h.BindAgentTelegram,
		"unbind": h.UnbindAgentTelegram,
		"get":    h.GetAgentTelegram,
	} {
		c, w := telegramTestCtx(t, "POST", "/agents/x/telegram", `{"bot_token":"t"}`, claims, "x")
		call(c)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503: %s", name, w.Code, w.Body.String())
		}
	}
}

// TestBindAgentTelegram_EmptyToken never reaches the gateway: an empty field
// is a validation error, not a proxied request.
func TestBindAgentTelegram_EmptyToken(t *testing.T) {
	h := &Handler{tgGateway: tggatewayclient.New("http://unused.invalid", "")}
	c, w := telegramTestCtx(t, "POST", "/agents/x/telegram", `{"bot_token":""}`, testAgentClaims(), "x")
	h.BindAgentTelegram(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestBindAgentTelegram_UnknownAgent refuses to bind a token to an agent this
// console has never heard of, rather than handing the gateway a made-up
// project id. It is 404, the answer an outsider gets for a real agent.
func TestBindAgentTelegram_UnknownAgent(t *testing.T) {
	pool := testAgentGatePool(t)
	h := &Handler{pool: pool, tgGateway: tggatewayclient.New("http://unused.invalid", "")}

	c, w := telegramTestCtx(t, "POST", "/agents/no-such-agent/telegram", `{"bot_token":"t"}`, testAgentClaims(), "no-such-agent")
	h.BindAgentTelegram(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
}

// TestBindAgentTelegram_InvalidToken surfaces tg-gateway's getMe rejection as
// a field error in the modal, not a silent dead poller.
func TestBindAgentTelegram_InvalidToken(t *testing.T) {
	pool := testAgentGatePool(t)
	agentName := "tg-test-agent-" + uuid.NewString()[:8]
	projectID := seedNamedAgent(t, pool, agentName)

	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer gw.Close()

	h := &Handler{pool: pool, tgGateway: tggatewayclient.New(gw.URL, "")}
	c, w := telegramTestCtx(t, "POST", "/agents/"+agentName+"/telegram", `{"bot_token":"bad"}`, agentRoleClaims(projectID, models.MemberRoleDeveloper), agentName)
	h.BindAgentTelegram(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestBindAgentTelegram_Success resolves the agent's project from
// resource_snapshots and hands the gateway a real project id.
func TestBindAgentTelegram_Success(t *testing.T) {
	pool := testAgentGatePool(t)
	okName := "tg-test-agent-ok-" + uuid.NewString()[:8]
	projectID := seedNamedAgent(t, pool, okName)

	var gotAgent, gotProject string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AgentName string `json:"agent_name"`
			ProjectID string `json:"project_id"`
			BotToken  string `json:"bot_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotAgent, gotProject = body.AgentName, body.ProjectID
		_ = json.NewEncoder(w).Encode(map[string]string{"bot_username": "test_bot"})
	}))
	defer gw.Close()

	h := &Handler{pool: pool, tgGateway: tggatewayclient.New(gw.URL, "")}
	c, w := telegramTestCtx(t, "POST", "/agents/"+okName+"/telegram", `{"bot_token":"good"}`, agentRoleClaims(projectID, models.MemberRoleDeveloper), okName)
	h.BindAgentTelegram(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp struct {
		BotUsername string `json:"bot_username"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.BotUsername != "test_bot" {
		t.Errorf("bot_username = %q, want test_bot", resp.BotUsername)
	}
	if gotAgent != okName {
		t.Errorf("gateway saw agent_name = %q", gotAgent)
	}
	if gotProject != projectID.String() {
		t.Errorf("gateway saw project_id = %q, want %q", gotProject, projectID)
	}
}

// telegramMember seeds an agent of this name and returns the claims of a
// developer of its project, the caller the binding routes let through.
func telegramMember(t *testing.T) (*pgxpool.Pool, *auth.Claims, string) {
	t.Helper()
	pool := testAgentGatePool(t)
	name := "tg-member-" + uuid.NewString()[:8]
	projectID := seedNamedAgent(t, pool, name)
	return pool, agentRoleClaims(projectID, models.MemberRoleDeveloper), name
}

func TestUnbindAgentTelegram_Success(t *testing.T) {
	pool, claims, name := telegramMember(t)
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer gw.Close()

	h := &Handler{pool: pool, tgGateway: tggatewayclient.New(gw.URL, "")}
	c, w := telegramTestCtx(t, "DELETE", "/agents/x/telegram", "", claims, name)
	h.UnbindAgentTelegram(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

func TestUnbindAgentTelegram_GatewayError(t *testing.T) {
	pool, claims, name := telegramMember(t)
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer gw.Close()

	h := &Handler{pool: pool, tgGateway: tggatewayclient.New(gw.URL, "")}
	c, w := telegramTestCtx(t, "DELETE", "/agents/x/telegram", "", claims, name)
	h.UnbindAgentTelegram(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
}

// TestGetAgentTelegram_NotFound reports bound=false rather than 404, so the
// modal treats "never connected" the same as any other clean state.
func TestGetAgentTelegram_NotFound(t *testing.T) {
	pool, claims, name := telegramMember(t)
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer gw.Close()

	h := &Handler{pool: pool, tgGateway: tggatewayclient.New(gw.URL, "")}
	c, w := telegramTestCtx(t, "GET", "/agents/x/telegram", "", claims, name)
	h.GetAgentTelegram(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Bound bool `json:"bound"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Bound {
		t.Errorf("bound = true, want false")
	}
}

func TestGetAgentTelegram_Success(t *testing.T) {
	pool, claims, name := telegramMember(t)
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"bound": true, "bot_username": "test_bot"})
	}))
	defer gw.Close()

	h := &Handler{pool: pool, tgGateway: tggatewayclient.New(gw.URL, "")}
	c, w := telegramTestCtx(t, "GET", "/agents/x/telegram", "", claims, name)
	h.GetAgentTelegram(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Bound       bool   `json:"bound"`
		BotUsername string `json:"bot_username"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Bound || resp.BotUsername != "test_bot" {
		t.Errorf("resp = %+v, want bound=true bot_username=test_bot", resp)
	}
}
