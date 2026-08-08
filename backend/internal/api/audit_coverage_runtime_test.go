package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type auditCoverageBody struct {
	Days         int                `json:"days"`
	Gaps         []auditCoverageGap `json:"gaps"`
	TotalMissing int                `json:"total_missing"`
}

// callAuditCoverage runs the handler as a platform admin and decodes the payload.
func callAuditCoverage(t *testing.T, pool *pgxpool.Pool) auditCoverageBody {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/?days=30", nil)
	auth.SetClaims(c, &auth.Claims{Groups: []string{"/platform-admins"}})

	h := &Handler{pool: pool}
	h.GetAuditCoverage(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body auditCoverageBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return body
}

// findGap returns the reported gap for an action, or nil.
func findGap(body auditCoverageBody, action string) *auditCoverageGap {
	for i := range body.Gaps {
		if body.Gaps[i].Action == action {
			return &body.Gaps[i]
		}
	}
	return nil
}

// TestAuditCoverage_ReportsOperationWithNoAuditRow is the runtime half of the
// audit-coverage ratchet. TestEveryMutatingHandlerAudits reads handler bodies,
// so it can only see actions a handler starts; an operation an agent enqueues as
// a follow-up has no handler to inspect and stayed invisible to it. That is
// where the trail actually went quiet on prod -- DeployStack and
// AttachDefaultDomain reached audit_events only when they FAILED.
func TestAuditCoverage_ReportsOperationWithNoAuditRow(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	action := "CoverageProbe" + suffix
	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"audit-coverage-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	if _, err := pool.Exec(ctx, `
		INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		VALUES ('00000000-0000-0000-0000-000000000000', $1, $2, 'App', 'silent', 'Ready', '{}'::jsonb)`,
		projectID, action,
	); err != nil {
		t.Fatalf("seed unaudited operation: %v", err)
	}

	gap := findGap(callAuditCoverage(t, pool), action)
	if gap == nil {
		t.Fatalf("an operation finished with nothing in audit_events and the coverage report stayed silent about %s", action)
	}
	if gap.Operations != 1 || gap.Audited != 0 || gap.Missing != 1 {
		t.Fatalf("gap = %+v, want 1 operation / 0 audited / 1 missing", *gap)
	}
}

// TestAuditCoverage_JoinsOnOperationIDNotAction pins the join that makes the
// measure trustworthy. Counting the two tables by action name lies in both
// directions: on prod ResizeApp read as 24 operations against zero audit rows
// while being fully covered, because the user path is audited as
// UpdateAppProfile and the autoscaler's as AutoscaleApp.
func TestAuditCoverage_JoinsOnOperationIDNotAction(t *testing.T) {
	pool := testOptimisticPool(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	opAction := "CoverageOp" + suffix
	auditAction := "CoverageAudit" + suffix
	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"audit-coverage-join-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	var opID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		VALUES ('00000000-0000-0000-0000-000000000000', $1, $2, 'App', 'renamed', 'Ready', '{}'::jsonb)
		RETURNING id`, projectID, opAction,
	).Scan(&opID); err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, outcome, metadata)
		VALUES ('00000000-0000-0000-0000-000000000000', $1, $2, $3, 'App', 'renamed', 'success', '{}'::jsonb)`,
		projectID, opID, auditAction,
	); err != nil {
		t.Fatalf("seed audit row under a different action name: %v", err)
	}

	body := callAuditCoverage(t, pool)
	if gap := findGap(body, opAction); gap != nil {
		t.Fatalf("%s is audited under the name %s, but the report called it a gap (%+v) — the join must be on operation_id",
			opAction, auditAction, *gap)
	}
}

// TestAuditCoverage_ForbidsNonAdmin keeps the report behind the same gate as the
// rest of the admin dashboards: it names actions, projects and volumes of user
// activity.
func TestAuditCoverage_ForbidsNonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	auth.SetClaims(c, &auth.Claims{Groups: []string{"/orgs/dada/Owner"}})

	h := &Handler{}
	h.GetAuditCoverage(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a caller outside /platform-admins and /platform-analysts", rec.Code)
	}
}
