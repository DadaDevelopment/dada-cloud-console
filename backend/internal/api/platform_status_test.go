package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// closedTestPool returns a pgxpool.Pool that is syntactically valid (so
// pgxpool.New never errors and never dials anything) but already closed, so
// every query against it fails synchronously with "closed pool". This is what
// lets TestGetPlatformStatus_ComponentErrorReturnsUnknownNot500 exercise the
// query-error path for all five components deterministically, without a real
// database connection.
func closedTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/db")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	pool.Close()
	return pool
}

// TestGetPlatformStatus_RequiresAuth pins that an unauthenticated request is
// rejected before any component query runs: platform/status is readable by
// every logged-in user, never an anonymous caller.
func TestGetPlatformStatus_RequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/platform/status", nil)

	h.GetPlatformStatus(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// TestGetPlatformStatus_ComponentErrorReturnsUnknownNot500 proves the
// contract clause that matters most for this endpoint: a component whose
// query fails must never turn the whole request into a 500 the assistant has
// to guess about. A Handler with a nil pool makes every component query
// panic-free-fail, so this exercises the failure path deterministically for
// all five components without needing a real database at all.
func TestGetPlatformStatus_ComponentErrorReturnsUnknownNot500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{pool: closedTestPool(t)}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/platform/status", nil)
	auth.SetClaims(c, &auth.Claims{Username: "any-authenticated-user"})

	h.GetPlatformStatus(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (a query error must still be HTTP 200)", rec.Code, http.StatusOK)
	}

	var resp platformStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Components) != 5 {
		t.Fatalf("len(Components) = %d, want 5", len(resp.Components))
	}
	for _, comp := range resp.Components {
		if comp.Status != platformStatusUnknown {
			t.Errorf("component %q status = %q, want %q when every query errors", comp.Name, comp.Status, platformStatusUnknown)
		}
		if comp.Detail == "" {
			t.Errorf("component %q has an empty Detail, want a human-readable reason for the failure", comp.Name)
		}
	}
}

// TestPlatformStatusResponse_NoTenantIdentifierFields is a static guard on
// the response shape: every field on platformStatusResponse and
// platformStatusComponent must be a status/count/age/detail string, never a
// field shaped to carry a project name, app name, email, shard name, or
// hostname. This cannot catch a leaky VALUE at runtime (that is what the
// Detail-string component tests below are for), but it pins the field set
// itself so a future addition of e.g. "ProjectName string" would need to
// deliberately edit this test to pass code review.
func TestPlatformStatusResponse_NoTenantIdentifierFields(t *testing.T) {
	resp := platformStatusResponse{
		Status:    platformStatusOK,
		CheckedAt: "2026-08-12T00:00:00Z",
		Components: []platformStatusComponent{
			{Name: "snapshot_reconciler", Status: platformStatusOK, Detail: "данные о состоянии приложений свежие"},
		},
		Note: platformStatusOKNote,
	}
	buf, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(buf, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	allowedTopLevel := map[string]bool{"status": true, "checked_at": true, "components": true, "note": true}
	for k := range decoded {
		if !allowedTopLevel[k] {
			t.Errorf("unexpected top-level field %q in platformStatusResponse", k)
		}
	}
	components, _ := decoded["components"].([]interface{})
	if len(components) != 1 {
		t.Fatalf("expected 1 component in the fixture, got %d", len(components))
	}
	allowedComponentFields := map[string]bool{"name": true, "status": true, "detail": true}
	comp, _ := components[0].(map[string]interface{})
	for k := range comp {
		if !allowedComponentFields[k] {
			t.Errorf("unexpected field %q in platformStatusComponent", k)
		}
	}
}
