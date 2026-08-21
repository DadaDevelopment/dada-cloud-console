package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedBillingPromoCode inserts one throwaway promo code and removes it (and
// every redemption against it) when the test ends, so runs never collide
// with the live STUDSTARTUP row seeded by migration 136.
func seedBillingPromoCode(t *testing.T, pool *pgxpool.Pool, plan string, days, maxRedemptions int, validUntil *time.Time) string {
	t.Helper()
	code := "TESTPROMO" + strings.ToUpper(uuid.NewString()[:8])
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO promo_codes (code, plan, days, max_redemptions, valid_until)
		VALUES ($1, $2, $3, $4, $5)
	`, code, plan, days, maxRedemptions, validUntil); err != nil {
		t.Fatalf("seed promo code: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM promo_redemptions WHERE code = $1`, code)
		_, _ = pool.Exec(ctx, `DELETE FROM promo_codes WHERE code = $1`, code)
	})
	return code
}

func billingPromoRedeemedCount(t *testing.T, pool *pgxpool.Pool, code string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT redeemed_count FROM promo_codes WHERE code = $1`, code,
	).Scan(&n); err != nil {
		t.Fatalf("read redeemed_count: %v", err)
	}
	return n
}

func redeemBillingPromoAs(h *Handler, userID uuid.UUID, code string) *httptestRecorderResult {
	c, rec := newGrowthCtx(http.MethodPost, "/api/v1/billing/promo/redeem", fmt.Sprintf(`{"code":%q}`, code))
	auth.SetClaims(c, &auth.Claims{UserID: userID})
	h.RedeemBillingPromo(c)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return &httptestRecorderResult{status: rec.Code, body: body}
}

type httptestRecorderResult struct {
	status int
	body   map[string]any
}

func TestRedeemBillingPromo_GrantsPlanWithATerm(t *testing.T) {
	pool := growthTestPool(t)
	now := time.Now().UTC()
	userID, _, _, orgID, _ := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	code := seedBillingPromoCode(t, pool, "startup", 30, 10, nil)
	t.Cleanup(func() { dropSeededAudit(pool, "BillingAccount", code) })

	h := &Handler{pool: pool}
	res := redeemBillingPromoAs(h, userID, code)

	if res.status != http.StatusOK {
		t.Fatalf("want 200, got %d: %v", res.status, res.body)
	}
	if res.body["applied"] != true {
		t.Fatalf("applied=%v want true: %v", res.body["applied"], res.body)
	}
	if res.body["plan"] != "startup" {
		t.Fatalf("plan=%v want startup", res.body["plan"])
	}

	plan, expires := promoPlan(t, pool, orgID)
	if plan != "startup" {
		t.Fatalf("plan=%q want startup", plan)
	}
	if expires == nil {
		t.Fatal("plan granted with no expiry -- a promo term must never be perpetual")
	}
	days := expires.Sub(now).Hours() / 24
	if days < 29 || days > 31 {
		t.Fatalf("term=%.1f days want ~30", days)
	}
	if got := billingPromoRedeemedCount(t, pool, code); got != 1 {
		t.Fatalf("redeemed_count=%d want 1", got)
	}
}

// TestRedeemBillingPromo_SameOrgTwiceIsRefused proves the eligibility gate:
// a second redemption of the same code by the same org must fail with the
// machine-readable promo_already_redeemed code and must not consume a
// second slot of max_redemptions.
func TestRedeemBillingPromo_SameOrgTwiceIsRefused(t *testing.T) {
	pool := growthTestPool(t)
	now := time.Now().UTC()
	userID, _, _, orgID, _ := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	code := seedBillingPromoCode(t, pool, "startup", 30, 10, nil)
	t.Cleanup(func() { dropSeededAudit(pool, "BillingAccount", code) })

	h := &Handler{pool: pool}
	first := redeemBillingPromoAs(h, userID, code)
	if first.status != http.StatusOK {
		t.Fatalf("first redeem: want 200, got %d: %v", first.status, first.body)
	}

	second := redeemBillingPromoAs(h, userID, code)
	if second.status != http.StatusConflict {
		t.Fatalf("second redeem: want 409, got %d: %v", second.status, second.body)
	}
	if second.body["code"] != promoErrAlreadyRedeemed {
		t.Fatalf("second redeem: code=%v want %q", second.body["code"], promoErrAlreadyRedeemed)
	}
	if got := billingPromoRedeemedCount(t, pool, code); got != 1 {
		t.Fatalf("redeemed_count=%d want 1 (second call must not consume a slot)", got)
	}

	_, expires := promoPlan(t, pool, orgID)
	days := expires.Sub(now).Hours() / 24
	if days > 31 {
		t.Fatalf("term=%.1f days -- the refused second call still extended it", days)
	}
}

func TestRedeemBillingPromo_UnknownCodeIsNotFound(t *testing.T) {
	pool := growthTestPool(t)
	now := time.Now().UTC()
	userID, _, _, _, _ := growthAccount(t, pool, now.Add(-30*24*time.Hour))

	h := &Handler{pool: pool}
	res := redeemBillingPromoAs(h, userID, "NOSUCHCODE")
	if res.status != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %v", res.status, res.body)
	}
	if res.body["code"] != promoErrCodeNotFound {
		t.Fatalf("code=%v want %q", res.body["code"], promoErrCodeNotFound)
	}
}

func TestRedeemBillingPromo_ExpiredCodeIsGone(t *testing.T) {
	pool := growthTestPool(t)
	now := time.Now().UTC()
	userID, _, _, _, _ := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	expired := now.Add(-time.Hour)
	code := seedBillingPromoCode(t, pool, "startup", 30, 10, &expired)

	h := &Handler{pool: pool}
	res := redeemBillingPromoAs(h, userID, code)
	if res.status != http.StatusGone {
		t.Fatalf("want 410, got %d: %v", res.status, res.body)
	}
	if res.body["code"] != promoErrCodeExpired {
		t.Fatalf("code=%v want %q", res.body["code"], promoErrCodeExpired)
	}
	if got := billingPromoRedeemedCount(t, pool, code); got != 0 {
		t.Fatalf("redeemed_count=%d want 0", got)
	}
}

func TestRedeemBillingPromo_PayingCustomerKeepsTheirPlan(t *testing.T) {
	pool := growthTestPool(t)
	now := time.Now().UTC()
	userID, _, _, orgID, _ := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	code := seedBillingPromoCode(t, pool, "startup", 30, 10, nil)
	t.Cleanup(func() { dropSeededAudit(pool, "BillingAccount", code) })

	paidUntil := now.Add(300 * 24 * time.Hour)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, updated_at)
		VALUES ($1, 'business', now(), $2, now())
	`, orgID, paidUntil); err != nil {
		t.Fatalf("seed paid account: %v", err)
	}

	h := &Handler{pool: pool}
	res := redeemBillingPromoAs(h, userID, code)
	if res.status != http.StatusOK {
		t.Fatalf("want 200, got %d: %v", res.status, res.body)
	}
	if res.body["applied"] != false {
		t.Fatalf("applied=%v want false for a paying customer", res.body["applied"])
	}
	plan, expires := promoPlan(t, pool, orgID)
	if plan != "business" {
		t.Fatalf("plan=%q -- the promo downgraded a paying customer", plan)
	}
	if expires == nil || expires.Before(paidUntil.Add(-time.Minute)) {
		t.Fatalf("term shortened to %v, paid through %v", expires, paidUntil)
	}
	if got := billingPromoRedeemedCount(t, pool, code); got != 1 {
		t.Fatalf("redeemed_count=%d want 1 -- the code is still consumed even when the plan is not applied", got)
	}
}

// TestRedeemBillingPromo_ConcurrencyCannotOversell is the mandatory
// concurrency/oversell proof: a code capped at max_redemptions=3 is redeemed
// by 10 different orgs at once, and exactly 3 must succeed.
func TestRedeemBillingPromo_ConcurrencyCannotOversell(t *testing.T) {
	pool := growthTestPool(t)
	now := time.Now().UTC()
	const maxRedemptions = 3
	const attempts = 10
	code := seedBillingPromoCode(t, pool, "startup", 30, maxRedemptions, nil)

	h := &Handler{pool: pool}
	userIDs := make([]uuid.UUID, attempts)
	for i := range userIDs {
		userIDs[i], _, _, _, _ = growthAccount(t, pool, now.Add(-30*24*time.Hour))
	}
	t.Cleanup(func() { dropSeededAudit(pool, "BillingAccount", code) })

	var wg sync.WaitGroup
	statuses := make([]int, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := redeemBillingPromoAs(h, userIDs[i], code)
			statuses[i] = res.status
		}(i)
	}
	wg.Wait()

	oks := 0
	for _, s := range statuses {
		if s == http.StatusOK {
			oks++
		}
	}
	if oks != maxRedemptions {
		t.Fatalf("succeeded=%d want exactly %d out of %d concurrent redeemers", oks, maxRedemptions, attempts)
	}
	if got := billingPromoRedeemedCount(t, pool, code); got != maxRedemptions {
		t.Fatalf("redeemed_count=%d want %d -- the counter must match what actually succeeded", got, maxRedemptions)
	}
	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM promo_redemptions WHERE code = $1`, code,
	).Scan(&rows); err != nil {
		t.Fatalf("count redemptions: %v", err)
	}
	if rows != maxRedemptions {
		t.Fatalf("promo_redemptions rows=%d want %d", rows, maxRedemptions)
	}
}
