package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
)

func testOnboardingPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping onboarding DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newOnboardingCtx(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, rec
}

// onboardingUser returns the id of a throwaway caller and removes the
// user_onboarding rows the handler writes for it. That table keys on user_sub
// with no foreign key back to users, so nothing else takes those rows away and
// they would pile up in the shared cloud-console database run after run.
func onboardingUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	uid := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_onboarding WHERE user_sub = $1`, uid.String())
	})
	return uid
}

func TestOnboarding_GetEmpty(t *testing.T) {
	pool := testOnboardingPool(t)
	h := &Handler{pool: pool}
	c, rec := newOnboardingCtx(http.MethodGet, "/api/v1/onboarding", "")
	auth.SetClaims(c, &auth.Claims{UserID: uuid.New()})

	h.GetOnboarding(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty map for fresh user, got %v", got)
	}
}

func TestOnboarding_PostThenGet(t *testing.T) {
	pool := testOnboardingPool(t)
	h := &Handler{pool: pool}
	uid := onboardingUser(t, pool)

	cPost, recPost := newOnboardingCtx(http.MethodPost, "/api/v1/onboarding/agent", `{"status":"seen","step":0}`)
	auth.SetClaims(cPost, &auth.Claims{UserID: uid})
	cPost.Params = gin.Params{{Key: "key", Value: "agent"}}
	h.PostOnboarding(cPost)
	if recPost.Code != http.StatusOK {
		t.Fatalf("post seen: want 200, got %d: %s", recPost.Code, recPost.Body.String())
	}

	cGet, recGet := newOnboardingCtx(http.MethodGet, "/api/v1/onboarding", "")
	auth.SetClaims(cGet, &auth.Claims{UserID: uid})
	h.GetOnboarding(cGet)
	var got map[string]string
	if err := json.Unmarshal(recGet.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["agent"] != "seen" {
		t.Fatalf("want agent=seen, got %v", got)
	}
}

func TestOnboarding_MonotonicNoDowngrade(t *testing.T) {
	pool := testOnboardingPool(t)
	h := &Handler{pool: pool}
	uid := onboardingUser(t, pool)

	cDone, _ := newOnboardingCtx(http.MethodPost, "/api/v1/onboarding/agent", `{"status":"done","step":1}`)
	auth.SetClaims(cDone, &auth.Claims{UserID: uid})
	cDone.Params = gin.Params{{Key: "key", Value: "agent"}}
	h.PostOnboarding(cDone)

	cSeen, recSeen := newOnboardingCtx(http.MethodPost, "/api/v1/onboarding/agent", `{"status":"seen","step":0}`)
	auth.SetClaims(cSeen, &auth.Claims{UserID: uid})
	cSeen.Params = gin.Params{{Key: "key", Value: "agent"}}
	h.PostOnboarding(cSeen)
	if recSeen.Code != http.StatusOK {
		t.Fatalf("post seen after done: want 200, got %d", recSeen.Code)
	}

	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM user_onboarding WHERE user_sub=$1 AND onboarding_key='agent'`, uid.String()).Scan(&status); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != "done" {
		t.Fatalf("done must not downgrade to seen, got %q", status)
	}
}

func TestOnboarding_UnknownKey400(t *testing.T) {
	pool := testOnboardingPool(t)
	h := &Handler{pool: pool}
	c, rec := newOnboardingCtx(http.MethodPost, "/api/v1/onboarding/nope", `{"status":"seen","step":0}`)
	auth.SetClaims(c, &auth.Claims{UserID: uuid.New()})
	c.Params = gin.Params{{Key: "key", Value: "nope"}}

	h.PostOnboarding(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown key: want 400, got %d", rec.Code)
	}
}

func TestOnboarding_InvalidStatus400(t *testing.T) {
	pool := testOnboardingPool(t)
	h := &Handler{pool: pool}
	c, rec := newOnboardingCtx(http.MethodPost, "/api/v1/onboarding/agent", `{"status":"bogus","step":0}`)
	auth.SetClaims(c, &auth.Claims{UserID: uuid.New()})
	c.Params = gin.Params{{Key: "key", Value: "agent"}}

	h.PostOnboarding(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status: want 400, got %d", rec.Code)
	}
}

// TestOnboardingKeysMatchFrontendRegistry guards the sync contract documented
// on onboardingKeys: a campaign added to the frontend but missing from the
// whitelist makes every progress report 400, so the prompt re-runs on every
// page load forever.
//
// Two frontend producers use the table. campaigns.ts holds the Joyride tours;
// the passkey prompt is a modal rather than a tour, so it declares its key in
// its own component while still storing progress here.
func TestOnboardingKeysMatchFrontendRegistry(t *testing.T) {
	sources := map[string]*regexp.Regexp{
		"../../../frontend/lib/onboarding/campaigns.ts":           regexp.MustCompile(`key:\s*"([^"]+)"`),
		"../../../frontend/components/passkey/passkey-prompt.tsx": regexp.MustCompile(`PASSKEY_KEY\s*=\s*"([^"]+)"`),
	}
	found := map[string]bool{}
	for path, re := range sources {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Skipf("frontend source not available (%v); skipping sync check", err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			found[m[1]] = true
		}
	}
	if len(found) == 0 {
		t.Fatalf("no onboarding keys parsed from the frontend sources")
	}
	for key := range found {
		if !onboardingKeys[key] {
			t.Errorf("campaign %q is in the frontend registry but not in onboardingKeys", key)
		}
	}
	for key := range onboardingKeys {
		if !found[key] {
			t.Errorf("onboardingKeys has %q but the frontend registry does not", key)
		}
	}
}
