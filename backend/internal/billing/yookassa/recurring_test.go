package yookassa

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
)

// captureBody runs one CreatePayment against a fake YooKassa and returns the
// request body it sent, decoded as a generic map. The map matters: these
// tests are about which JSON keys reach YooKassa, and a typed struct would
// hide exactly the mistake they exist to catch (a field silently serialised
// when it must be absent).
func captureBody(t *testing.T, req CreatePaymentRequest, respond func(w http.ResponseWriter)) map[string]any {
	t.Helper()
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("request body is not JSON: %v (%s)", err, raw)
		}
		respond(w)
	})
	if _, err := c.CreatePayment(context.Background(), "idem-key", req); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	return got
}

func okPending(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(Payment{ID: "pay_1", Status: "pending"})
}

func TestCreatePayment_FirstPayment_AsksToSaveTheMethod(t *testing.T) {
	body := captureBody(t, CreatePaymentRequest{
		Amount:            Amount{Value: "990.00", Currency: "RUB"},
		Capture:           true,
		Confirmation:      &Confirmation{Type: "redirect", ReturnURL: "https://console.dada-tuda.ru/billing/return"},
		SavePaymentMethod: true,
	}, okPending)

	if body["save_payment_method"] != true {
		t.Fatalf("save_payment_method=%v want true; without it YooKassa returns no reusable method and autopay can never arm", body["save_payment_method"])
	}
	if _, ok := body["confirmation"]; !ok {
		t.Fatal("confirmation missing from a customer-present payment; the payer would have nowhere to confirm")
	}
	if _, ok := body["payment_method_id"]; ok {
		t.Fatal("payment_method_id sent on a first payment; that is the recurring flow, not this one")
	}
}

func TestCreatePayment_Recurring_OmitsConfirmationAndSaveFlag(t *testing.T) {
	body := captureBody(t, CreatePaymentRequest{
		Amount:          Amount{Value: "990.00", Currency: "RUB"},
		Capture:         true,
		PaymentMethodID: "pm_saved_42",
	}, okPending)

	if body["payment_method_id"] != "pm_saved_42" {
		t.Fatalf("payment_method_id=%v want pm_saved_42", body["payment_method_id"])
	}
	if _, ok := body["confirmation"]; ok {
		t.Fatal("confirmation sent alongside payment_method_id; YooKassa rejects that combination and nobody is present to confirm anyway")
	}
	if _, ok := body["save_payment_method"]; ok {
		t.Fatal("save_payment_method sent on a recurring charge; the method is already saved")
	}
}

func TestCreatePayment_Receipt_CarriesFiscalFields(t *testing.T) {
	p := &YooKassaProvider{SendReceipt: true, VatCode: 1, TaxSystemCode: 2}
	amount := Amount{Value: "990.00", Currency: "RUB"}
	body := captureBody(t, CreatePaymentRequest{
		Amount:  amount,
		Capture: true,
		Receipt: p.receiptFor(pricing.Plan{Key: "startup", Name: "Startup", PriceRUB: 990}, amount, "buyer@example.com"),
	}, okPending)

	receipt, ok := body["receipt"].(map[string]any)
	if !ok {
		t.Fatalf("receipt missing from body=%v; without it no fiscal receipt is issued and 54-FZ is not met", body)
	}
	customer, _ := receipt["customer"].(map[string]any)
	if customer["email"] != "buyer@example.com" {
		t.Fatalf("receipt customer=%v want the payer's email", customer)
	}
	if receipt["tax_system_code"] != float64(2) {
		t.Fatalf("tax_system_code=%v want 2 (configured)", receipt["tax_system_code"])
	}
	items, _ := receipt["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("receipt items=%v want exactly one line", items)
	}
	item, _ := items[0].(map[string]any)
	if item["vat_code"] != float64(1) {
		t.Fatalf("vat_code=%v want 1 (configured)", item["vat_code"])
	}
	if item["amount"].(map[string]any)["value"] != "990.00" {
		t.Fatalf("receipt line amount=%v must equal the charged amount, or the receipt is invalid", item["amount"])
	}
}

func TestReceiptFor_OffOrNoEmail_IsNil(t *testing.T) {
	plan := pricing.Plan{Key: "startup", Name: "Startup", PriceRUB: 990}
	amount := Amount{Value: "990.00", Currency: "RUB"}

	off := &YooKassaProvider{SendReceipt: false, VatCode: 1}
	if r := off.receiptFor(plan, amount, "buyer@example.com"); r != nil {
		t.Fatal("receipt built while fiscalization is off; a shop without it enabled rejects the whole payment")
	}
	noEmail := &YooKassaProvider{SendReceipt: true, VatCode: 1}
	if r := noEmail.receiptFor(plan, amount, ""); r != nil {
		t.Fatal("receipt built without a delivery address; YooKassa rejects it and the payment fails")
	}
}

func TestReceiptFor_SingleTaxSystem_OmitsTaxSystemCode(t *testing.T) {
	p := &YooKassaProvider{SendReceipt: true, VatCode: 1, TaxSystemCode: 0}
	amount := Amount{Value: "990.00", Currency: "RUB"}
	body := captureBody(t, CreatePaymentRequest{
		Amount:  amount,
		Capture: true,
		Receipt: p.receiptFor(pricing.Plan{Key: "startup", Name: "Startup"}, amount, "buyer@example.com"),
	}, okPending)

	receipt := body["receipt"].(map[string]any)
	if _, ok := receipt["tax_system_code"]; ok {
		t.Fatal("tax_system_code sent for a single-tax-system shop; YooKassa rejects it")
	}
}

func TestGetPayment_ReadsSavedPaymentMethod(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "pay_9",
			"status": "succeeded",
			"paid": true,
			"amount": {"value": "990.00", "currency": "RUB"},
			"payment_method": {"id": "pm_saved_9", "type": "bank_card", "saved": true, "title": "Bank card *4444"}
		}`))
	})

	payment, err := c.GetPayment(context.Background(), "pay_9")
	if err != nil {
		t.Fatalf("GetPayment: %v", err)
	}
	if !payment.PaymentMethod.Saved || payment.PaymentMethod.ID != "pm_saved_9" {
		t.Fatalf("payment_method=%+v want saved=true id=pm_saved_9; this is the only handle autopay ever gets", payment.PaymentMethod)
	}
	if payment.PaymentMethod.Title != "Bank card *4444" {
		t.Fatalf("payment_method title=%q want the display string shown in the console", payment.PaymentMethod.Title)
	}
}
