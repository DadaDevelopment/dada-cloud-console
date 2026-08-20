package fincore

import (
	"context"
	"strings"
	"testing"
	"time"
)

func testUser() CloudUser {
	return CloudUser{
		ID:            "11111111-2222-3333-4444-555555555555",
		Username:      "artempro2021@bk.ru",
		Email:         "artempro2021@bk.ru",
		DisplayName:   "Артём",
		CreatedAt:     time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
		SignupChannel: "yandex",
	}
}

func TestClientExternalIDIsTheUserID(t *testing.T) {
	u := testUser()
	if got := ClientFromUser(u).ExternalID; got != u.ID {
		t.Fatalf("external id = %q, want the console user id %q", got, u.ID)
	}
}

func TestClientFromUserFallsBackWhenDisplayNameIsEmpty(t *testing.T) {
	u := testUser()
	u.DisplayName = ""
	got := ClientFromUser(u)
	if got.ShortName != u.Username {
		t.Fatalf("short_name = %q, want the username %q", got.ShortName, u.Username)
	}
}

func TestIncomeSourceCarriesTheSignupDoor(t *testing.T) {
	u := testUser()
	u.SignupSource = "yandex-direct"
	if got := ClientFromUser(u).IncomeSource; got != "dada_cloud/yandex/yandex-direct" {
		t.Fatalf("income_source = %q", got)
	}

	u.SignupChannel, u.SignupSource = "", ""
	if got := ClientFromUser(u).IncomeSource; got != "dada_cloud" {
		t.Fatalf("income_source without attribution = %q, want plain dada_cloud", got)
	}
}

// A CREDIT item without payer_name is rejected by the ingest DTO's own
// validator, so a payment whose org resolves to nobody must still name someone.
func TestPaymentAlwaysCarriesAPayerName(t *testing.T) {
	paid := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	p := CloudPayment{
		ID:          "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		OrgID:       "dada",
		Plan:        "pro",
		Amount:      "990.00",
		Currency:    "RUB",
		YKPaymentID: "2f0a",
		PaidAt:      paid,
	}

	tx := TransactionFromPayment(p)
	if tx.Direction != DirectionCredit {
		t.Fatalf("direction = %q, want CREDIT", tx.Direction)
	}
	if tx.PayerName == "" {
		t.Fatal("payer_name is empty; the ingest DTO rejects a CREDIT without one")
	}
	if tx.ClientExternal != "" {
		t.Fatalf("client_external_id = %q, want empty for an org that resolves to no user", tx.ClientExternal)
	}
	if tx.Metadata["client_unresolved"] != true {
		t.Fatal("an unlinked payment must say so in metadata, otherwise it reads as a linked one")
	}
	if !strings.HasPrefix(tx.SourceIdentity, "payment:") {
		t.Fatalf("source_identity = %q", tx.SourceIdentity)
	}

	u := testUser()
	p.Owner = &u
	linked := TransactionFromPayment(p)
	if linked.ClientExternal != u.ID {
		t.Fatalf("client_external_id = %q, want %q", linked.ClientExternal, u.ID)
	}
	if linked.PayerName != "Артём" {
		t.Fatalf("payer_name = %q, want the person, not the org slug", linked.PayerName)
	}
	if _, ok := linked.Metadata["client_unresolved"]; ok {
		t.Fatal("a linked payment must not be marked unresolved")
	}
}

func TestPaymentSourceIdentityIsStableAcrossRuns(t *testing.T) {
	p := CloudPayment{ID: "abc", Amount: "1.00", PaidAt: time.Now()}
	if TransactionFromPayment(p).SourceIdentity != TransactionFromPayment(p).SourceIdentity {
		t.Fatal("source_identity changes between runs; a backfill would double-book the money")
	}
}

func TestHostingCostIsMeasuredButNotIngested(t *testing.T) {
	got, reason := (&Syncer{hardwareMonthlyRUB: 1234.50}).collectHostingCost(context.Background())
	if got != 1234.50 || reason != "" {
		t.Fatalf("collectHostingCost = %v, %q; want the configured bill and no excuse", got, reason)
	}

	got, reason = (&Syncer{}).collectHostingCost(context.Background())
	if got != 0 || reason == "" {
		t.Fatalf("collectHostingCost = %v, %q; an absent billing source must say so, not read as a free month", got, reason)
	}
}

func TestFormatAmountNeverEmitsScientificNotation(t *testing.T) {
	if got := FormatAmount(0.0000001); got != "0.00" {
		t.Fatalf("FormatAmount(1e-7) = %q", got)
	}
	if got := FormatAmount(1234567.891); got != "1234567.89" {
		t.Fatalf("FormatAmount = %q", got)
	}
}

// TestClientCarriesPayerRequisitesSoBankTransfersCanBind pins the attribution
// key. FinCore binds an incoming transfer to the client whose card matches the
// statement's payer; a client without an INN can never be credited with money
// that arrived by transfer.
func TestClientCarriesPayerRequisitesSoBankTransfersCanBind(t *testing.T) {
	got := ClientFromUser(CloudUser{
		ID:           "u-1",
		Username:     "acme",
		Email:        "cfo@acme.ru",
		DisplayName:  "Иван Петров",
		INN:          "7807402712",
		KPP:          "780701001",
		OrgName:      `ООО "АКМЕ"`,
		LegalAddress: "Санкт-Петербург, ул. Тестовая, 1",
	})

	if got.INN != "7807402712" {
		t.Fatalf("iin = %q, want the payer INN the console captured", got.INN)
	}
	if got.ShortName != `ООО "АКМЕ"` {
		t.Fatalf("short_name = %q, want the legal entity that pays", got.ShortName)
	}
	if !strings.Contains(got.Requisites, "ИНН 7807402712") || !strings.Contains(got.Requisites, "КПП 780701001") {
		t.Fatalf("requisites = %q, want the payer's INN and KPP", got.Requisites)
	}
	if got.ContactPerson != "Иван Петров" {
		t.Fatalf("contact_person = %q, want the person behind the org", got.ContactPerson)
	}

	person := ClientFromUser(CloudUser{ID: "u-2", Username: "solo", DisplayName: "Соло Разработчик"})
	if person.INN != "" || person.Requisites != "" {
		t.Fatalf("card payer got iin=%q requisites=%q, want both empty", person.INN, person.Requisites)
	}
	if person.ShortName != "Соло Разработчик" {
		t.Fatalf("short_name = %q, want the person's own name", person.ShortName)
	}
}

// TestInvoicePaymentIsLeftToTheBank guards the rule the hosting bill broke:
// money that lands on the company account as its own statement line is already
// in FinCore through the bank feed, so the console must not mint a second row
// for it. A card payment has no such line and stays ours to push.
func TestInvoicePaymentIsLeftToTheBank(t *testing.T) {
	if !(CloudPayment{Method: "invoice"}).SettledInBank() {
		t.Fatal("invoice payment not recognised as settled in the bank: it would be booked twice")
	}
	if (CloudPayment{Method: "card"}).SettledInBank() {
		t.Fatal("card payment treated as a bank line: YooKassa money would go unrecorded")
	}
	if (CloudPayment{}).SettledInBank() {
		t.Fatal("payment with no method treated as a bank line")
	}

	tx := TransactionFromPayment(CloudPayment{
		ID: "p-1", OrgID: "acme", Plan: "business", Amount: "2900.00",
		Method: "card", InvoiceNumber: "INV-2026-00002",
		PayerINN: "7807402712", PayerOrgName: `ООО "АКМЕ"`,
	})
	if tx.PayerINN != "7807402712" {
		t.Fatalf("payer_inn = %q, want the INN so FinCore can match the payer", tx.PayerINN)
	}
	if tx.PayerName != `ООО "АКМЕ"` {
		t.Fatalf("payer_name = %q, want the legal entity", tx.PayerName)
	}
	if !strings.Contains(tx.PaymentPurpose, "INV-2026-00002") {
		t.Fatalf("payment_purpose = %q, want the invoice number a bank transfer quotes", tx.PaymentPurpose)
	}
}
