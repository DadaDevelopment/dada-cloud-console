package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/tggatewayclient"
)

// agentRoleClaims is a human holding exactly one explicit project role.
func agentRoleClaims(projectID uuid.UUID, role models.MemberRole) *auth.Claims {
	return &auth.Claims{
		UserID:   uuid.New(),
		Username: "agent-gate-" + uuid.NewString()[:8],
		Groups:   []string{"/orgs/agent-gate-test/projects/" + projectID.String() + "/" + string(role)},
	}
}

// seedNamedAgent seeds a project holding one ManagedAgent snapshot of this
// name, the row the agent-by-name routes resolve the owning project from.
func seedNamedAgent(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	projectID := seedProjectWithOwner(t, pool, seedUser(t, pool))
	addAgentSnapshot(t, pool, projectID, name)
	return projectID
}

func addAgentSnapshot(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, name string) {
	t.Helper()
	envID := seedEnvironment(t, pool, projectID)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase)
		 VALUES ($1, $2, 'ManagedAgent', $3, 'Pending')`,
		projectID, envID, name); err != nil {
		t.Fatalf("seed managed agent snapshot: %v", err)
	}
}

// seedAgentIdentityGrant returns the claims of a /agents identity holding a
// grant of this role on the project, live or already expired.
func seedAgentIdentityGrant(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, role models.MemberRole, live bool) *auth.Claims {
	t.Helper()
	agentID := seedUser(t, pool)
	var username string
	if err := pool.QueryRow(context.Background(), `SELECT username FROM users WHERE id = $1`, agentID).Scan(&username); err != nil {
		t.Fatalf("read agent username: %v", err)
	}
	expires := time.Now().Add(time.Hour)
	if !live {
		expires = time.Now().Add(-time.Minute)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO agent_project_grants (project_id, agent_user_id, role, run_ref, expires_at)
		 VALUES ($1, $2, $3, 'run_agent_gate_test', $4)`,
		projectID, agentID, string(role), expires); err != nil {
		t.Fatalf("seed agent grant: %v", err)
	}
	return agentClaims(agentID, username)
}

// agentGateSideEffects counts what reached the gateway and the agent, so a
// refused call is proven to have touched neither.
type agentGateSideEffects struct {
	mu    sync.Mutex
	calls []string
}

func (s *agentGateSideEffects) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *agentGateSideEffects) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.calls
	s.calls = nil
	return out
}

type recordingA2A struct {
	fakeA2AClient
	effects *agentGateSideEffects
}

func (r *recordingA2A) Send(ctx context.Context, agentName, text string) (string, error) {
	r.effects.record("a2a " + agentName)
	return "ok", nil
}

func agentGateHandler(t *testing.T, pool *pgxpool.Pool) (*Handler, *agentGateSideEffects) {
	t.Helper()
	effects := &agentGateSideEffects{}
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		effects.record("gateway " + r.Method)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"bound": true, "bot_username": "gate_bot"})
	}))
	t.Cleanup(gw.Close)
	h := agentTestHandler(nil)
	h.pool = pool
	h.tgGateway = tggatewayclient.New(gw.URL, "")
	h.a2a = &recordingA2A{effects: effects}
	return h, effects
}

type agentGateRoute struct {
	name   string
	method string
	body   string
	write  bool
	call   func(h *Handler) func(*gin.Context)
}

var agentGateRoutes = []agentGateRoute{
	{"state", http.MethodGet, "", false, func(h *Handler) func(*gin.Context) { return h.GetAgentState }},
	{"get telegram", http.MethodGet, "", false, func(h *Handler) func(*gin.Context) { return h.GetAgentTelegram }},
	{"bind telegram", http.MethodPost, `{"bot_token":"123:abc"}`, true, func(h *Handler) func(*gin.Context) { return h.BindAgentTelegram }},
	{"unbind telegram", http.MethodDelete, "", true, func(h *Handler) func(*gin.Context) { return h.UnbindAgentTelegram }},
	{"message", http.MethodPost, `{"text":"hi"}`, true, func(h *Handler) func(*gin.Context) { return h.SendAgentMessage }},
}

func callAgentRoute(t *testing.T, h *Handler, route agentGateRoute, agentName string, claims *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	c, w := telegramTestCtx(t, route.method, "/agents/"+agentName, route.body, claims, agentName)
	route.call(h)(c)
	return w
}

// TestAgentByName_ProjectGate is the regression test for the agent routes that
// carry only a name: any logged-in user could read another tenant's agent,
// talk to it, and bind or unbind its Telegram bot. The gate is the owning
// project's role, human or /agents grant alike; an outsider gets 404 (the
// agent's existence is not confirmed) and a reader gets 403 on writes, and a
// refused call never reaches the gateway or the agent.
func TestAgentByName_ProjectGate(t *testing.T) {
	pool := testAgentGatePool(t)
	h, effects := agentGateHandler(t, pool)

	agentName := "gate-agent-" + uuid.NewString()[:8]
	projectID := seedNamedAgent(t, pool, agentName)

	type caller struct {
		name   string
		claims *auth.Claims
		read   int
		write  int
	}
	callers := []caller{
		{"outsider", notAMemberClaims(uuid.New()), http.StatusNotFound, http.StatusNotFound},
		{"read-only member", agentRoleClaims(projectID, models.MemberRoleReadOnly), http.StatusOK, http.StatusForbidden},
		{"developer member", agentRoleClaims(projectID, models.MemberRoleDeveloper), http.StatusOK, http.StatusOK},
		{"platform analyst", &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-analysts"}}, http.StatusOK, http.StatusForbidden},
		{"agent without grant", agentClaims(seedUser(t, pool), "agent-nogrant-"+uuid.NewString()[:8]), http.StatusNotFound, http.StatusNotFound},
		{"agent with expired grant", seedAgentIdentityGrant(t, pool, projectID, models.MemberRoleDeveloper, false), http.StatusNotFound, http.StatusNotFound},
		{"agent with read-only grant", seedAgentIdentityGrant(t, pool, projectID, models.MemberRoleReadOnly, true), http.StatusOK, http.StatusForbidden},
		{"agent with developer grant", seedAgentIdentityGrant(t, pool, projectID, models.MemberRoleDeveloper, true), http.StatusOK, http.StatusOK},
	}

	for _, who := range callers {
		for _, route := range agentGateRoutes {
			t.Run(who.name+"/"+route.name, func(t *testing.T) {
				effects.take()
				want := who.read
				if route.write {
					want = who.write
				}
				w := callAgentRoute(t, h, route, agentName, who.claims)
				if w.Code != want {
					t.Fatalf("status = %d, want %d: %s", w.Code, want, w.Body.String())
				}
				if want != http.StatusOK {
					if got := effects.take(); len(got) != 0 {
						t.Fatalf("a refused call reached %v", got)
					}
				}
			})
		}
	}
}

// TestAgentByName_SameNameInOwnProjectDoesNotUnlockAnother: agent names are
// not unique across projects in resource_snapshots, so an owner of one project
// who writes a claim under a victim agent's name must still be refused the
// victim's agent rather than authorised by their own row.
func TestAgentByName_SameNameInOwnProjectDoesNotUnlockAnother(t *testing.T) {
	pool := testAgentGatePool(t)
	h, effects := agentGateHandler(t, pool)

	agentName := "gate-squat-" + uuid.NewString()[:8]
	seedNamedAgent(t, pool, agentName)
	squatterProject := seedNamedAgent(t, pool, agentName)
	squatter := agentRoleClaims(squatterProject, models.MemberRoleOwner)

	for _, route := range agentGateRoutes {
		effects.take()
		w := callAgentRoute(t, h, route, agentName, squatter)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404: %s", route.name, w.Code, w.Body.String())
		}
		if got := effects.take(); len(got) != 0 {
			t.Errorf("%s: squatter reached %v", route.name, got)
		}
	}
}

// TestAgentByName_UnknownName answers 404 for a name no project holds, the
// same answer an outsider gets, so the two cannot be told apart. A platform
// admin still reads the runtime state of an agent with no snapshot, but
// nothing is bound to or sent to an agent no project owns.
func TestAgentByName_UnknownName(t *testing.T) {
	pool := testAgentGatePool(t)
	h, effects := agentGateHandler(t, pool)
	agentName := "gate-unknown-" + uuid.NewString()[:8]

	for _, route := range agentGateRoutes {
		effects.take()
		w := callAgentRoute(t, h, route, agentName, testAgentClaims())
		if w.Code != http.StatusNotFound {
			t.Errorf("tenant %s: status = %d, want 404: %s", route.name, w.Code, w.Body.String())
		}
	}

	admin := &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}}
	want := map[string]int{
		"state":           http.StatusOK,
		"get telegram":    http.StatusOK,
		"bind telegram":   http.StatusNotFound,
		"unbind telegram": http.StatusOK,
		"message":         http.StatusNotFound,
	}
	for _, route := range agentGateRoutes {
		w := callAgentRoute(t, h, route, agentName, admin)
		if w.Code != want[route.name] {
			t.Errorf("admin %s: status = %d, want %d: %s", route.name, w.Code, want[route.name], w.Body.String())
		}
	}
	if got := effects.take(); len(got) != 2 {
		t.Errorf("admin should reach the gateway for get and unbind only, got %v", got)
	}
}

// TestGetAgentState_OffClusterAnswers503 keeps the absent-runtime answer for a
// caller who passes the project gate.
func TestGetAgentState_OffClusterAnswers503(t *testing.T) {
	pool := testAgentGatePool(t)
	agentName := "gate-offcluster-" + uuid.NewString()[:8]
	projectID := seedNamedAgent(t, pool, agentName)

	h := &Handler{pool: pool}
	c, w := agentTestContext(t, "GET", "/agents/"+agentName+"/state", "", agentRoleClaims(projectID, models.MemberRoleReadOnly))
	c.Params = gin.Params{{Key: "agentName", Value: agentName}}
	h.GetAgentState(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
}
