package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// waitForAuditRow polls for a row written off the request's hot path.
//
// recordAuditAsync deliberately outlives the response, so asserting straight
// after the handler returns would race it and fail on a slow machine only.
func waitForAuditRow(t *testing.T, pool *pgxpool.Pool, actorID uuid.UUID, action string) (resourceName string, metadata map[string]any) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := pool.QueryRow(context.Background(),
			`SELECT resource_name, metadata FROM audit_events
			 WHERE actor_id = $1 AND action = $2 ORDER BY created_at DESC LIMIT 1`,
			actorID, action,
		).Scan(&resourceName, &metadata)
		if err == nil {
			return resourceName, metadata
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s row for actor %s within 5s: %v", action, actorID, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func newResolveCtx(projectID uuid.UUID, query string, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	path := "/api/v1/projects/" + projectID.String() + "/solutions/resolve?q=" + query
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}
	auth.SetClaims(c, claims)
	return c, rec
}

// TestResolveSolution_RecordsWhatTheUserAskedFor closes the blind spot at the
// very top of the activation funnel.
//
// The empty-project screen is one field: the newcomer types what they want to
// run, and either finds it or gives up. Only the install used to leave a row,
// so "typed something, got nothing, left" -- the most valuable terminal action
// there is -- was invisible in the path graph. The row must carry the query and
// the candidate count, because a resolve returning zero is the answer.
func TestResolveSolution_RecordsWhatTheUserAskedFor(t *testing.T) {
	pool := testInstallPool(t)
	projectID, _, userID := seedInstallProject(t, pool, "acme", "k8s")
	t.Cleanup(func() { dropSeededAudit(pool, "Solution", "excalidraw") })

	h := newInstallHandler(pool)
	claims := &auth.Claims{UserID: userID, Groups: []string{"/platform-admins"}}
	c, rec := newResolveCtx(projectID, "excalidraw", claims)
	h.ResolveSolution(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}

	name, meta := waitForAuditRow(t, pool, userID, "ResolveSolution")
	if name != "excalidraw" {
		t.Fatalf("resource_name = %q, want the query the user typed", name)
	}
	if meta["query"] != "excalidraw" {
		t.Fatalf("metadata query = %v, want excalidraw", meta["query"])
	}
	candidates, ok := meta["candidates"].(float64)
	if !ok {
		t.Fatalf("metadata candidates missing: %v", meta)
	}
	if candidates < 1 {
		t.Fatalf("candidates = %v: a catalog hit must not read as a dead end", candidates)
	}
}

// TestResolveSolution_RecordsTheDeadEnd is the half that matters for the
// funnel: a query nothing matches still leaves a row, saying so.
func TestResolveSolution_RecordsTheDeadEnd(t *testing.T) {
	pool := testInstallPool(t)
	projectID, _, userID := seedInstallProject(t, pool, "acme", "k8s")
	t.Cleanup(func() { dropSeededAudit(pool, "Solution", "zzz-nothing-matches-this") })

	h := newInstallHandler(pool)
	claims := &auth.Claims{UserID: userID, Groups: []string{"/platform-admins"}}
	c, rec := newResolveCtx(projectID, "zzz-nothing-matches-this", claims)
	h.ResolveSolution(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}

	_, meta := waitForAuditRow(t, pool, userID, "ResolveSolution")
	if meta["candidates"] != float64(0) {
		t.Fatalf("candidates = %v, want 0 for a query nothing matches", meta["candidates"])
	}
}

// TestResolveSolution_ForbiddenLeavesNoRow keeps the funnel counts honest: a
// caller who never got an answer never took the step.
func TestResolveSolution_ForbiddenLeavesNoRow(t *testing.T) {
	pool := testInstallPool(t)
	projectID, _, userID := seedInstallProject(t, pool, "acme", "k8s")

	h := newInstallHandler(pool)
	claims := &auth.Claims{UserID: userID, Groups: []string{"/orgs/acme/projects/" + projectID.String() + "/ReadOnly"}}
	c, rec := newResolveCtx(projectID, "excalidraw", claims)
	h.ResolveSolution(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s want 403", rec.Code, rec.Body.String())
	}

	time.Sleep(200 * time.Millisecond)
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE actor_id = $1 AND action = 'ResolveSolution'`,
		userID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("a rejected resolve left %d rows", n)
	}
}

// TestAgentChatUserMessageAudit_RecordsTheTurnNotTheText makes the assistant a
// visible step in the path graph while keeping the transcript where it belongs.
//
// audit_events gets the shape of the turn -- who, when, which session, how long
// the message was -- and the session id joins back to agent_chat_messages for
// the content, mirroring the redaction the approve/decline row already applies.
func TestAgentChatUserMessageAudit_RecordsTheTurnNotTheText(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "k8s")
	sessionID := uuid.New()
	t.Cleanup(func() { dropSeededAudit(pool, "AgentChat", sessionID.String()) })

	h := &Handler{pool: pool}
	h.agentChatRecordUserMessageAudit(userID, &projectID, &envID, sessionID, "почему мой апп упал?", "my-app")

	name, meta := waitForAuditRow(t, pool, userID, "AgentChatUserMessage")
	if name != sessionID.String() {
		t.Fatalf("resource_name = %q, want the session id so the transcript is joinable", name)
	}
	if meta["chars"] != float64(len("почему мой апп упал?")) {
		t.Fatalf("chars = %v", meta["chars"])
	}
	if meta["has_app"] != true || meta["has_project"] != true {
		t.Fatalf("context flags lost: %v", meta)
	}
	for _, key := range []string{"message", "text", "content"} {
		if _, found := meta[key]; found {
			t.Fatalf("the transcript must not be copied into audit metadata, found %q", key)
		}
	}
}
