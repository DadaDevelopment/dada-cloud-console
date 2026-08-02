package yookassa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func startupPlan() pricing.Plan {
	return pricing.Plan{Key: "startup", Name: "Startup", PriceRUB: 990}
}

// seedPaidAccount puts an org on a paid plan with a term already running, the
// state every renewal starts from.
func seedPaidAccount(t *testing.T, pool *pgxpool.Pool, orgID string, expiresAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, updated_at)
		VALUES ($1, 'startup', now(), $2, now())
	`, orgID, expiresAt); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})
}

// declineServer answers every create with the YooKassa error envelope a dead
// card produces.
func declineServer(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"type":        "error",
			"code":        "invalid_request",
			"description": "Payment method is expired",
		})
	}))
	t.Cleanup(srv.Close)
	c := New("shop", "secret")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()
	return c
}

func TestChargeSaved_Succeeded_ExtendsTermAndRecordsRecurringPayment(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-autopay-ok-" + uuid.NewString()[:8]
	expiresAt := time.Now().UTC().Add(20 * time.Hour).Truncate(time.Microsecond)
	seedPaidAccount(t, pool, orgID, expiresAt)

	p := NewProvider(pool, newFakeYooKassaServer(t, "succeeded"), "https://console.dada-tuda.ru/billing/return", false, 1, 0)
	result, err := p.ChargeSaved(context.Background(), orgID, startupPlan(), "pm_saved_1", "buyer@example.com")
	if err != nil {
		t.Fatalf("ChargeSaved: %v", err)
	}
	if result.Outcome != ChargeSucceeded {
		t.Fatalf("outcome=%q want %q", result.Outcome, ChargeSucceeded)
	}

	var status string
	var isRecurring bool
	if err := pool.QueryRow(context.Background(),
		`SELECT status, is_recurring FROM payments WHERE id = $1`, result.PaymentID,
	).Scan(&status, &isRecurring); err != nil {
		t.Fatalf("read recurring payment: %v", err)
	}
	if status != "succeeded" || !isRecurring {
		t.Fatalf("payment status=%q is_recurring=%t want succeeded/true", status, isRecurring)
	}

	var newExpiry time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT plan_expires_at FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&newExpiry); err != nil {
		t.Fatalf("read new term: %v", err)
	}
	if !newExpiry.After(expiresAt.Add(29 * 24 * time.Hour)) {
		t.Fatalf("plan_expires_at=%s want roughly %s (previous term + 30 days); a renewal that does not extend the term is money taken for nothing",
			newExpiry.Format(time.RFC3339), expiresAt.Add(30*24*time.Hour).Format(time.RFC3339))
	}
}

// TestChargeSaved_Declined_LeavesTermAloneAndReportsReason asserts a declined
// charge changes nothing about the running term. The seeded expiry is truncated
// to microseconds because that is all a Postgres timestamptz keeps: seeding a
// nanosecond-precision instant makes the read-back equality unreachable and the
// test fails on rounding rather than on behaviour.
func TestChargeSaved_Declined_LeavesTermAloneAndReportsReason(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-autopay-decline-" + uuid.NewString()[:8]
	expiresAt := time.Now().UTC().Add(20 * time.Hour).Truncate(time.Microsecond)
	seedPaidAccount(t, pool, orgID, expiresAt)

	p := NewProvider(pool, declineServer(t), "https://console.dada-tuda.ru/billing/return", false, 1, 0)
	result, err := p.ChargeSaved(context.Background(), orgID, startupPlan(), "pm_dead", "buyer@example.com")
	if err != nil {
		t.Fatalf("ChargeSaved returned a hard error for a declined card: %v", err)
	}
	if result.Outcome != ChargeFailed {
		t.Fatalf("outcome=%q want %q", result.Outcome, ChargeFailed)
	}
	if result.Reason != "Payment method is expired" {
		t.Fatalf("reason=%q want the YooKassa description, which is what the customer is told", result.Reason)
	}

	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM payments WHERE id = $1`, result.PaymentID,
	).Scan(&status); err != nil {
		t.Fatalf("read failed payment: %v", err)
	}
	if status != "canceled" {
		t.Fatalf("payment status=%q want canceled; a pending row left behind would be retried forever", status)
	}

	var expiry time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT plan_expires_at FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&expiry); err != nil {
		t.Fatalf("read term: %v", err)
	}
	if !expiry.Equal(expiresAt) {
		t.Fatalf("plan_expires_at moved to %s after a DECLINED charge; the term must only extend when money actually arrived", expiry)
	}
}

func TestChargeSaved_NoSavedMethod_Refuses(t *testing.T) {
	p := &YooKassaProvider{}
	if _, err := p.ChargeSaved(context.Background(), "org", startupPlan(), "", ""); err == nil {
		t.Fatal("ChargeSaved accepted an empty payment method; that is a create-payment call that can only fail at the provider")
	}
}

func TestProcessWebhook_SucceededWithSavedMethod_ArmsAutopay(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-arm-" + uuid.NewString()[:8]
	ykID := "ykid-" + uuid.NewString()[:8]
	seedPendingPayment(t, pool, orgID, "startup", ykID)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "` + ykID + `",
			"status": "succeeded",
			"paid": true,
			"amount": {"value": "990.00", "currency": "RUB"},
			"payment_method": {"id": "pm_saved_web", "type": "bank_card", "saved": true, "title": "Bank card *4444"}
		}`))
	}))
	t.Cleanup(srv.Close)
	client := New("shop", "secret")
	client.BaseURL = srv.URL
	client.HTTPClient = srv.Client()

	p := NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)
	result, err := p.ProcessWebhook(context.Background(), ykID)
	if err != nil {
		t.Fatalf("ProcessWebhook: %v", err)
	}
	if result.Outcome != OutcomeSucceeded || !result.AutopayArmed {
		t.Fatalf("outcome=%q autopayArmed=%t want succeeded/true", result.Outcome, result.AutopayArmed)
	}

	var enabled bool
	var methodID, methodTitle string
	if err := pool.QueryRow(context.Background(), `
		SELECT autopay_enabled, autopay_method_id, autopay_method_title FROM billing_accounts WHERE org_id = $1
	`, orgID).Scan(&enabled, &methodID, &methodTitle); err != nil {
		t.Fatalf("read autopay state: %v", err)
	}
	if !enabled || methodID != "pm_saved_web" || methodTitle != "Bank card *4444" {
		t.Fatalf("autopay enabled=%t method=%q title=%q want the saved method persisted; without it the plan can never renew itself",
			enabled, methodID, methodTitle)
	}
}

func TestProcessWebhook_SucceededWithoutSavedMethod_LeavesAutopayOff(t *testing.T) {
	pool := testProviderPool(t)
	orgID := "org-noarm-" + uuid.NewString()[:8]
	ykID := "ykid-" + uuid.NewString()[:8]
	seedPendingPayment(t, pool, orgID, "startup", ykID)

	p := NewProvider(pool, newFakeYooKassaServer(t, "succeeded"), "https://console.dada-tuda.ru/billing/return", false, 1, 0)
	result, err := p.ProcessWebhook(context.Background(), ykID)
	if err != nil {
		t.Fatalf("ProcessWebhook: %v", err)
	}
	if result.AutopayArmed {
		t.Fatal("autopay armed from a payment that saved nothing; a recurring charge with no consent is a chargeback")
	}

	var enabled bool
	var methodID string
	if err := pool.QueryRow(context.Background(), `
		SELECT autopay_enabled, autopay_method_id FROM billing_accounts WHERE org_id = $1
	`, orgID).Scan(&enabled, &methodID); err != nil {
		t.Fatalf("read autopay state: %v", err)
	}
	if enabled || methodID != "" {
		t.Fatalf("autopay enabled=%t method=%q want off/empty", enabled, methodID)
	}
}
