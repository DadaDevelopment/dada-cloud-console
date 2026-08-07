package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// auditFilterFixture is the trail the exclusion tests read: two actors sharing
// a unique email token (so the existing ?user= substring filter scopes the
// assertions to this test's rows in a shared database) and two actions, one of
// which is the chatty kind an operator wants to untick.
type auditFilterFixture struct {
	pool      *pgxpool.Pool
	token     string
	emailA    string
	emailB    string
	projectID uuid.UUID
}

func seedAuditFilterFixture(t *testing.T) auditFilterFixture {
	t.Helper()
	pool := testOptimisticPool(t)
	ctx := context.Background()

	token := "auditflt" + uuid.NewString()[:8]
	f := auditFilterFixture{
		pool:   pool,
		token:  token,
		emailA: token + "-a@example.com",
		emailB: token + "-b@example.com",
	}

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		token,
	).Scan(&f.projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, f.projectID) })

	seedActor := func(email string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, display_name)
			 VALUES ($1, $2, 'x', $1) RETURNING id`, email, email,
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", email, err)
		}
		t.Cleanup(func() { dropSeededUser(pool, id) })
		return id
	}
	actorA := seedActor(f.emailA)
	actorB := seedActor(f.emailB)

	events := []struct {
		actor  uuid.UUID
		action string
	}{
		{actorA, "ViewProject"},
		{actorA, "CreateApp"},
		{actorB, "ViewProject"},
	}
	for _, e := range events {
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name)
			 VALUES ($1, $2, $3, 'Project', $4)`,
			e.actor, f.projectID, e.action, token,
		); err != nil {
			t.Fatalf("seed audit event %s: %v", e.action, err)
		}
	}
	return f
}

// callAuditEndpoint invokes an admin audit handler with platform-admin claims
// and returns the decoded JSON body.
func callAuditEndpoint(t *testing.T, pool *pgxpool.Pool, handler gin.HandlerFunc, query string) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit?"+query, nil)
	auth.SetClaims(c, &auth.Claims{
		UserID: uuid.New(),
		Email:  "gate@dada-tuda.ru",
		Groups: []string{"/platform-admins"},
	})

	handler(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func auditActionsOf(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["events"].([]any)
	if !ok {
		t.Fatalf("no events array in %v", body)
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		row, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("event is not an object: %v", r)
		}
		out = append(out, row["action"].(string)+"|"+row["actor_email"].(string))
	}
	return out
}

// TestListAuditEvents_ExcludeFilters pins the checkbox filters behind the audit
// viewer. Two actions carried 46% of a week's trail on prod (ViewProject 319,
// SessionStart 244 of 1212 rows [live psql, 7d]) and drowned everything an
// operator was actually reading, so the dashboard needs to hide a chatty
// action or a chatty actor without also hiding the rest.
//
// The parameters are exclusions rather than inclusions on purpose: unticking
// two boxes must not freeze the view to the actions that existed when the box
// was ticked. An empty parameter must therefore change nothing at all.
func TestListAuditEvents_ExcludeFilters(t *testing.T) {
	f := seedAuditFilterFixture(t)
	h := &Handler{pool: f.pool}

	base := "user=" + f.token

	all := auditActionsOf(t, callAuditEndpoint(t, f.pool, h.ListAuditEvents, base))
	if len(all) != 3 {
		t.Fatalf("unfiltered rows = %v, want the 3 seeded events", all)
	}

	noViews := auditActionsOf(t, callAuditEndpoint(t, f.pool, h.ListAuditEvents, base+"&exclude_action=ViewProject"))
	if len(noViews) != 1 || noViews[0] != "CreateApp|"+f.emailA {
		t.Errorf("unticking an action left %v, want only CreateApp by %s", noViews, f.emailA)
	}

	noB := auditActionsOf(t, callAuditEndpoint(t, f.pool, h.ListAuditEvents, base+"&exclude_user="+f.emailB))
	if len(noB) != 2 {
		t.Errorf("unticking an actor left %v, want the 2 events of %s", noB, f.emailA)
	}
	for _, row := range noB {
		if row == "ViewProject|"+f.emailB {
			t.Errorf("row of the unticked actor %s survived: %v", f.emailB, noB)
		}
	}

	both := auditActionsOf(t, callAuditEndpoint(t, f.pool, h.ListAuditEvents,
		base+"&exclude_action=ViewProject,SessionStart&exclude_user="+f.emailB))
	if len(both) != 1 || both[0] != "CreateApp|"+f.emailA {
		t.Errorf("combined exclusions left %v, want only CreateApp by %s", both, f.emailA)
	}

	empty := auditActionsOf(t, callAuditEndpoint(t, f.pool, h.ListAuditEvents, base+"&exclude_action=&exclude_user=%20,%20"))
	if len(empty) != 3 {
		t.Errorf("blank exclusions changed the view to %v — nothing unticked must mean nothing hidden", empty)
	}
}

// TestListAuditFacets_ReportsActorsAndActions pins the list the checkboxes are
// built from. The dashboard pages 50 rows at a time, so the set of actors and
// actions to offer cannot be derived from the current page — it has to come
// from the whole trail, with counts so the operator can see which box is worth
// unticking.
func TestListAuditFacets_ReportsActorsAndActions(t *testing.T) {
	f := seedAuditFilterFixture(t)
	h := &Handler{pool: f.pool}

	body := callAuditEndpoint(t, f.pool, h.ListAuditFacets, "")

	actors, ok := body["actors"].([]any)
	if !ok {
		t.Fatalf("no actors array in %v", body)
	}
	counts := map[string]float64{}
	kinds := map[string]string{}
	for _, r := range actors {
		row := r.(map[string]any)
		email := row["email"].(string)
		counts[email] = row["count"].(float64)
		kinds[email] = row["account_kind"].(string)
	}
	if counts[f.emailA] != 2 {
		t.Errorf("actor facet for %s = %v events, want 2", f.emailA, counts[f.emailA])
	}
	if counts[f.emailB] != 1 {
		t.Errorf("actor facet for %s = %v events, want 1", f.emailB, counts[f.emailB])
	}
	if kinds[f.emailA] != "customer" {
		t.Errorf("actor facet cohort for %s = %q, want customer — the badge is what tells a probe from a user", f.emailA, kinds[f.emailA])
	}

	actions, ok := body["actions"].([]any)
	if !ok {
		t.Fatalf("no actions array in %v", body)
	}
	seen := map[string]bool{}
	for _, r := range actions {
		seen[r.(map[string]any)["action"].(string)] = true
	}
	for _, want := range []string{"ViewProject", "CreateApp"} {
		if !seen[want] {
			t.Errorf("action %q missing from the facet list — it cannot be unticked if it is not offered", want)
		}
	}
}
