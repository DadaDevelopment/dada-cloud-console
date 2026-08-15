package yookassa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func testProviderPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping yookassa provider DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedPendingPayment(t *testing.T, pool *pgxpool.Pool, orgID, plan, ykPaymentID string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, yk_payment_id, customer_email, created_by_sub)
		VALUES ($1, $2, $3, '990.00', 'RUB', 'pending', $4, 'buyer@example.com', 'sub-test')
	`, id, orgID, plan, ykPaymentID)
	if err != nil {
		t.Fatalf("seed pending payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})
	return id
}

func paymentStatusOf(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM payments WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read payment status: %v", err)
	}
	return status
}

func billingPlanOf(t *testing.T, pool *pgxpool.Pool, orgID string) (string, bool) {
	t.Helper()
	var plan string
	err := pool.QueryRow(context.Background(), `SELECT plan FROM billing_accounts WHERE org_id = $1`, orgID).Scan(&plan)
	if err != nil {
		return "", false
	}
	return plan, true
}

func newFakeYooKassaServer(t *testing.T, status string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Payment{
			ID:     "yk_" + r.URL.Path,
			Status: status,
			Paid:   status == "succeeded",
			Amount: Amount{Value: "990.00", Currency: "RUB"},
			Confirmation: Confirmation{
				Type: "redirect",
				URL:  "https://yoomoney.ru/checkout/payments/v2/contract?orderId=" + r.URL.Path,
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := New("shop", "secret")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()
	return c
}

func TestProcessWebhook_SpoofedSucceeded_AuthoritativeStatusIsPending_NoFlip(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-spoof-" + uuid.NewString()[:8]
	ykID := "ykid-" + uuid.NewString()[:8]
	id := seedPendingPayment(t, pool, orgID, "startup", ykID)

	client := newFakeYooKassaServer(t, "pending")
	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)

	result, err := p.ProcessWebhook(context.Background(), ykID)
	if err != nil {
		t.Fatalf("ProcessWebhook: %v", err)
	}
	if result.Outcome != OutcomeNoop {
		t.Fatalf("Outcome=%q want %q (payload claiming succeeded must NOT be trusted; authoritative refetch says pending)", result.Outcome, OutcomeNoop)
	}
	if status := paymentStatusOf(t, pool, id); status != "pending" {
		t.Fatalf("payments row status=%q want still pending -- spoofed webhook must not flip it", status)
	}
	if _, ok := billingPlanOf(t, pool, orgID); ok {
		t.Fatal("billing_accounts row was created despite the payment never actually succeeding")
	}
}

func TestProcessWebhook_Succeeded_FlipsPaymentAndAssignsPlan(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-ok-" + uuid.NewString()[:8]
	ykID := "ykid-" + uuid.NewString()[:8]
	id := seedPendingPayment(t, pool, orgID, "startup", ykID)

	client := newFakeYooKassaServer(t, "succeeded")
	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)

	result, err := p.ProcessWebhook(context.Background(), ykID)
	if err != nil {
		t.Fatalf("ProcessWebhook: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Fatalf("Outcome=%q want %q", result.Outcome, OutcomeSucceeded)
	}
	if status := paymentStatusOf(t, pool, id); status != "succeeded" {
		t.Fatalf("payments row status=%q want succeeded", status)
	}
	plan, ok := billingPlanOf(t, pool, orgID)
	if !ok {
		t.Fatal("billing_accounts row was not created after a successful payment")
	}
	if plan != "startup" {
		t.Fatalf("billing_accounts.plan=%q want startup", plan)
	}
}

func TestProcessWebhook_Canceled_FlipsPaymentOnly(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-cancel-" + uuid.NewString()[:8]
	ykID := "ykid-" + uuid.NewString()[:8]
	id := seedPendingPayment(t, pool, orgID, "startup", ykID)

	client := newFakeYooKassaServer(t, "canceled")
	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)

	result, err := p.ProcessWebhook(context.Background(), ykID)
	if err != nil {
		t.Fatalf("ProcessWebhook: %v", err)
	}
	if result.Outcome != OutcomeCanceled {
		t.Fatalf("Outcome=%q want %q", result.Outcome, OutcomeCanceled)
	}
	if status := paymentStatusOf(t, pool, id); status != "canceled" {
		t.Fatalf("payments row status=%q want canceled", status)
	}
	if _, ok := billingPlanOf(t, pool, orgID); ok {
		t.Fatal("billing_accounts row must not be created for a canceled payment")
	}
}

func TestProcessWebhook_DoubleDelivery_IsIdempotent(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-idem-" + uuid.NewString()[:8]
	ykID := "ykid-" + uuid.NewString()[:8]
	id := seedPendingPayment(t, pool, orgID, "startup", ykID)

	client := newFakeYooKassaServer(t, "succeeded")
	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)

	first, err := p.ProcessWebhook(context.Background(), ykID)
	if err != nil {
		t.Fatalf("ProcessWebhook (1st): %v", err)
	}
	if first.Outcome != OutcomeSucceeded {
		t.Fatalf("1st Outcome=%q want %q", first.Outcome, OutcomeSucceeded)
	}

	second, err := p.ProcessWebhook(context.Background(), ykID)
	if err != nil {
		t.Fatalf("ProcessWebhook (2nd): %v", err)
	}
	if second.Outcome != OutcomeAlreadyProcessed {
		t.Fatalf("2nd delivery Outcome=%q want %q (must be a no-op replay, not re-applied)", second.Outcome, OutcomeAlreadyProcessed)
	}
	if status := paymentStatusOf(t, pool, id); status != "succeeded" {
		t.Fatalf("payments row status=%q want succeeded after replay", status)
	}
}

func TestProcessWebhook_UnknownPayment_ReturnsUnknownOutcome(t *testing.T) {
	pool := testProviderPool(t)
	client := newFakeYooKassaServer(t, "succeeded")
	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)

	result, err := p.ProcessWebhook(context.Background(), "ykid-does-not-exist-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("ProcessWebhook: %v", err)
	}
	if result.Outcome != OutcomeUnknownPayment {
		t.Fatalf("Outcome=%q want %q", result.Outcome, OutcomeUnknownPayment)
	}
}

func TestCheckout_InsertsPendingRowThenStoresYkPaymentID(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-checkout-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
	})

	client := newFakeYooKassaServer(t, "pending")
	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)
	plan := pricing.Plan{Key: "startup", Name: "Startup", PriceRUB: 990}

	paymentID, confirmationURL, err := p.Checkout(context.Background(), orgID, plan, "buyer@example.com", "sub-checkout", uuid.NewString(), false, uuid.Nil)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if paymentID == "" {
		t.Fatal("Checkout returned empty payment id")
	}
	if confirmationURL == "" {
		t.Fatal("Checkout returned empty confirmation url")
	}

	var status, ykPaymentID string
	var storedConfirmationURL string
	err = pool.QueryRow(context.Background(),
		`SELECT status, yk_payment_id, confirmation_url FROM payments WHERE id = $1`, paymentID,
	).Scan(&status, &ykPaymentID, &storedConfirmationURL)
	if err != nil {
		t.Fatalf("read seeded payment row: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status=%q want pending", status)
	}
	if ykPaymentID == "" {
		t.Fatal("yk_payment_id was not stored")
	}
	if storedConfirmationURL != confirmationURL {
		t.Fatalf("stored confirmation_url=%q want %q", storedConfirmationURL, confirmationURL)
	}
}

// TestCheckout_ProviderCreateFails_MarksRowCanceledInsteadOfLeavingBarePending
// pins the P0-PAY-CHECKOUT regression: a real payer's two checkout attempts on
// 2026-08-14 both inserted a pending payments row and then failed the
// YooKassa create call, leaving both rows stuck at status=pending with empty
// yk_payment_id/confirmation_url forever -- no webhook could ever arrive for
// a payment YooKassa never created, so nothing would ever move that row again.
// Checkout must now mark the row canceled on a create failure, mirroring
// ChargeSaved, so a failed attempt is a terminal, visible row rather than a
// silent stall that looks identical to "still in flight".
func TestCheckout_ProviderCreateFails_MarksRowCanceledInsteadOfLeavingBarePending(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-checkout-fail-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
	})

	client := declineServer(t)
	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)
	plan := pricing.Plan{Key: "startup", Name: "Startup", PriceRUB: 990}

	paymentID, confirmationURL, err := p.Checkout(context.Background(), orgID, plan, "buyer@example.com", "sub-checkout-fail", uuid.NewString(), false, uuid.Nil)
	if err == nil {
		t.Fatal("Checkout returned no error for a declined create call")
	}
	if paymentID != "" || confirmationURL != "" {
		t.Fatalf("Checkout returned paymentID=%q confirmationURL=%q on failure, want both empty", paymentID, confirmationURL)
	}

	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payments WHERE org_id = $1`, orgID,
	).Scan(&rows); err != nil {
		t.Fatalf("count payment rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("payment rows for org=%d, want exactly 1 (the failed attempt)", rows)
	}

	var status, ykPaymentID, storedConfirmationURL string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, coalesce(yk_payment_id, ''), coalesce(confirmation_url, '') FROM payments WHERE org_id = $1`, orgID,
	).Scan(&status, &ykPaymentID, &storedConfirmationURL); err != nil {
		t.Fatalf("read failed checkout row: %v", err)
	}
	if status == "pending" {
		t.Fatal("payment row is still pending after a failed create call; it can never receive a webhook and would be stuck forever")
	}
	if status != "canceled" {
		t.Fatalf("status=%q want canceled", status)
	}
	if ykPaymentID != "" {
		t.Fatalf("yk_payment_id=%q want empty; YooKassa never created this payment", ykPaymentID)
	}
	if storedConfirmationURL != "" {
		t.Fatalf("confirmation_url=%q want empty; YooKassa never created this payment", storedConfirmationURL)
	}
}

// TestCheckout_ProviderCreateFails_LeavesAuditTrailAndBumpsMetric pins the
// observability half of P0-PAY-CHECKOUT (sess-0815b): the row is now marked
// canceled (previous test), but before this test that terminal row was the
// ONLY trace a failed checkout ever left -- no audit_events row, no metric,
// nothing an operator could alert on. artempro2021@bk.ru's 2026-08-14 failed
// checkouts were found a day later by hand-scanning the payments table.
// This asserts the audit row lands in the SAME transaction as the canceled
// mark (both present or, on any DB failure, neither -- checked here as "both
// present, since the happy path of the tx must commit both together") and
// that the Prometheus counter moves.
func TestCheckout_ProviderCreateFails_LeavesAuditTrailAndBumpsMetric(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-checkout-audit-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE resource_kind = 'Payment' AND resource_name = $1`, orgID)
	})

	before := testutil.ToFloat64(metrics.PaymentCreateFailuresCollectorForTest("yk_invalid_request"))

	client := declineServer(t)
	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)
	plan := pricing.Plan{Key: "startup", Name: "Startup", PriceRUB: 990}

	_, _, err := p.Checkout(context.Background(), orgID, plan, "buyer@example.com", "sub-checkout-audit", uuid.NewString(), false, uuid.Nil)
	if err == nil {
		t.Fatal("Checkout returned no error for a declined create call")
	}

	var paymentRowID, status string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text, status FROM payments WHERE org_id = $1`, orgID,
	).Scan(&paymentRowID, &status); err != nil {
		t.Fatalf("read failed checkout row: %v", err)
	}
	if status != "canceled" {
		t.Fatalf("status=%q want canceled", status)
	}

	var count int
	var action, outcome, actorID string
	var metadata []byte
	err = pool.QueryRow(context.Background(), `
		SELECT count(*), max(action), max(outcome), max(actor_id::text), max(metadata::text)
		FROM audit_events WHERE resource_kind = 'Payment' AND resource_name = $1
	`, orgID).Scan(&count, &action, &outcome, &actorID, &metadata)
	if err != nil {
		t.Fatalf("read audit_events row: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit_events rows for org=%d, want exactly 1 -- the failed checkout left no durable trail outside the payments table", count)
	}
	if action != "CreatePaymentFailed" {
		t.Fatalf("action=%q want CreatePaymentFailed", action)
	}
	if outcome != "failure" {
		t.Fatalf("outcome=%q want failure", outcome)
	}
	if actorID != uuid.Nil.String() {
		t.Fatalf("actor_id=%q want %q", actorID, uuid.Nil.String())
	}

	var meta map[string]string
	if err := json.Unmarshal(metadata, &meta); err != nil {
		t.Fatalf("unmarshal audit metadata: %v", err)
	}
	if meta["payment_id"] != paymentRowID {
		t.Fatalf("metadata payment_id=%q want %q (the canceled payments row)", meta["payment_id"], paymentRowID)
	}
	if meta["plan"] != "startup" {
		t.Fatalf("metadata plan=%q want startup", meta["plan"])
	}
	if meta["error_class"] == "" {
		t.Fatal("metadata missing error_class")
	}

	after := testutil.ToFloat64(metrics.PaymentCreateFailuresCollectorForTest(meta["error_class"]))
	if after <= before {
		t.Fatalf("dada_payment_create_failures_total{error_class=%q} did not increase: before=%v after=%v", meta["error_class"], before, after)
	}
}

func planExpiryOf(t *testing.T, pool *pgxpool.Pool, orgID string) *time.Time {
	t.Helper()
	var expires *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT plan_expires_at FROM billing_accounts WHERE org_id = $1`, orgID).Scan(&expires); err != nil {
		t.Fatalf("read plan_expires_at: %v", err)
	}
	return expires
}

func TestProcessWebhook_Succeeded_SetsThirtyDayExpiry(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-exp-" + uuid.NewString()[:8]
	ykID := "ykid-" + uuid.NewString()[:8]
	seedPendingPayment(t, pool, orgID, "startup", ykID)

	client := newFakeYooKassaServer(t, "succeeded")
	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)

	if _, err := p.ProcessWebhook(context.Background(), ykID); err != nil {
		t.Fatalf("ProcessWebhook: %v", err)
	}

	expires := planExpiryOf(t, pool, orgID)
	if expires == nil {
		t.Fatal("plan_expires_at is NULL after a paid assignment; want ~now+30d")
	}
	want := time.Now().UTC().Add(30 * 24 * time.Hour)
	if diff := expires.Sub(want); diff < -time.Hour || diff > time.Hour {
		t.Fatalf("plan_expires_at=%s want within 1h of %s", expires, want)
	}
}

func TestProcessWebhook_Renewal_ExtendsFromCurrentExpiry(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-renew-" + uuid.NewString()[:8]
	ykID := "ykid-" + uuid.NewString()[:8]
	seedPendingPayment(t, pool, orgID, "startup", ykID)

	remaining := 10 * 24 * time.Hour
	current := time.Now().UTC().Add(remaining)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, expiry_notified_at, updated_at)
		VALUES ($1, 'startup', now(), $2, now(), now())
	`, orgID, current); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}

	client := newFakeYooKassaServer(t, "succeeded")
	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)
	if _, err := p.ProcessWebhook(context.Background(), ykID); err != nil {
		t.Fatalf("ProcessWebhook: %v", err)
	}

	expires := planExpiryOf(t, pool, orgID)
	if expires == nil {
		t.Fatal("plan_expires_at is NULL after renewal")
	}
	want := current.Add(30 * 24 * time.Hour)
	if diff := expires.Sub(want); diff < -time.Hour || diff > time.Hour {
		t.Fatalf("early renewal must extend from the current expiry: plan_expires_at=%s want within 1h of %s", expires, want)
	}

	var notified *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT expiry_notified_at FROM billing_accounts WHERE org_id = $1`, orgID).Scan(&notified); err != nil {
		t.Fatalf("read expiry_notified_at: %v", err)
	}
	if notified != nil {
		t.Fatal("expiry_notified_at must reset on renewal so reminders re-arm for the new term")
	}
}
