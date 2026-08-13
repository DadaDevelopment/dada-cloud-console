package yookassa

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// forbiddenServer fails the test if any request reaches YooKassa. A refusal
// that still calls the API has already lost the point of the guard.
func forbiddenServer(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected call to YooKassa: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	c := New("shop", "secret")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()
	return c
}

// A fiscalising shop cannot charge a payer whose email is unknown: YooKassa
// answers "Receipt is missing or illegal" and the whole payment dies. The
// provider is built with a nil pool on purpose — reaching the pending-row
// INSERT would panic, which is exactly the regression this pins.
func TestCheckout_NoEmailOnFiscalShop_RefusesBeforeAnySideEffect(t *testing.T) {
	p := NewProvider(nil, forbiddenServer(t), "https://console.example/return", true, 1, 0)

	_, _, err := p.Checkout(context.Background(), "org-test", startupPlan(), "", "sub-test", "", false)

	if !errors.Is(err, ErrReceiptEmailRequired) {
		t.Fatalf("Checkout error = %v, want ErrReceiptEmailRequired", err)
	}
}

func TestChargeSaved_NoEmailOnFiscalShop_RefusesBeforeAnySideEffect(t *testing.T) {
	p := NewProvider(nil, forbiddenServer(t), "https://console.example/return", true, 1, 0)

	_, err := p.ChargeSaved(context.Background(), "org-test", startupPlan(), "pm-test", "")

	if !errors.Is(err, ErrReceiptEmailRequired) {
		t.Fatalf("ChargeSaved error = %v, want ErrReceiptEmailRequired", err)
	}
}

// A shop without fiscalization has no receipt to deliver, so an unknown email
// is not a reason to refuse money.
func TestRequireReceiptEmail_NonFiscalShopAcceptsEmptyEmail(t *testing.T) {
	p := NewProvider(nil, nil, "", false, 1, 0)

	if err := p.requireReceiptEmail(""); err != nil {
		t.Fatalf("requireReceiptEmail on non-fiscal shop = %v, want nil", err)
	}
	if err := p.requireReceiptEmail("buyer@example.com"); err != nil {
		t.Fatalf("requireReceiptEmail with email = %v, want nil", err)
	}
}

func TestRequireReceiptEmail_FiscalShopAcceptsKnownEmail(t *testing.T) {
	p := NewProvider(nil, nil, "", true, 1, 0)

	if err := p.requireReceiptEmail("buyer@example.com"); err != nil {
		t.Fatalf("requireReceiptEmail with email = %v, want nil", err)
	}
}
