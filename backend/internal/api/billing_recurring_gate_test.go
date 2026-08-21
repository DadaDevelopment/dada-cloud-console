package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

// TestBillingCheckout_AutopayRequested_FlagOff_422BeforeProvider proves the
// checkout refuses a doomed recurring request before it ever reaches
// YooKassa: the merchant account cannot save a payment method for recurring
// charges today (live audit_events 2026-08-15 21:45:43 UTC, 403 "This store
// can't make recurring payments"). The provider is a real client pointed at
// no server (default BaseURL); if the guard did not fire first, Checkout
// would attempt a real network call and this test would see something other
// than 422 recurring_not_supported with zero rows written.
func TestBillingCheckout_AutopayRequested_FlagOff_422BeforeProvider(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-recur-checkout-off-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans(), yookassa: nonNilProvider(pool), cfg: &config.Config{YooKassaRecurringEnabled: false}}
	c, rec := newBillingCtx(http.MethodPost, "/", `{"plan":"startup","autopay":true}`, godClaims(uuid.New()), projectID)
	h.BillingCheckout(c)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code=%d body=%s want 422 recurring_not_supported before any provider call", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	if body["code"] != "recurring_not_supported" {
		t.Fatalf("code=%q want recurring_not_supported", body["code"])
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM payments WHERE org_id = $1`, orgID,
	).Scan(&count); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if count != 0 {
		t.Fatalf("payments count=%d want 0: the request must be refused BEFORE creating a pending payment row", count)
	}
}

// TestBillingCheckout_NoAutopay_FlagOff_StillProceeds proves the guard is
// scoped to autopay requests only: a plain one-time checkout must keep
// working while recurring is unsupported.
func TestBillingCheckout_NoAutopay_FlagOff_StillProceeds(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-recur-checkout-plain-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})

	client := newFakeYooKassaClient(t, "pending")
	provider := yookassa.NewProvider(pool, client, "https://console.dada-tuda.ru/billing/return", false, 1, 0)
	h := &Handler{pool: pool, billingPlans: testPlans(), yookassa: provider, cfg: &config.Config{YooKassaRecurringEnabled: false}}
	c, rec := newBillingCtx(http.MethodPost, "/", `{"plan":"startup","autopay":false}`, godClaims(uuid.New()), projectID)
	h.BillingCheckout(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200 for a non-recurring checkout while recurring is unsupported", rec.Code, rec.Body.String())
	}
}

// TestSetBillingAutopay_EnableRequested_FlagOff_422 proves PUT autopay
// refuses to arm recurring charges while the merchant cannot honor them, and
// does so before touching billing_accounts.
func TestSetBillingAutopay_EnableRequested_FlagOff_422(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-recur-autopay-on-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, autopay_enabled, updated_at)
		VALUES ($1, 'startup', now(), FALSE, now())
	`, orgID); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})

	h := &Handler{pool: pool, billingPlans: testPlans(), cfg: &config.Config{YooKassaRecurringEnabled: false}}
	c, rec := newBillingCtx(http.MethodPut, "/", `{"enabled":true}`, godClaims(uuid.New()), projectID)
	h.SetBillingAutopay(c)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code=%d body=%s want 422 recurring_not_supported", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	if body["code"] != "recurring_not_supported" {
		t.Fatalf("code=%q want recurring_not_supported", body["code"])
	}

	var enabled bool
	if err := pool.QueryRow(context.Background(),
		`SELECT autopay_enabled FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&enabled); err != nil {
		t.Fatalf("read autopay state: %v", err)
	}
	if enabled {
		t.Fatalf("autopay_enabled=true after a refused enable request; the DB must not have been touched")
	}
}

// TestSetBillingAutopay_DisableRequested_FlagOff_StillSucceeds proves turning
// autopay OFF is never blocked by the recurring-support gate -- only turning
// it on is.
func TestSetBillingAutopay_DisableRequested_FlagOff_StillSucceeds(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-recur-autopay-off-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, autopay_enabled, autopay_method_id, autopay_method_title, updated_at)
		VALUES ($1, 'startup', now(), TRUE, 'pm_stub', 'Bank card *4444', now())
	`, orgID); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})

	h := &Handler{pool: pool, billingPlans: testPlans(), cfg: &config.Config{YooKassaRecurringEnabled: false}}
	c, rec := newBillingCtx(http.MethodPut, "/", `{"enabled":false}`, godClaims(uuid.New()), projectID)
	h.SetBillingAutopay(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200: disabling autopay must always work regardless of the recurring flag", rec.Code, rec.Body.String())
	}
}

// TestGetBillingAccount_AutopaySupported_ReflectsFlag proves the account
// payload carries autopay.supported so the console can hide/gray the
// autopay offer instead of letting a user opt into a doomed request.
func TestGetBillingAccount_AutopaySupported_ReflectsFlag(t *testing.T) {
	pool := testPaymentsPool(t)

	for _, flag := range []bool{false, true} {
		orgID := "org-recur-account-" + uuid.NewString()[:8]
		projectID := seedPaymentsProject(t, pool, orgID)

		h := &Handler{pool: pool, billingPlans: testPlans(), cfg: &config.Config{YooKassaRecurringEnabled: flag}}
		c, rec := newBillingCtx(http.MethodGet, "/", "", godClaims(uuid.New()), projectID)
		h.GetBillingAccount(c)

		if rec.Code != http.StatusOK {
			t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
		}
		var resp struct {
			Autopay struct {
				Supported bool `json:"supported"`
			} `json:"autopay"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
		}
		if resp.Autopay.Supported != flag {
			t.Fatalf("autopay.supported=%t want %t (YooKassaRecurringEnabled=%t)", resp.Autopay.Supported, flag, flag)
		}
	}
}
