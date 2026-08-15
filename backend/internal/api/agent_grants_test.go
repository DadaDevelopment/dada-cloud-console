package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAgentGrantableRole pins the roles a grant may carry. Owner/Admin manage
// membership, so an automation holding one could widen its own access — the
// exact property this mechanism exists to deny.
func TestAgentGrantableRole(t *testing.T) {
	cases := []struct {
		in   string
		want models.MemberRole
		ok   bool
	}{
		{"", models.MemberRoleDeveloper, true},
		{"Developer", models.MemberRoleDeveloper, true},
		{"ReadOnly", models.MemberRoleReadOnly, true},
		{"Admin", "", false},
		{"Owner", "", false},
		{"root", "", false},
	}
	for _, tc := range cases {
		got, ok := agentGrantableRole(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("agentGrantableRole(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func agentGrantPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping agent-grant DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedGrantProject inserts a project owned by org "dada" for grant tests.
func seedGrantProject(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID) uuid.UUID {
	t.Helper()
	name := "agent-grant-" + uuid.NewString()[:8]
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, display_name, org_id, owner_id)
		 VALUES ($1, $1, 'dada', $2) RETURNING id`, name, owner,
	).Scan(&id); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, id) })
	return id
}

// agentClaims is the identity an automated run authenticates as: in /agents, so
// it has no personal org and gets no org cascade, and carrying no project group.
func agentClaims(userID uuid.UUID, username string) *auth.Claims {
	return &auth.Claims{UserID: userID, Username: username, Groups: []string{"/agents"}}
}

// TestAgentGrant_ScopesExactlyOneProject is the whole point of the mechanism:
// a granted agent reaches the project it was lent and nothing else, and loses it
// again the moment the grant is revoked. Without a grant the answer is 404 (not
// 403), so a machine identity cannot enumerate what it may not touch.
func TestAgentGrant_ScopesExactlyOneProject(t *testing.T) {
	pool := agentGrantPool(t)
	h := &Handler{pool: pool}
	ctx := context.Background()

	admin := seedUser(t, pool)
	agent := seedUser(t, pool)
	var agentName string
	if err := pool.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, agent).Scan(&agentName); err != nil {
		t.Fatalf("read agent username: %v", err)
	}

	projectA := seedGrantProject(t, pool, admin)
	projectB := seedGrantProject(t, pool, admin)
	claims := agentClaims(agent, agentName)

	if _, err := h.effectiveRole(ctx, claims, projectA); err == nil {
		t.Fatal("agent had access to project A before any grant")
	}

	c, rec := newCreateCtx(t, `{"agent_username":"`+agentName+`","run_ref":"run_test"}`,
		gin.Params{{Key: "projectId", Value: projectA.String()}}, godClaims(admin))
	h.CreateAgentGrant(c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateAgentGrant status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created agentGrantResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	t.Cleanup(func() { dropSeededAudit(pool, "AgentGrant", agentName) })
	t.Cleanup(func() { dropSeededAudit(pool, "AgentGrant", created.ID.String()) })

	role, err := h.effectiveRole(ctx, claims, projectA)
	if err != nil {
		t.Fatalf("granted project unreachable: %v", err)
	}
	if role != models.MemberRoleDeveloper {
		t.Fatalf("granted role = %q, want Developer", role)
	}

	if _, err := h.effectiveRole(ctx, claims, projectB); err == nil {
		t.Fatal("grant on project A leaked access to project B")
	}

	rec = httptest.NewRecorder()
	dc, _ := gin.CreateTestContext(rec)
	dc.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	dc.Params = gin.Params{
		{Key: "projectId", Value: projectA.String()},
		{Key: "grantId", Value: created.ID.String()},
	}
	auth.SetClaims(dc, godClaims(admin))
	h.RevokeAgentGrant(dc)
	if dc.Writer.Status() != http.StatusNoContent {
		t.Fatalf("RevokeAgentGrant status = %d, body = %s", dc.Writer.Status(), rec.Body.String())
	}

	if _, err := h.effectiveRole(ctx, claims, projectA); err == nil {
		t.Fatal("access survived revocation")
	}
}

// TestAgentGrant_ExpiryEndsAccess covers the run that dies without ever calling
// finish: the clock ends the grant even though nothing revoked it.
func TestAgentGrant_ExpiryEndsAccess(t *testing.T) {
	pool := agentGrantPool(t)
	h := &Handler{pool: pool}
	ctx := context.Background()

	admin := seedUser(t, pool)
	agent := seedUser(t, pool)
	project := seedGrantProject(t, pool, admin)

	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_project_grants
		            (project_id, agent_user_id, role, granted_by, expires_at)
		     VALUES ($1, $2, 'Developer', $3, now() - interval '1 minute')`,
		project, agent, admin); err != nil {
		t.Fatalf("seed expired grant: %v", err)
	}

	if _, err := h.effectiveRole(ctx, agentClaims(agent, "agent"), project); err == nil {
		t.Fatal("expired grant still granted access")
	}
}

// TestAgentGrant_InertForHumans pins the containment property: the table is read
// only for callers in /agents, so a row naming a person is not a second way to
// elevate them outside member management.
func TestAgentGrant_InertForHumans(t *testing.T) {
	pool := agentGrantPool(t)
	h := &Handler{pool: pool}
	ctx := context.Background()

	admin := seedUser(t, pool)
	human := seedUser(t, pool)
	project := seedGrantProject(t, pool, admin)

	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_project_grants
		            (project_id, agent_user_id, role, granted_by, expires_at)
		     VALUES ($1, $2, 'Developer', $3, now() + interval '1 hour')`,
		project, human, admin); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	claims := &auth.Claims{UserID: human, Username: "someone"}
	if _, err := h.effectiveRole(ctx, claims, project); err == nil {
		t.Fatal("a grant elevated a human identity")
	}
}

// TestAgentGrant_AgentCannotGrant closes the self-widening path: an agent that
// somehow holds an admin role on a project still may not hand access to itself
// or to another automation.
func TestAgentGrant_AgentCannotGrant(t *testing.T) {
	pool := agentGrantPool(t)
	h := &Handler{pool: pool}

	admin := seedUser(t, pool)
	agent := seedUser(t, pool)
	project := seedGrantProject(t, pool, admin)

	claims := agentClaims(agent, "service-account-dada-agent")
	claims.Groups = append(claims.Groups, "/orgs/dada/projects/"+project.String()+"/Admin")

	c, rec := newCreateCtx(t, `{"agent_username":"whoever"}`,
		gin.Params{{Key: "projectId", Value: project.String()}}, claims)
	h.CreateAgentGrant(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("agent-issued grant status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
}
