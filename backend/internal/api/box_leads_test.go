package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- pure validation and rate limiting (no database) ---

func TestBoxClaimFormat(t *testing.T) {
	valid := []string{"BOX-7F3A-9C21", "BOX-0000-FFFF"}
	for _, v := range valid {
		if !boxClaimRe.MatchString(v) {
			t.Errorf("claim %q should be valid", v)
		}
	}
	// Lowercase is rejected by the regex on purpose: the handler upper-cases
	// first, so anything still lowercase here is a different string entirely.
	invalid := []string{
		"", "BOX-7f3a-9c21", "box-7F3A-9C21", "BOX-7F3A", "BOX-7F3A-9C21-EXTRA",
		"BOX-7F3G-9C21", "7F3A-9C21", "BOX7F3A9C21",
		// The one that matters: a claim column must never be able to hold an
		// address, because it is joined against box_grants and printed in reports.
		"someone@example.com",
	}
	for _, v := range invalid {
		if boxClaimRe.MatchString(v) {
			t.Errorf("claim %q should be rejected", v)
		}
	}
}

func TestBoxFunnelEventSetIsClosed(t *testing.T) {
	// The event name becomes a Prometheus label value, so the set must stay
	// closed and must match the four events in the brief.
	for _, e := range []string{"page_view", "demo_run", "box_requested", "crystallize_intent"} {
		if !boxFunnelEvents[e] {
			t.Errorf("event %q must be accepted", e)
		}
	}
	if len(boxFunnelEvents) != 4 {
		t.Errorf("event set has %d entries, want exactly the 4 funnel events", len(boxFunnelEvents))
	}
	for _, e := range []string{"", "view", "PAGE_VIEW", "box_requested ", "anything"} {
		if boxFunnelEvents[e] {
			t.Errorf("event %q must be rejected", e)
		}
	}
}

func TestBoxFunnelLimiterPerKey(t *testing.T) {
	l := newBoxFunnelLimiter(10, 1000)
	for i := 0; i < 10; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("request %d blocked inside the burst", i)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("request past the per-key burst must be blocked")
	}
	if !l.Allow("5.6.7.8") {
		t.Fatal("a different client must have its own bucket")
	}
}

// The global bucket is the database guard: it must trip even when every request
// claims a fresh source address, which is exactly what a forged X-Forwarded-For
// produces.
func TestBoxFunnelLimiterGlobalCapsForgedSources(t *testing.T) {
	l := newBoxFunnelLimiter(100, 5)
	allowed := 0
	for i := 0; i < 50; i++ {
		if l.Allow(fmt.Sprintf("10.0.0.%d", i)) {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("global bucket allowed %d requests, want 5", allowed)
	}
}

func TestBoxFunnelLimiterDefaults(t *testing.T) {
	l := newBoxFunnelLimiter(0, 0)
	if l.perMin != boxFunnelPerMin {
		t.Errorf("perMin = %d, want %d", l.perMin, boxFunnelPerMin)
	}
	if !l.Allow("1.1.1.1") {
		t.Error("a fresh limiter must allow the first request")
	}
}

func TestNullableString(t *testing.T) {
	if nullableString("") != nil {
		t.Error(`"" must map to SQL NULL`)
	}
	if got := nullableString("x"); got != any("x") {
		t.Errorf("non-empty must pass through, got %v", got)
	}
}

func TestTrimTo(t *testing.T) {
	if got := trimTo("  hello  ", 10); got != "hello" {
		t.Errorf("trimTo = %q, want %q", got, "hello")
	}
	if got := trimTo("abcdef", 3); got != "abc" {
		t.Errorf("trimTo = %q, want %q", got, "abc")
	}
	if got := trimTo("   ", 10); got != "" {
		t.Errorf("whitespace-only must collapse to empty, got %q", got)
	}
}

// --- handler validation (no database: every case below must be rejected before
// the handler ever touches the pool, which is what lets a nil pool work here) ---

func boxFunnelTestRouter(t *testing.T, pool *pgxpool.Pool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &Handler{pool: pool}
	h.boxFunnelLimiter = newBoxFunnelLimiter(1000, 100000)
	r := gin.New()
	r.POST("/api/v1/box/leads", h.RecordBoxFunnelEvent)
	return r
}

func postBoxFunnel(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/box/leads", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRecordBoxFunnelEventRejectsBadRequests(t *testing.T) {
	r := boxFunnelTestRouter(t, nil)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"unknown event", map[string]any{"event": "second_use"}},
		{"empty event", map[string]any{"event": ""}},
		// The PII gate: a non-UUID vid is refused outright, so no email can be
		// smuggled into the vid column even by a hand-rolled POST.
		{"email as vid", map[string]any{"event": "demo_run", "vid": "someone@example.com"}},
		{"non-uuid vid", map[string]any{"event": "demo_run", "vid": "abc123"}},
		{"malformed claim", map[string]any{"event": "demo_run", "claim": "not-a-claim"}},
		{"box_requested without claim", map[string]any{
			"event": "box_requested", "email": "a@b.co", "use_case": "x",
		}},
		{"box_requested without email", map[string]any{
			"event": "box_requested", "claim": "BOX-1111-2222", "use_case": "x",
		}},
		{"box_requested with bad email", map[string]any{
			"event": "box_requested", "claim": "BOX-1111-2222", "email": "nope", "use_case": "x",
		}},
		{"box_requested without use_case", map[string]any{
			"event": "box_requested", "claim": "BOX-1111-2222", "email": "a@b.co",
		}},
		{"crystallize_intent without claim", map[string]any{
			"event": "crystallize_intent", "wants": []string{"volumes"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postBoxFunnel(t, r, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
		})
	}
}

func TestRecordBoxFunnelEventRejectsBadJSON(t *testing.T) {
	r := boxFunnelTestRouter(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/box/leads", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRecordBoxFunnelEventRateLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{pool: nil}
	h.boxFunnelLimiter = newBoxFunnelLimiter(3, 1000)
	r := gin.New()
	r.POST("/api/v1/box/leads", h.RecordBoxFunnelEvent)

	// An invalid event is enough: the limiter runs before validation, so the
	// first three get 400 and the fourth gets 429 without any database access.
	var codes []int
	for i := 0; i < 4; i++ {
		w := postBoxFunnel(t, r, map[string]any{"event": "nope"})
		codes = append(codes, w.Code)
	}
	for i := 0; i < 3; i++ {
		if codes[i] != http.StatusBadRequest {
			t.Fatalf("request %d: status = %d, want 400", i, codes[i])
		}
	}
	if codes[3] != http.StatusTooManyRequests {
		t.Fatalf("request 4: status = %d, want 429", codes[3])
	}
}

// --- database-backed behaviour ---

// boxFunnelTestPool mirrors the pool-backed harness used across the repo
// (alert_recipient_test.go, deploy_hooks_test.go): skip cleanly when
// TEST_DATABASE_URL is unset so `go test ./...` stays green offline.
func boxFunnelTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping box funnel DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// uniqueClaim mints a claim code in the real BOX-XXXX-XXXX shape and registers
// its cleanup, so a repeated run does not collide on the primary key.
func uniqueClaim(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	hex := uuid.NewString()
	claim := "BOX-" + string([]byte{hex[0], hex[1], hex[2], hex[3]}) + "-" + string([]byte{hex[4], hex[5], hex[6], hex[7]})
	claim = upperASCII(claim)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM box_funnel_events WHERE claim = $1`, claim)
		_, _ = pool.Exec(ctx, `DELETE FROM box_leads WHERE claim = $1`, claim)
		_, _ = pool.Exec(ctx, `DELETE FROM box_grants WHERE claim = $1`, claim)
	})
	return claim
}

func upperASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'f' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}

func TestRecordBoxFunnelEventStoresLead(t *testing.T) {
	pool := boxFunnelTestPool(t)
	r := boxFunnelTestRouter(t, pool)
	claim := uniqueClaim(t, pool)
	vid := uuid.NewString()

	w := postBoxFunnel(t, r, map[string]any{
		"event":      "box_requested",
		"claim":      claim,
		"vid":        vid,
		"locale":     "ru",
		"utm_source": "door_box",
		"referer":    "https://cloud.dada-tuda.ru/box",
		"email":      "lead@example.com",
		"contact":    "@tg",
		"agent":      "claude-code",
		"parallel":   "3",
		"price":      "1500",
		"use_case":   "parallel agents on one repo",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}

	ctx := context.Background()
	var email, useCase, utm string
	var storedVID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT email, use_case, utm_source, vid FROM box_leads WHERE claim = $1`, claim,
	).Scan(&email, &useCase, &utm, &storedVID); err != nil {
		t.Fatalf("read back lead: %v", err)
	}
	if email != "lead@example.com" || useCase != "parallel agents on one repo" || utm != "door_box" {
		t.Errorf("lead round-trip mismatch: email=%q use_case=%q utm=%q", email, useCase, utm)
	}
	if storedVID.String() != vid {
		t.Errorf("vid = %s, want %s", storedVID, vid)
	}

	var events int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM box_funnel_events WHERE claim = $1 AND event = 'box_requested'`, claim,
	).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Errorf("box_requested events = %d, want 1", events)
	}

	// A retried submission with the same claim must not duplicate the lead: the
	// fallback path in the Next route can legitimately resend it.
	if w2 := postBoxFunnel(t, r, map[string]any{
		"event": "box_requested", "claim": claim, "vid": vid,
		"email": "lead@example.com", "use_case": "parallel agents on one repo",
	}); w2.Code != http.StatusCreated {
		t.Fatalf("retry status = %d, want 201", w2.Code)
	}
	var leads int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM box_leads WHERE claim = $1`, claim).Scan(&leads); err != nil {
		t.Fatalf("count leads: %v", err)
	}
	if leads != 1 {
		t.Errorf("leads = %d, want 1 after a retry", leads)
	}
}

func TestRecordBoxFunnelEventDeduplicatesPageViewPerVisitor(t *testing.T) {
	pool := boxFunnelTestPool(t)
	r := boxFunnelTestRouter(t, pool)

	vid := uuid.New()
	ctx := context.Background()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM box_funnel_events WHERE vid = $1`, vid) })

	first := postBoxFunnel(t, r, map[string]any{"event": "page_view", "vid": vid.String(), "locale": "en"})
	if first.Code != http.StatusCreated {
		t.Fatalf("first page_view status = %d, want 201 (body: %s)", first.Code, first.Body.String())
	}
	if !decodeRecorded(t, first) {
		t.Fatal("first page_view must be recorded")
	}

	second := postBoxFunnel(t, r, map[string]any{"event": "page_view", "vid": vid.String(), "locale": "en"})
	if second.Code != http.StatusCreated {
		t.Fatalf("second page_view status = %d, want 201", second.Code)
	}
	if decodeRecorded(t, second) {
		t.Error("a second page_view in the same session must report recorded=false")
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM box_funnel_events WHERE vid = $1 AND event = 'page_view'`, vid,
	).Scan(&n); err != nil {
		t.Fatalf("count page_views: %v", err)
	}
	if n != 1 {
		t.Errorf("page_view rows = %d, want 1 — the denominator must count people, not reloads", n)
	}

	// demo_run is NOT deduplicated: replaying the demo twice is two events by one
	// person, and the runbook counts distinct vids when it wants people.
	for i := 0; i < 2; i++ {
		if w := postBoxFunnel(t, r, map[string]any{"event": "demo_run", "vid": vid.String()}); w.Code != http.StatusCreated {
			t.Fatalf("demo_run %d status = %d, want 201", i, w.Code)
		}
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM box_funnel_events WHERE vid = $1 AND event = 'demo_run'`, vid,
	).Scan(&n); err != nil {
		t.Fatalf("count demo_runs: %v", err)
	}
	if n != 2 {
		t.Errorf("demo_run rows = %d, want 2", n)
	}
}

func TestRecordBoxFunnelEventStoresCrystallizeIntentWants(t *testing.T) {
	pool := boxFunnelTestPool(t)
	r := boxFunnelTestRouter(t, pool)
	claim := uniqueClaim(t, pool)

	w := postBoxFunnel(t, r, map[string]any{
		"event": "crystallize_intent",
		"claim": claim,
		"vid":   uuid.NewString(),
		"wants": []string{"volumes", "env", "domain"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}

	var props []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT props FROM box_funnel_events WHERE claim = $1 AND event = 'crystallize_intent'`, claim,
	).Scan(&props); err != nil {
		t.Fatalf("read back props: %v", err)
	}
	var decoded struct {
		Wants []string `json:"wants"`
	}
	if err := json.Unmarshal(props, &decoded); err != nil {
		t.Fatalf("decode props: %v", err)
	}
	if len(decoded.Wants) != 3 {
		t.Errorf("wants = %v, want 3 entries", decoded.Wants)
	}
}

// crystallize_intent must survive a claim that never made it into box_leads --
// the backend could have been down when the lead was submitted, and this is the
// highest-signal event in the experiment. A foreign key here would drop it.
func TestRecordBoxFunnelEventKeepsIntentForAnUnknownClaim(t *testing.T) {
	pool := boxFunnelTestPool(t)
	r := boxFunnelTestRouter(t, pool)
	claim := uniqueClaim(t, pool)

	w := postBoxFunnel(t, r, map[string]any{"event": "crystallize_intent", "claim": claim})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM box_funnel_events WHERE claim = $1`, claim).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Errorf("events = %d, want 1 for a claim with no lead row", n)
	}
	var leads int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM box_leads WHERE claim = $1`, claim).Scan(&leads); err != nil {
		t.Fatalf("count leads: %v", err)
	}
	if leads != 0 {
		t.Errorf("leads = %d, want 0", leads)
	}
}

func decodeRecorded(t *testing.T, w *httptest.ResponseRecorder) bool {
	t.Helper()
	var body struct {
		Status   string `json:"status"`
		Recorded bool   `json:"recorded"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return body.Recorded
}
