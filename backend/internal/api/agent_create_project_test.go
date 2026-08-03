package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
)

func testAgentGatePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping agent-gate DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newCreateProjectCtx(body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, rec
}

// TestCreateProject_AgentDenied is the regression test for test-project sprawl:
// autonomous sessions minting a throwaway project per run left 24 junk projects
// in org dada, each holding live namespaces and volumes. An identity in /agents
// must be refused before any row is written, whichever org it aims at — including
// the personal org its username would otherwise imply.
func TestCreateProject_AgentDenied(t *testing.T) {
	pool := testAgentGatePool(t)
	h := &Handler{pool: pool}
	actor := seedUser(t, pool)

	cases := []struct {
		name string
		body string
	}{
		{"explicit shared org", `{"slug":"agent-sprawl-shared","org_id":"dada"}`},
		{"implicit personal org", `{"slug":"agent-sprawl-personal"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newCreateProjectCtx(tc.body)
			auth.SetClaims(c, &auth.Claims{
				UserID:   actor,
				Username: "service-account-dada-routine-svc",
				Groups: []string{
					"/agents",
					"/orgs/dada/projects/agent-sandbox/Owner",
				},
			})

			h.CreateProject(c)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d: %s", rec.Code, rec.Body.String())
			}

			var slug string
			err := pool.QueryRow(context.Background(),
				`SELECT name FROM projects WHERE name LIKE 'agent-sprawl-%'`).Scan(&slug)
			if err == nil {
				t.Fatalf("project %q was created despite the 403 — the gate must refuse before insert", slug)
			}

			var reason string
			if err := pool.QueryRow(context.Background(),
				`SELECT metadata->>'reason' FROM audit_events
				 WHERE action = 'CreateProject' AND actor_id = $1
				 ORDER BY created_at DESC LIMIT 1`, actor,
			).Scan(&reason); err != nil {
				t.Fatalf("refusal must leave an audit row: %v", err)
			}
			if reason != "agent_project_creation_denied" {
				t.Errorf("audit reason = %q, want agent_project_creation_denied", reason)
			}
		})
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_id = $1`, actor)
	})
}

// TestCreateProject_NonAgentStillCreates guards the gate's blast radius: real
// self-service signup into a personal org must keep working.
func TestCreateProject_NonAgentStillCreates(t *testing.T) {
	pool := testAgentGatePool(t)
	h := &Handler{pool: pool}
	actor := seedUser(t, pool)
	slug := "agent-gate-ok-" + uuid.NewString()[:8]

	c, rec := newCreateProjectCtx(`{"slug":"` + slug + `"}`)
	auth.SetClaims(c, &auth.Claims{UserID: actor, Username: "realperson-" + uuid.NewString()[:8]})

	h.CreateProject(c)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE actor_id = $1`, actor)
		_, _ = pool.Exec(context.Background(), `DELETE FROM environments WHERE project_id IN (SELECT id FROM projects WHERE name = $1)`, slug)
		_, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE name = $1`, slug)
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["project_id"] == nil {
		t.Errorf("response has no project_id: %s", rec.Body.String())
	}
}
