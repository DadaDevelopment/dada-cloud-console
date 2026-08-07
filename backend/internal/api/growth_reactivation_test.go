package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func growthTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping reactivation DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// growthSpec names a throwaway campaign for one test and removes its rows
// afterwards.
//
// Every one of these tests runs against the shared cloud-console database,
// where the live reactivation campaign also lives. Running the sweep under the
// production campaign name would stamp sent_at on real dormant accounts that
// never received a letter, and the unique index would then refuse to ever mail
// them -- the campaign would be burned by its own test suite. A per-test
// campaign name keeps the code path identical and the live funnel untouched.
func growthSpec(t *testing.T, pool *pgxpool.Pool) campaignSpec {
	t.Helper()
	name := "test-reactivation-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM growth_campaign_sends WHERE campaign = $1`, name)
	})
	return campaignSpec{Campaign: name, Variant: "a", MinAge: reactivationMinAge, PerRun: 3}
}

// growthAccount seeds a customer-kind account that registered long ago, owns a
// project, and has shipped nothing.
//
// createdAt is pushed far into the past on purpose: enrollment orders
// candidates by signup date, so an ancient seed sorts ahead of every real
// account and lands inside the sweep's per-run limit deterministically.
func growthAccount(t *testing.T, pool *pgxpool.Pool, createdAt time.Time) (userID, projectID, envID uuid.UUID, orgID, email string) {
	t.Helper()
	ctx := context.Background()
	name := "growth-" + uuid.NewString()[:8]
	email = name + "@example.com"
	orgID = "org-" + name

	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, display_name, created_at)
		VALUES ($1, $2, '', $1, $3) RETURNING id
	`, name, email, createdAt).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { dropSeededUser(pool, userID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (name, display_name, owner_id, org_id) VALUES ($1, $1, $2, $3) RETURNING id
	`, name, userID, orgID).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id
	`, projectID, name).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})
	return userID, projectID, envID, orgID, email
}

// growthBuild gives an account the one thing that disqualifies it from the
// campaign: a build.
func growthBuild(t *testing.T, pool *pgxpool.Pool, projectID, envID, userID uuid.UUID, status string, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	name := "growth-repo-" + uuid.NewString()[:8]
	var repoID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO git_repos (project_id, environment_id, app_name, provider, repo_full_name, clone_url)
		VALUES ($1, $2, $3, 'github', $4, $5) RETURNING id
	`, projectID, envID, name, "dada/"+name, "https://github.com/dada/"+name+".git").Scan(&repoID); err != nil {
		t.Fatalf("seed git repo: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO builds (git_repo_id, environment_id, app_name, commit_sha, branch, triggered_by, trigger, status, created_at)
		VALUES ($1, $2, $3, $4, 'main', $5, 'manual', $6, $7)
	`, repoID, envID, name, uuid.NewString()[:12], userID, status, createdAt); err != nil {
		t.Fatalf("seed build: %v", err)
	}
}

func growthSendRow(t *testing.T, pool *pgxpool.Pool, spec campaignSpec, userID uuid.UUID) (token string, sentAt, clickedAt, redeemedAt, convertedAt *time.Time) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT token, sent_at, clicked_at, redeemed_at, converted_at
		FROM growth_campaign_sends WHERE campaign = $1 AND user_id = $2
	`, spec.Campaign, userID).Scan(&token, &sentAt, &clickedAt, &redeemedAt, &convertedAt)
	if err != nil {
		t.Fatalf("read campaign send: %v", err)
	}
	return token, sentAt, clickedAt, redeemedAt, convertedAt
}

func sendsTo(m *recordingMailer, email string) int {
	n := 0
	for _, s := range m.sends {
		if s.to == email {
			n++
		}
	}
	return n
}

func TestSweepReactivation_MailsDormantAccountAndSkipsDeployer(t *testing.T) {
	pool := growthTestPool(t)
	spec := growthSpec(t, pool)
	now := time.Now().UTC()
	ancient := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

	dormant, _, _, _, dormantEmail := growthAccount(t, pool, ancient)
	shipped, shippedProject, shippedEnv, _, shippedEmail := growthAccount(t, pool, ancient.Add(time.Hour))
	growthBuild(t, pool, shippedProject, shippedEnv, shipped, "success", ancient.Add(48*time.Hour))

	mailer := &recordingMailer{}
	sweepCampaign(context.Background(), pool, mailer, "https://console.dada-tuda.ru", now, spec)

	token, sentAt, _, _, _ := growthSendRow(t, pool, spec, dormant)
	if sentAt == nil {
		t.Fatal("dormant account enrolled but never marked sent")
	}
	if len(token) != promoTokenHexLen {
		t.Fatalf("token length=%d want %d", len(token), promoTokenHexLen)
	}
	if got := sendsTo(mailer, dormantEmail); got != 1 {
		t.Fatalf("letters to the dormant account=%d want 1", got)
	}
	if got := sendsTo(mailer, shippedEmail); got != 0 {
		t.Fatalf("letters to an account that already shipped=%d want 0", got)
	}

	var shippedRows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM growth_campaign_sends WHERE campaign = $1 AND user_id = $2`,
		spec.Campaign, shipped,
	).Scan(&shippedRows); err != nil {
		t.Fatalf("count sends for the shipped account: %v", err)
	}
	if shippedRows != 0 {
		t.Fatalf("shipped account enrolled %d times, want 0", shippedRows)
	}
}

func TestSweepReactivation_SecondPassDoesNotMailAgain(t *testing.T) {
	pool := growthTestPool(t)
	spec := growthSpec(t, pool)
	now := time.Now().UTC()
	dormant, _, _, _, email := growthAccount(t, pool, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC))

	mailer := &recordingMailer{}
	sweepCampaign(context.Background(), pool, mailer, "https://console.dada-tuda.ru", now, spec)
	sweepCampaign(context.Background(), pool, mailer, "https://console.dada-tuda.ru", now.Add(time.Hour), spec)

	if got := sendsTo(mailer, email); got != 1 {
		t.Fatalf("letters after two sweeps=%d want 1", got)
	}
	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM growth_campaign_sends WHERE campaign = $1 AND user_id = $2`,
		spec.Campaign, dormant,
	).Scan(&rows); err != nil {
		t.Fatalf("count sends: %v", err)
	}
	if rows != 1 {
		t.Fatalf("enrolled %d times, want 1", rows)
	}
}

func TestSweepReactivation_ConversionCountsOnlyBuildsAfterTheLetter(t *testing.T) {
	pool := growthTestPool(t)
	spec := growthSpec(t, pool)
	now := time.Now().UTC()
	dormant, projectID, envID, _, _ := growthAccount(t, pool, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC))

	mailer := &recordingMailer{}
	sweepCampaign(context.Background(), pool, mailer, "https://console.dada-tuda.ru", now, spec)

	growthBuild(t, pool, projectID, envID, dormant, "failed", now.Add(time.Hour))
	sweepCampaign(context.Background(), pool, mailer, "https://console.dada-tuda.ru", now.Add(2*time.Hour), spec)
	if _, _, _, _, converted := growthSendRow(t, pool, spec, dormant); converted != nil {
		t.Fatalf("a failed build counted as a conversion: %v", converted)
	}

	growthBuild(t, pool, projectID, envID, dormant, "success", now.Add(3*time.Hour))
	sweepCampaign(context.Background(), pool, mailer, "https://console.dada-tuda.ru", now.Add(4*time.Hour), spec)
	_, _, _, _, converted := growthSendRow(t, pool, spec, dormant)
	if converted == nil {
		t.Fatal("a successful build after the letter did not count as a conversion")
	}
}

func newGrowthCtx(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, rec
}

// seedPromo enrolls one account by hand and marks it mailed, which is the
// state every redeem test starts from.
func seedPromo(t *testing.T, pool *pgxpool.Pool, spec campaignSpec, userID uuid.UUID, email string, sentAt time.Time) string {
	t.Helper()
	token := strings.Repeat("ab", promoTokenBytes)[:promoTokenHexLen-8] + uuid.NewString()[:8]
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO growth_campaign_sends (campaign, variant, user_id, email, token, sent_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6, $6)
	`, spec.Campaign, spec.Variant, userID, email, token, sentAt); err != nil {
		t.Fatalf("seed promo send: %v", err)
	}
	return token
}

func promoPlan(t *testing.T, pool *pgxpool.Pool, orgID string) (plan string, expires *time.Time) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT plan, plan_expires_at FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&plan, &expires); err != nil {
		t.Fatalf("read billing account: %v", err)
	}
	return plan, expires
}

func TestRedeemPromo_GrantsPlanWithATerm(t *testing.T) {
	pool := growthTestPool(t)
	spec := growthSpec(t, pool)
	now := time.Now().UTC()
	userID, _, _, orgID, email := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	token := seedPromo(t, pool, spec, userID, email, now.Add(-time.Hour))
	t.Cleanup(func() { dropSeededAudit(pool, "BillingAccount", reactivationPlan) })

	h := &Handler{pool: pool}
	c, rec := newGrowthCtx(http.MethodPost, "/api/v1/promo/redeem", fmt.Sprintf(`{"token":%q}`, token))
	auth.SetClaims(c, &auth.Claims{UserID: userID})
	h.RedeemPromo(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["granted"] != true {
		t.Fatalf("granted=%v want true: %s", got["granted"], rec.Body.String())
	}

	plan, expires := promoPlan(t, pool, orgID)
	if plan != reactivationPlan {
		t.Fatalf("plan=%q want %q", plan, reactivationPlan)
	}
	if expires == nil {
		t.Fatal("plan granted with no expiry -- a promo term must never be perpetual")
	}
	days := expires.Sub(now).Hours() / 24
	if days < float64(reactivationGrantDays)-1 || days > float64(reactivationGrantDays)+1 {
		t.Fatalf("term=%.1f days want ~%d", days, reactivationGrantDays)
	}

	if _, _, _, redeemed, _ := growthSendRow(t, pool, spec, userID); redeemed == nil {
		t.Fatal("plan granted but the send row was not marked redeemed")
	}
}

func TestRedeemPromo_SecondCallIsANoOp(t *testing.T) {
	pool := growthTestPool(t)
	spec := growthSpec(t, pool)
	now := time.Now().UTC()
	userID, _, _, orgID, email := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	token := seedPromo(t, pool, spec, userID, email, now.Add(-time.Hour))
	t.Cleanup(func() { dropSeededAudit(pool, "BillingAccount", reactivationPlan) })

	h := &Handler{pool: pool}
	for i := 0; i < 2; i++ {
		c, rec := newGrowthCtx(http.MethodPost, "/api/v1/promo/redeem", fmt.Sprintf(`{"token":%q}`, token))
		auth.SetClaims(c, &auth.Claims{UserID: userID})
		h.RedeemPromo(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: want 200, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("call %d: decode: %v", i+1, err)
		}
		if want := i == 1; got["already_redeemed"] != want {
			t.Fatalf("call %d: already_redeemed=%v want %v", i+1, got["already_redeemed"], want)
		}
	}

	_, expires := promoPlan(t, pool, orgID)
	if expires == nil {
		t.Fatal("expiry lost after the second redeem")
	}
}

func TestRedeemPromo_SomeoneElsesTokenIsRefused(t *testing.T) {
	pool := growthTestPool(t)
	spec := growthSpec(t, pool)
	now := time.Now().UTC()
	owner, _, _, ownerOrg, ownerEmail := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	stranger, _, _, _, _ := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	token := seedPromo(t, pool, spec, owner, ownerEmail, now.Add(-time.Hour))

	h := &Handler{pool: pool}
	c, rec := newGrowthCtx(http.MethodPost, "/api/v1/promo/redeem", fmt.Sprintf(`{"token":%q}`, token))
	auth.SetClaims(c, &auth.Claims{UserID: stranger})
	h.RedeemPromo(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for a forwarded link, got %d: %s", rec.Code, rec.Body.String())
	}
	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM billing_accounts WHERE org_id = $1`, ownerOrg,
	).Scan(&rows); err != nil {
		t.Fatalf("count billing accounts: %v", err)
	}
	if rows != 0 {
		t.Fatalf("a refused redeem still granted a plan (%d rows)", rows)
	}
	if _, _, _, redeemed, _ := growthSendRow(t, pool, spec, owner); redeemed != nil {
		t.Fatalf("a refused redeem still consumed the token: %v", redeemed)
	}
}

func TestRedeemPromo_PayingCustomerKeepsTheirPlan(t *testing.T) {
	pool := growthTestPool(t)
	spec := growthSpec(t, pool)
	now := time.Now().UTC()
	userID, _, _, orgID, email := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	token := seedPromo(t, pool, spec, userID, email, now.Add(-time.Hour))
	t.Cleanup(func() { dropSeededAudit(pool, "BillingAccount", reactivationPlan) })

	paidUntil := now.Add(300 * 24 * time.Hour)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, updated_at)
		VALUES ($1, 'business', now(), $2, now())
	`, orgID, paidUntil); err != nil {
		t.Fatalf("seed paid account: %v", err)
	}

	h := &Handler{pool: pool}
	c, rec := newGrowthCtx(http.MethodPost, "/api/v1/promo/redeem", fmt.Sprintf(`{"token":%q}`, token))
	auth.SetClaims(c, &auth.Claims{UserID: userID})
	h.RedeemPromo(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["granted"] != false {
		t.Fatalf("granted=%v want false for a paying customer", got["granted"])
	}
	plan, expires := promoPlan(t, pool, orgID)
	if plan != "business" {
		t.Fatalf("plan=%q -- the promo downgraded a paying customer", plan)
	}
	if expires == nil || expires.Before(paidUntil.Add(-time.Minute)) {
		t.Fatalf("term shortened to %v, paid through %v", expires, paidUntil)
	}
}

func TestRecordPromoClick_StampsTheClickAndHidesUnknownTokens(t *testing.T) {
	pool := growthTestPool(t)
	spec := growthSpec(t, pool)
	now := time.Now().UTC()
	userID, _, _, _, email := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	token := seedPromo(t, pool, spec, userID, email, now.Add(-time.Hour))

	h := &Handler{pool: pool}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/v1/promo/click", h.RecordPromoClick)
	click := func(tok string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/promo/click", strings.NewReader(fmt.Sprintf(`{"token":%q}`, tok)))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(rec, req)
		return rec
	}

	rec := click(token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, _, clicked, _, _ := growthSendRow(t, pool, spec, userID); clicked == nil {
		t.Fatal("click not recorded")
	}

	rec2 := click(strings.Repeat("f", promoTokenHexLen))
	if rec2.Code != http.StatusNoContent || rec2.Body.Len() != 0 {
		t.Fatalf("an unknown token must answer exactly like a live one, got %d %q", rec2.Code, rec2.Body.String())
	}
}

func promoOpenedAt(t *testing.T, pool *pgxpool.Pool, spec campaignSpec, userID uuid.UUID) *time.Time {
	t.Helper()
	var opened *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT opened_at FROM growth_campaign_sends WHERE campaign = $1 AND user_id = $2`,
		spec.Campaign, userID,
	).Scan(&opened); err != nil {
		t.Fatalf("read opened_at: %v", err)
	}
	return opened
}

func TestRecordPromoOpen_StampsFirstOpenAndAlwaysServesThePixel(t *testing.T) {
	pool := growthTestPool(t)
	spec := growthSpec(t, pool)
	now := time.Now().UTC()
	userID, _, _, _, email := growthAccount(t, pool, now.Add(-30*24*time.Hour))
	token := seedPromo(t, pool, spec, userID, email, now.Add(-time.Hour))

	h := &Handler{pool: pool}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/api/v1/promo/pixel/:token", h.RecordPromoOpen)
	open := func(tok string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/promo/pixel/"+tok+".gif", nil))
		return rec
	}

	rec := open(token)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("want a 200 image/gif, got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Body.Len() != len(promoPixelGIF) {
		t.Fatalf("pixel body=%d bytes want %d", rec.Body.Len(), len(promoPixelGIF))
	}
	first := promoOpenedAt(t, pool, spec, userID)
	if first == nil {
		t.Fatal("first open not recorded")
	}

	if rec2 := open(token); rec2.Code != http.StatusOK {
		t.Fatalf("second open answered %d", rec2.Code)
	}
	second := promoOpenedAt(t, pool, spec, userID)
	if second == nil || !second.Equal(*first) {
		t.Fatalf("opened_at moved on a re-open: %v -> %v; a proxy refetch must not look like a new read", first, second)
	}

	if rec3 := open(strings.Repeat("f", promoTokenHexLen)); rec3.Code != http.StatusOK || rec3.Body.Len() != len(promoPixelGIF) {
		t.Fatalf("an unknown token must answer exactly like a live one, got %d %d bytes", rec3.Code, rec3.Body.Len())
	}
}

// growthFixSpec names a throwaway fix-wave campaign, for the same reason
// growthSpec does: these tests share the production database with the live
// campaign, and a fix wave run under its real name would burn real recipients.
func growthFixSpec(t *testing.T, pool *pgxpool.Pool) campaignSpec {
	t.Helper()
	name := "test-reactivation-fix-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM growth_campaign_sends WHERE campaign = $1`, name)
	})
	return campaignSpec{Campaign: name, Variant: "a", MinAge: reactivationFixMinAge, PerRun: 3, QuietPeriod: reactivationWaveQuiet}
}

// markRedeemed puts a first-wave row in the state the fix wave targets.
func markRedeemed(t *testing.T, pool *pgxpool.Pool, spec campaignSpec, userID uuid.UUID, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE growth_campaign_sends SET redeemed_at = $3, updated_at = $3
		WHERE campaign = $1 AND user_id = $2
	`, spec.Campaign, userID, at); err != nil {
		t.Fatalf("mark redeemed: %v", err)
	}
}

func TestSweepReactivationFix_MailsTheStalledRedeemerOnly(t *testing.T) {
	pool := growthTestPool(t)
	first := growthSpec(t, pool)
	fix := growthFixSpec(t, pool)
	now := time.Now().UTC()
	ancient := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

	stalled, _, _, _, stalledEmail := growthAccount(t, pool, ancient)
	seedPromo(t, pool, first, stalled, stalledEmail, ancient.Add(time.Hour))
	markRedeemed(t, pool, first, stalled, now.Add(-48*time.Hour))

	shipped, shippedProject, shippedEnv, _, shippedEmail := growthAccount(t, pool, ancient)
	seedPromo(t, pool, first, shipped, shippedEmail, ancient.Add(time.Hour))
	markRedeemed(t, pool, first, shipped, now.Add(-48*time.Hour))
	growthBuild(t, pool, shippedProject, shippedEnv, shipped, "success", now.Add(-24*time.Hour))

	fresh, _, _, _, freshEmail := growthAccount(t, pool, ancient)
	seedPromo(t, pool, first, fresh, freshEmail, ancient.Add(time.Hour))
	markRedeemed(t, pool, first, fresh, now.Add(-time.Hour))

	silent, _, _, _, silentEmail := growthAccount(t, pool, ancient)
	seedPromo(t, pool, first, silent, silentEmail, ancient.Add(time.Hour))

	mailer := &recordingMailer{}
	sweepFixCampaign(context.Background(), pool, mailer, "https://console.dada-tuda.ru", now, fix, first.Campaign)

	if got := sendsTo(mailer, stalledEmail); got != 1 {
		t.Fatalf("letters to the stalled redeemer=%d want 1", got)
	}
	if got := sendsTo(mailer, shippedEmail); got != 0 {
		t.Fatalf("letters to a redeemer who already shipped=%d want 0", got)
	}
	if got := sendsTo(mailer, freshEmail); got != 0 {
		t.Fatalf("letters to someone who redeemed an hour ago=%d want 0 -- the wave must not read as surveillance", got)
	}
	if got := sendsTo(mailer, silentEmail); got != 0 {
		t.Fatalf("letters to someone who never redeemed=%d want 0", got)
	}

	token, sentAt, _, _, _ := growthSendRow(t, pool, fix, stalled)
	if sentAt == nil {
		t.Fatal("stalled redeemer enrolled but never marked sent")
	}
	if len(token) != promoTokenHexLen {
		t.Fatalf("fix-wave token length=%d want %d", len(token), promoTokenHexLen)
	}
	if firstToken, _, _, _, _ := growthSendRow(t, pool, first, stalled); firstToken == token {
		t.Fatal("fix wave reused the first wave's token; the two waves would be indistinguishable in the funnel")
	}
	if len(mailer.html) == 0 || !strings.Contains(mailer.html[0], token) {
		t.Fatal("the letter does not carry the fix wave's own promo link")
	}
	if !strings.Contains(mailer.html[0], "/api/v1/promo/pixel/"+token+".gif") {
		t.Fatal("the fix letter has no open pixel")
	}
}

func TestSweepReactivationFix_SecondPassDoesNotMailAgain(t *testing.T) {
	pool := growthTestPool(t)
	first := growthSpec(t, pool)
	fix := growthFixSpec(t, pool)
	now := time.Now().UTC()
	ancient := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

	stalled, _, _, _, email := growthAccount(t, pool, ancient)
	seedPromo(t, pool, first, stalled, email, ancient.Add(time.Hour))
	markRedeemed(t, pool, first, stalled, now.Add(-48*time.Hour))

	mailer := &recordingMailer{}
	sweepFixCampaign(context.Background(), pool, mailer, "https://console.dada-tuda.ru", now, fix, first.Campaign)
	sweepFixCampaign(context.Background(), pool, mailer, "https://console.dada-tuda.ru", now.Add(time.Hour), fix, first.Campaign)

	if got := sendsTo(mailer, email); got != 1 {
		t.Fatalf("letters after two passes=%d want 1", got)
	}
}

func TestSweepReactivationFix_HoldsTheQuietPeriodAfterTheFirstLetter(t *testing.T) {
	pool := growthTestPool(t)
	first := growthSpec(t, pool)
	fix := growthFixSpec(t, pool)
	now := time.Now().UTC()
	ancient := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

	recent, _, _, _, recentEmail := growthAccount(t, pool, ancient)
	seedPromo(t, pool, first, recent, recentEmail, now.Add(-24*time.Hour))
	markRedeemed(t, pool, first, recent, now.Add(-23*time.Hour))

	old, _, _, _, oldEmail := growthAccount(t, pool, ancient)
	seedPromo(t, pool, first, old, oldEmail, now.Add(-reactivationWaveQuiet-time.Hour))
	markRedeemed(t, pool, first, old, now.Add(-reactivationWaveQuiet))

	mailer := &recordingMailer{}
	sweepFixCampaign(context.Background(), pool, mailer, "https://console.example", now, fix, first.Campaign)

	for _, sent := range mailer.sends {
		if sent.to == recentEmail {
			t.Fatalf("second letter landed %v after the first, inside the %v quiet period", 24*time.Hour, reactivationWaveQuiet)
		}
	}
	found := false
	for _, sent := range mailer.sends {
		if sent.to == oldEmail {
			found = true
		}
	}
	if !found {
		t.Fatalf("recipient past the quiet period was never mailed, got %v", mailer.sends)
	}
}
