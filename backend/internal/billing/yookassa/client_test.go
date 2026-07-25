package yookassa

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("shop_123", "secret_abc")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()
	return c
}

func TestCreatePayment_SetsBasicAuthAndIdempotenceKey(t *testing.T) {
	var gotAuth, gotIdempotence, gotMethod, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotIdempotence = r.Header.Get("Idempotence-Key")
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Payment{ID: "pay_1", Status: "pending"})
	})

	_, err := c.CreatePayment(context.Background(), "idem-key-1", CreatePaymentRequest{
		Amount: Amount{Value: "990.00", Currency: "RUB"},
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/payments" {
		t.Fatalf("method=%s path=%s want POST /payments", gotMethod, gotPath)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("shop_123:secret_abc"))
	if gotAuth != wantAuth {
		t.Fatalf("Authorization=%q want %q", gotAuth, wantAuth)
	}
	if gotIdempotence != "idem-key-1" {
		t.Fatalf("Idempotence-Key=%q want idem-key-1", gotIdempotence)
	}
}

func TestCreatePayment_HappyPath_DecodesPayment(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Payment{
			ID:     "pay_42",
			Status: "pending",
			Amount: Amount{Value: "990.00", Currency: "RUB"},
			Confirmation: Confirmation{
				Type: "redirect",
				URL:  "https://yoomoney.ru/checkout/payments/v2/contract?orderId=pay_42",
			},
		})
	})

	payment, err := c.CreatePayment(context.Background(), "idem-key-2", CreatePaymentRequest{
		Amount:       Amount{Value: "990.00", Currency: "RUB"},
		Confirmation: Confirmation{Type: "redirect", ReturnURL: "https://console.dada-tuda.ru/billing/return"},
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if payment.ID != "pay_42" || payment.Status != "pending" {
		t.Fatalf("payment=%+v want id=pay_42 status=pending", payment)
	}
	if payment.Confirmation.URL == "" || !strings.Contains(payment.Confirmation.URL, "pay_42") {
		t.Fatalf("confirmation url=%q want it to reference pay_42", payment.Confirmation.URL)
	}
}

func TestGetPayment_HappyPath_NoIdempotenceKeySent(t *testing.T) {
	var gotIdempotence, gotMethod, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotIdempotence = r.Header.Get("Idempotence-Key")
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Payment{ID: "pay_42", Status: "succeeded", Paid: true})
	})

	payment, err := c.GetPayment(context.Background(), "pay_42")
	if err != nil {
		t.Fatalf("GetPayment: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/payments/pay_42" {
		t.Fatalf("method=%s path=%s want GET /payments/pay_42", gotMethod, gotPath)
	}
	if gotIdempotence != "" {
		t.Fatalf("GetPayment must not send Idempotence-Key, got %q", gotIdempotence)
	}
	if payment.Status != "succeeded" || !payment.Paid {
		t.Fatalf("payment=%+v want status=succeeded paid=true", payment)
	}
}

func TestClient_NonSuccessStatus_ReturnsError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(apiError{
			Type:        "error",
			Code:        "invalid_credentials",
			Description: "Invalid shopId or secret key",
		})
	})

	_, err := c.GetPayment(context.Background(), "pay_1")
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
	var ykErr *Error
	if e, ok := err.(*Error); ok {
		ykErr = e
	} else {
		t.Fatalf("error type=%T want *yookassa.Error", err)
	}
	if ykErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode=%d want 401", ykErr.StatusCode)
	}
	if ykErr.Code != "invalid_credentials" {
		t.Fatalf("Code=%q want invalid_credentials", ykErr.Code)
	}
	if !strings.Contains(ykErr.Error(), "invalid_credentials") {
		t.Fatalf("Error() = %q, want it to mention invalid_credentials", ykErr.Error())
	}
}
