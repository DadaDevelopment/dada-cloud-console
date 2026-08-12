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

// seedFeedback inserts a feedback row for the given user_sub and returns its id,
// cleaning it up when the test ends.
func seedFeedback(t *testing.T, pool *pgxpool.Pool, userSub, route, message, status, resolution string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO feedback (user_sub, route, message, status, resolution, resolved_at)
		 VALUES ($1, $2, $3, $4, $5, CASE WHEN $4 = 'resolved' THEN NOW() ELSE NULL END)
		 RETURNING id`,
		userSub, route, message, status, resolution,
	).Scan(&id); err != nil {
		t.Fatalf("seed feedback: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM feedback WHERE id = $1`, id)
	})
	return id
}

// newMineCtx builds a gin context for a GET request carrying the given claims.
func newMineCtx(claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/feedback/mine", nil)
	if claims != nil {
		auth.SetClaims(c, claims)
	}
	return c, rec
}

// TestListMyFeedback_OnlyOwnRows is the isolation regression test: the query
// must filter on the caller's own user_sub, never returning another user's
// ticket even when both wrote against the same handler in the same run.
func TestListMyFeedback_OnlyOwnRows(t *testing.T) {
	pool := testAgentGatePool(t)
	h := &Handler{pool: pool}

	me := seedUser(t, pool)
	other := seedUser(t, pool)

	seedFeedback(t, pool, me.String(), "/projects/x/apps/web", "my ticket", "new", "")
	seedFeedback(t, pool, other.String(), "/projects/x/apps/web", "someone else's ticket", "new", "")

	c, rec := newMineCtx(&auth.Claims{UserID: me})
	h.ListMyFeedback(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []myFeedbackItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1 (only the caller's own ticket): %+v", len(resp.Items), resp.Items)
	}
	if resp.Items[0].Message != "my ticket" {
		t.Fatalf("message = %q, want the caller's own ticket, not the other user's", resp.Items[0].Message)
	}
}

// TestListMyFeedback_EmptyIsArrayNotNull pins the contract the frontend is
// built against: a caller with no tickets gets items: [], never items: null.
func TestListMyFeedback_EmptyIsArrayNotNull(t *testing.T) {
	pool := testAgentGatePool(t)
	h := &Handler{pool: pool}

	me := seedUser(t, pool)

	c, rec := newMineCtx(&auth.Claims{UserID: me})
	h.ListMyFeedback(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !jsonHasEmptyItemsArray(t, got) {
		t.Fatalf("body = %s, want items to be [] not null", got)
	}
}

func jsonHasEmptyItemsArray(t *testing.T, body string) bool {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, ok := raw["items"]
	if !ok {
		t.Fatalf("response has no items key: %s", body)
	}
	return string(items) == "[]"
}

// TestListMyFeedback_ResolvedTicketCarriesResolution covers the closed-ticket
// shape the frontend renders: a resolved row must surface both the operator's
// resolution note and a non-null resolved_at.
func TestListMyFeedback_ResolvedTicketCarriesResolution(t *testing.T) {
	pool := testAgentGatePool(t)
	h := &Handler{pool: pool}

	me := seedUser(t, pool)
	seedFeedback(t, pool, me.String(), "/projects/x/apps/web", "it crashed", "resolved", "fixed the null pointer")

	c, rec := newMineCtx(&auth.Claims{UserID: me})
	h.ListMyFeedback(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []myFeedbackItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Items))
	}
	it := resp.Items[0]
	if it.Status != "resolved" {
		t.Fatalf("status = %q, want resolved", it.Status)
	}
	if it.Resolution != "fixed the null pointer" {
		t.Fatalf("resolution = %q, want the operator's note", it.Resolution)
	}
	if it.ResolvedAt == nil {
		t.Fatalf("resolved_at = nil, want a timestamp")
	}
}

// TestListMyFeedback_Unauthenticated confirms the route rejects a caller with
// no claims instead of panicking on a nil UserID.
func TestListMyFeedback_Unauthenticated(t *testing.T) {
	pool := testAgentGatePool(t)
	h := &Handler{pool: pool}

	c, rec := newMineCtx(nil)
	h.ListMyFeedback(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}
