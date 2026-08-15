package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedAccountWithCard puts an org on a paid plan with a saved card and live
// autopay -- the state a successful checkout with consent leaves behind.
func seedAccountWithCard(t *testing.T, pool *pgxpool.Pool, orgID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, autopay_enabled, autopay_method_id, autopay_method_title)
		VALUES ($1, 'startup', $2, $3, TRUE, 'pm-1234', 'Карта •••• 4242')
	`, orgID, time.Now().UTC(), time.Now().UTC().Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE resource_name = $1`, orgID)
	})
}

func readAutopayState(t *testing.T, pool *pgxpool.Pool, orgID string) (enabled bool, methodID, methodTitle string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT autopay_enabled, autopay_method_id, autopay_method_title FROM billing_accounts WHERE org_id = $1`,
		orgID,
	).Scan(&enabled, &methodID, &methodTitle); err != nil {
		t.Fatalf("read autopay state: %v", err)
	}
	return
}

// Pausing renewal must not throw the card away. Erasing it conflated two
// decisions and left the user with no way back except a full checkout -- and
// left the console with no payment method to display at all.
func TestSetBillingAutopay_DisableKeepsTheSavedCard(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-autopay-keep-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	seedAccountWithCard(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans()}
	c, rec := newBillingCtx(http.MethodPut, "/", `{"enabled":false}`, godClaims(uuid.New()), projectID)
	h.SetBillingAutopay(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	enabled, methodID, methodTitle := readAutopayState(t, pool, orgID)
	if enabled {
		t.Fatalf("autopay_enabled=true after disable, want false")
	}
	if methodID == "" || methodTitle == "" {
		t.Fatalf("method_id=%q method_title=%q: disabling autopay must not forget the card -- resuming renewal should be one click, not a fresh checkout", methodID, methodTitle)
	}

	var resp struct {
		AutopayEnabled     bool   `json:"autopay_enabled"`
		AutopayMethodTitle string `json:"autopay_method_title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	if resp.AutopayEnabled {
		t.Fatalf("response autopay_enabled=true, want false")
	}
	if resp.AutopayMethodTitle == "" {
		t.Fatalf("response autopay_method_title is empty: the console cannot render a payment-method block for a card the API refuses to name")
	}
}

// The retained card is what makes resuming cheap: enable must succeed right
// after a disable, with no checkout in between.
func TestSetBillingAutopay_CanBeReenabledAfterDisable(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-autopay-resume-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	seedAccountWithCard(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans()}
	off, _ := newBillingCtx(http.MethodPut, "/", `{"enabled":false}`, godClaims(uuid.New()), projectID)
	h.SetBillingAutopay(off)

	on, rec := newBillingCtx(http.MethodPut, "/", `{"enabled":true}`, godClaims(uuid.New()), projectID)
	h.SetBillingAutopay(on)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200: re-arming renewal on a card we still hold must not require a new checkout", rec.Code, rec.Body.String())
	}
	enabled, _, _ := readAutopayState(t, pool, orgID)
	if !enabled {
		t.Fatalf("autopay_enabled=false after re-enable, want true")
	}
}

// Withdrawing the instrument is the other half: the card goes, and consent
// cannot outlive it.
func TestDeleteBillingPaymentMethod_ErasesCardAndDisarmsAutopay(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-detach-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	seedAccountWithCard(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans()}
	c, rec := newBillingCtx(http.MethodDelete, "/", "", godClaims(uuid.New()), projectID)
	h.DeleteBillingPaymentMethod(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	enabled, methodID, methodTitle := readAutopayState(t, pool, orgID)
	if methodID != "" || methodTitle != "" {
		t.Fatalf("method_id=%q method_title=%q, want both empty after detach", methodID, methodTitle)
	}
	if enabled {
		t.Fatalf("autopay_enabled=true after detach: a charge armed against a method we no longer hold can only fail")
	}
	if n := countAuditRows(t, pool, orgID, "PaymentMethodDetached"); n != 1 {
		t.Fatalf("PaymentMethodDetached audit rows = %d, want 1", n)
	}
}

// A second click is not an error -- the account is already in the requested
// state.
func TestDeleteBillingPaymentMethod_IsIdempotent(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-detach-twice-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	seedAccountWithCard(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans()}
	first, _ := newBillingCtx(http.MethodDelete, "/", "", godClaims(uuid.New()), projectID)
	h.DeleteBillingPaymentMethod(first)

	second, rec := newBillingCtx(http.MethodDelete, "/", "", godClaims(uuid.New()), projectID)
	h.DeleteBillingPaymentMethod(second)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200 on a repeated detach", rec.Code, rec.Body.String())
	}
	if n := countAuditRows(t, pool, orgID, "PaymentMethodDetached"); n != 1 {
		t.Fatalf("PaymentMethodDetached audit rows = %d, want 1: a no-op detach must not manufacture an event", n)
	}
}

// The payment history is where an abandoned checkout is recoverable from, so
// pending rows carry their confirmation URL and terminal rows do not.
func TestGetBillingPayments_ExposesConfirmationURLForPendingOnly(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-payresume-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	now := time.Now().UTC()

	seedPendingPayment(t, pool, orgID, "yk-"+uuid.NewString()[:8], now.Add(-5*time.Minute))
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, created_by_sub, yk_payment_id, confirmation_url, created_at, paid_at)
		VALUES ($1, $2, 'startup', '990.00', 'RUB', 'succeeded', 'sub-1', $3, 'https://yoomoney.example/confirm', $4, $4)
	`, uuid.New(), orgID, "yk-"+uuid.NewString()[:8], now.Add(-time.Hour)); err != nil {
		t.Fatalf("seed succeeded payment: %v", err)
	}

	h := &Handler{pool: pool, billingPlans: testPlans()}
	c, rec := newBillingCtx(http.MethodGet, "/", "", godClaims(uuid.New()), projectID)
	h.GetBillingPayments(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Payments []paymentResponse `json:"payments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	if len(resp.Payments) != 2 {
		t.Fatalf("payments=%d, want 2", len(resp.Payments))
	}
	for _, p := range resp.Payments {
		switch p.Status {
		case "pending":
			if p.ConfirmationURL == "" {
				t.Fatalf("pending payment %s has no confirmation_url: without it the console cannot offer to finish a checkout the user walked away from", p.ID)
			}
		default:
			if p.ConfirmationURL != "" {
				t.Fatalf("payment %s status=%s carries confirmation_url=%q: no surface may offer to pay for something already settled", p.ID, p.Status, p.ConfirmationURL)
			}
		}
	}
}
