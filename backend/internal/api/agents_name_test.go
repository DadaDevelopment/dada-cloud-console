package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
)

func countAgentRows(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, name string) (ops, snapshots int) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM operations WHERE project_id = $1 AND resource_name = $2`,
		projectID, name).Scan(&ops); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM resource_snapshots WHERE project_id = $1 AND name = $2 AND kind IN ('ManagedAgent', 'Agent')`,
		projectID, name).Scan(&snapshots); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	return ops, snapshots
}

// TestSaveAgent_RefusesANameAnotherProjectHolds is the regression test for the
// cross-tenant agent takeover: every agent lands in the one kagent namespace by
// name, so a second project's claim under a taken name composes a CR that
// fights the owner's, and the by-name routes then lock the owner out. Both a
// console claim and a raw CR from the owner's git count as holding the name.
func TestSaveAgent_RefusesANameAnotherProjectHolds(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}

	claimed := "name-claimed-" + uuid.NewString()[:8]
	seedNamedAgent(t, pool, claimed)
	handWritten := "name-raw-" + uuid.NewString()[:8]
	ownerProject, ownerEnv := seedOptimisticFixture(t, pool)
	seedGitWrittenAgent(t, pool, ownerProject, ownerEnv, handWritten)

	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	for _, name := range []string{claimed, handWritten} {
		c, rec := newCreateCtx(t, `{"name":"`+name+`","prompt":"Be brief."}`, params(projectID, envID), claims)
		h.SaveAgent(c)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409; body = %s", name, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["code"] != "agent_name_taken" || body["error"] != "agent_name_taken" {
			t.Fatalf("%s: body = %s, want code agent_name_taken", name, rec.Body.String())
		}
		if ops, snaps := countAgentRows(t, pool, projectID, name); ops != 0 || snaps != 0 {
			t.Fatalf("%s: a refused save left %d operations and %d snapshots", name, ops, snaps)
		}
	}
}

// TestSaveAgent_OwnNameStillSaves keeps the gate from refusing the owner: a
// create under a free name and an update of one's own agent both queue.
func TestSaveAgent_OwnNameStillSaves(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "name-own-" + uuid.NewString()[:8]

	for _, want := range []string{"CreateAgent", "UpdateAgent"} {
		c, rec := newCreateCtx(t, `{"name":"`+name+`","prompt":"Be brief."}`, params(projectID, envID), claims)
		h.SaveAgent(c)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s: status = %d, want 202; body = %s", want, rec.Code, rec.Body.String())
		}
		var out struct {
			Operation models.Operation `json:"operation"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Operation.Action != want {
			t.Fatalf("action = %q, want %q", out.Operation.Action, want)
		}
	}
}

// TestSaveAgent_ConcurrentCreatesOfOneNameAdmitOne covers the window between the
// check and the optimistic snapshot insert: projects racing to create the same
// free name must end with exactly one holder, not one each.
func TestSaveAgent_ConcurrentCreatesOfOneNameAdmitOne(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	name := "name-race-" + uuid.NewString()[:8]

	const racers = 6
	type racer struct {
		projectID, envID uuid.UUID
		claims           *auth.Claims
	}
	racersList := make([]racer, racers)
	for i := range racersList {
		p, e := seedOptimisticFixture(t, pool)
		racersList[i] = racer{p, e, godClaims(seedUser(t, pool))}
	}

	codes := make([]int, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, r := range racersList {
		c, rec := newCreateCtx(t, `{"name":"`+name+`","prompt":"Be brief."}`, params(r.projectID, r.envID), r.claims)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h.SaveAgent(c)
			codes[i] = rec.Code
		}()
	}
	close(start)
	wg.Wait()

	accepted, conflicts := 0, 0
	for _, code := range codes {
		switch code {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
			conflicts++
		}
	}
	if accepted != 1 || conflicts != racers-1 {
		t.Fatalf("codes = %v, want exactly one 202 and %d 409", codes, racers-1)
	}
	var holders int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(DISTINCT project_id) FROM resource_snapshots WHERE name = $1 AND kind IN ('ManagedAgent', 'Agent')`,
		name).Scan(&holders); err != nil {
		t.Fatalf("count holders: %v", err)
	}
	if holders != 1 {
		t.Fatalf("%d projects hold %s, want 1", holders, name)
	}
}

func validateErrors(t *testing.T, h *Handler, query, body string, claims *auth.Claims) (int, []AgentFieldError) {
	t.Helper()
	path := "/agents/validate"
	if query != "" {
		path += "?project=" + query
	}
	c, w := agentTestContext(t, "POST", path, body, claims)
	h.ValidateAgent(c)
	var out struct {
		Errors []AgentFieldError `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v; body = %s", err, w.Body.String())
	}
	return w.Code, out.Errors
}

func hasFieldError(errs []AgentFieldError, field, substr string) bool {
	for _, e := range errs {
		if e.Field == field && strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

// TestValidateAgent_ReportsATakenNameForTheProject answers the name conflict
// SaveAgent would refuse with 409, next to the name field, for the project the
// draft is for; the caller's own agent of that name is not a conflict.
func TestValidateAgent_ReportsATakenNameForTheProject(t *testing.T) {
	pool := testAgentGatePool(t)
	h := agentTestHandler(nil)
	h.pool = pool

	taken := "name-validate-" + uuid.NewString()[:8]
	seedNamedAgent(t, pool, taken)
	own := "name-validate-own-" + uuid.NewString()[:8]
	projectID := seedNamedAgent(t, pool, own)
	member := agentRoleClaims(projectID, models.MemberRoleDeveloper)

	code, errs := validateErrors(t, h, projectID.String(), `{"name":"`+taken+`","prompt":"hi"}`, member)
	if code != http.StatusBadRequest || !hasFieldError(errs, "name", "already runs on this platform") {
		t.Fatalf("taken name: status = %d, errors = %+v", code, errs)
	}

	code, errs = validateErrors(t, h, projectID.String(), `{"name":"`+own+`","prompt":"hi"}`, member)
	if code != http.StatusOK {
		t.Fatalf("own agent's name: status = %d, errors = %+v", code, errs)
	}
}

// TestValidateAgent_TreatsAnotherProjectsServerAsUnknown: a bare name counts
// only for a platform server or the project's own, the same set the form's tool
// list offers; another project's server is unknown whether or not a project is
// passed, and declaring an own server under its name is the takeover SaveAgent
// refuses.
func TestValidateAgent_TreatsAnotherProjectsServerAsUnknown(t *testing.T) {
	pool := testAgentGatePool(t)
	projectID := seedProjectWithOwner(t, pool, seedUser(t, pool))
	var projectName string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM projects WHERE id = $1`, projectID).Scan(&projectName); err != nil {
		t.Fatalf("read project name: %v", err)
	}
	h := agentTestHandler([]runtime.Object{
		testMCPServer("platform-task-tools", "http://platform/mcp"),
		testProjectMCPServer("own-notion", projectName),
		testProjectMCPServer("neighbour-crm", "someone-else"),
	})
	h.pool = pool
	member := agentRoleClaims(projectID, models.MemberRoleDeveloper)
	draft := func(tools string) string {
		return `{"name":"name-tools-` + uuid.NewString()[:8] + `","prompt":"hi","tools":` + tools + `}`
	}

	code, errs := validateErrors(t, h, projectID.String(), draft(`["platform-task-tools","own-notion"]`), member)
	if code != http.StatusOK {
		t.Fatalf("platform and own servers: status = %d, errors = %+v", code, errs)
	}

	for _, query := range []string{projectID.String(), ""} {
		code, errs = validateErrors(t, h, query, draft(`["neighbour-crm"]`), member)
		if code != http.StatusBadRequest || !hasFieldError(errs, "tools", "no MCP server named neighbour-crm") {
			t.Fatalf("?project=%q: another project's server must be unknown: status = %d, errors = %+v", query, code, errs)
		}
		for _, e := range errs {
			if strings.Contains(e.Message, "another project") {
				t.Fatalf("the answer must not confirm the server belongs to someone: %+v", errs)
			}
		}
	}

	code, errs = validateErrors(t, h, projectID.String(),
		draft(`[{"name":"neighbour-crm","url":"https://crm.example.com/mcp"}]`), member)
	if code != http.StatusBadRequest || !hasFieldError(errs, "tools[0].name", "already runs on this platform") {
		t.Fatalf("declared takeover: status = %d, errors = %+v", code, errs)
	}

	code, errs = validateErrors(t, h, projectID.String(), draft(`["own-notion"]`), testAgentClaims())
	if code != http.StatusBadRequest || !hasFieldError(errs, "tools", "no MCP server named own-notion") {
		t.Fatalf("a project the caller has no role in must not count: status = %d, errors = %+v", code, errs)
	}
}
