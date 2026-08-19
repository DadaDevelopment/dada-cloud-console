package tbank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// realStatementBody is a verbatim (trimmed) GET /statement response captured
// from the production T-Bank Business API on 2026-08-19. It documents the
// wire shape the client must survive: amounts are JSON numbers, the payer's
// text lives in payPurpose while description carries only a short bank-side
// label, and direction is typeOfOperation.
const realStatementBody = `{"operations":[
 {"operationDate":"2026-07-28T00:56:49Z","operationId":"11bdd4c5-f727-0091-938a-5ab4b547c926",
  "operationStatus":"Transaction","accountNumber":"40702810310001995295","typeOfOperation":"Debit",
  "category":"fee","priority":5,"operationAmount":1990,"operationCurrencyDigitalCode":"643",
  "accountAmount":1990,"accountCurrencyDigitalCode":"643","rubleAmount":1990.0,
  "description":"Плата за обслуживание счета",
  "payPurpose":"Плата за обслуживание счета. Договор 7127308335",
  "payer":{"acct":"40702810310001995295","inn":"7807402712","name":"ООО ДАДА ДЕВЕЛОПМЕНТ"}},
 {"operationDate":"2026-08-19T09:00:00Z","operationId":"22cc0000-1111-2222-3333-444455556666",
  "operationStatus":"Transaction","accountNumber":"40702810310001995295","typeOfOperation":"Credit",
  "operationAmount":4900,"operationCurrencyDigitalCode":"643",
  "accountAmount":4900,"accountCurrencyDigitalCode":"643","rubleAmount":4900.0,
  "description":"Перевод",
  "payPurpose":"Оплата по счету INV-2026-00042 от 19.08.2026. НДС не облагается",
  "payer":{"acct":"40702810900000000001","inn":"7707083893","name":"ООО РОМАШКА"}}
]}`

func newStatementServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("from"); got == "" || len(got) <= len("2006-01-02") {
			t.Errorf("from must be a full RFC3339 timestamp, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
}

func TestStatementDecodesProductionWireShape(t *testing.T) {
	srv := newStatementServer(t, realStatementBody)
	defer srv.Close()

	c := &Client{Token: "x", BaseURL: srv.URL}
	ops, err := c.Statement(context.Background(), "40702810310001995295",
		time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Statement: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("want 2 operations, got %d", len(ops))
	}

	fee := ops[0]
	if fee.Amount != 1990 {
		t.Errorf("fee amount = %v, want 1990 (amounts arrive as JSON numbers)", fee.Amount)
	}
	if fee.Purpose != "Плата за обслуживание счета. Договор 7127308335" {
		t.Errorf("fee purpose = %q, want payPurpose not description", fee.Purpose)
	}
	if fee.IsSettledCredit() {
		t.Error("a Debit must never be eligible to settle an invoice")
	}

	paid := ops[1]
	if paid.Amount != 4900 {
		t.Errorf("credit amount = %v, want 4900", paid.Amount)
	}
	if !paid.IsSettledCredit() {
		t.Error("a settled Credit must be eligible")
	}
	if paid.PayerINN != "7707083893" {
		t.Errorf("payer inn = %q, want 7707083893", paid.PayerINN)
	}
	if got := invoiceNumberPattern.FindString(paid.Purpose); got != "INV-2026-00042" {
		t.Errorf("invoice number from purpose = %q, want INV-2026-00042", got)
	}
}

func TestUnsettledOperationIsNotEligible(t *testing.T) {
	op := StatementOperation{Type: "Credit", Status: "Authorization"}
	if op.IsSettledCredit() {
		t.Error("an authorization hold is not money that landed")
	}
}
