package fincore

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// yookassaAccount stands in for the cash source of a fact.
// FinCore never resolves an account number against a real bank -- it only
// compares them -- so naming them keeps revenue and hosting spend separable in
// the ledger instead of collapsing both onto the synthetic EXT:dada_cloud
// account the ingest seam would otherwise assign.
const (
	yookassaAccount = "YOOKASSA"
)

// CloudUser is the console side of a FinCore client.
//
// The requisites are what the user typed when asking for an invoice. They are
// the only identity the console holds that a bank statement also carries, so
// they are what lets FinCore bind an incoming transfer to this client instead
// of leaving it as an unknown counterparty.
type CloudUser struct {
	ID            string
	Username      string
	Email         string
	DisplayName   string
	CreatedAt     time.Time
	SignupChannel string
	SignupSource  string

	INN          string
	KPP          string
	OrgName      string
	LegalAddress string
}

// CloudPayment is one payment as the console recorded it.
//
// Method separates the two ways money reaches the company. A card payment is
// collected by YooKassa and never appears in the bank feed as its own row, so
// the console is its only witness. An invoice is paid by bank transfer onto the
// company account, which the findata T-Bank integration already streams into
// the same FinCore tenant.
type CloudPayment struct {
	ID            string
	OrgID         string
	Plan          string
	Amount        string
	Currency      string
	YKPaymentID   string
	CustomerEmail string
	PaidAt        time.Time
	Method        string
	InvoiceNumber string
	PayerINN      string
	PayerOrgName  string
	Owner         *CloudUser
}

// methodInvoice is payments.payment_method for "we issued an invoice and the
// customer pays it by bank transfer".
const methodInvoice = "invoice"

// SettledInBank reports whether this payment arrives on the company's bank
// account as its own statement line. Those are already in FinCore through the
// bank integration; minting a second row for them would book the same money
// twice, exactly as the hosting bill did.
func (p CloudPayment) SettledInBank() bool {
	return strings.EqualFold(strings.TrimSpace(p.Method), methodInvoice)
}

// ClientExternalID is the key a Dada Cloud user is known by inside FinCore.
// It is the console's own user id: usernames and emails both change, the
// primary key does not, and a changed key would fork the client into two.
func ClientExternalID(u CloudUser) string { return u.ID }

// PaymentSourceIdentity keys a payment fact. FinCore prefixes it with the
// source system, so the stored key ends up "dada_cloud:payment:<uuid>".
func PaymentSourceIdentity(paymentID string) string { return "payment:" + paymentID }

// ClientFromUser maps a console user onto FinCore's client shape.
//
// INN is the attribution key. FinCore binds an incoming bank transfer to a
// client by matching the statement's payer against the client card -- client 1
// in this tenant carries iin 7840394339 and every transfer from that INN is
// classified as its revenue. A cloud client without an INN can therefore never
// be credited with money that arrived by transfer, however much the console
// knows about it.
func ClientFromUser(u CloudUser) ClientUpsert {
	return ClientUpsert{
		ExternalID:    ClientExternalID(u),
		ShortName:     clientShortName(u),
		INN:           strings.TrimSpace(u.INN),
		ContactPerson: strings.TrimSpace(u.DisplayName),
		Email:         strings.TrimSpace(u.Email),
		Requisites:    clientRequisites(u),
		IncomeSource:  incomeSource(u),
		Comment:       clientComment(u),
	}
}

// clientShortName prefers the legal entity: once a user has asked for an
// invoice, the counterparty that pays is the company, and that is the name the
// bank statement will carry.
func clientShortName(u CloudUser) string {
	for _, candidate := range []string{u.OrgName, u.DisplayName, u.Username, u.Email} {
		if v := strings.TrimSpace(candidate); v != "" {
			return v
		}
	}
	return u.ID
}

// clientRequisites renders what the console holds about the paying entity.
// Empty when the user has never asked for an invoice -- a card payer is a
// person, and inventing requisites for them would put noise on the card.
func clientRequisites(u CloudUser) string {
	var parts []string
	for _, part := range []struct{ label, value string }{
		{"", u.OrgName},
		{"ИНН ", u.INN},
		{"КПП ", u.KPP},
		{"", u.LegalAddress},
	} {
		if v := strings.TrimSpace(part.value); v != "" {
			parts = append(parts, part.label+v)
		}
	}
	return strings.Join(parts, ", ")
}

// incomeSource records the door the user came through, which is the one piece
// of attribution the console owns and the CRM cannot reconstruct.
func incomeSource(u CloudUser) string {
	channel := strings.TrimSpace(u.SignupChannel)
	source := strings.TrimSpace(u.SignupSource)
	switch {
	case channel != "" && source != "":
		return "dada_cloud/" + channel + "/" + source
	case channel != "":
		return "dada_cloud/" + channel
	case source != "":
		return "dada_cloud/" + source
	default:
		return "dada_cloud"
	}
}

func clientComment(u CloudUser) string {
	username := strings.TrimSpace(u.Username)
	if username == "" {
		username = u.ID
	}
	return fmt.Sprintf("Пользователь Dada Cloud %s, регистрация %s", username, u.CreatedAt.Format("2006-01-02"))
}

// TransactionFromPayment maps a succeeded YooKassa payment onto an incoming
// money fact.
//
// The payer is the person, not the org slug: FinCore shows payer_name on the
// transaction card, and an org slug there reads as an unknown counterparty.
// When the payment's org resolves to no console user the fact is still pushed,
// unlinked and marked in metadata, because dropping a real ruble to keep the
// CRM tidy loses money that actually moved.
func TransactionFromPayment(p CloudPayment) Transaction {
	tx := Transaction{
		SourceIdentity: PaymentSourceIdentity(p.ID),
		AccountNumber:  yookassaAccount,
		OperationDate:  WallTime(p.PaidAt),
		Direction:      DirectionCredit,
		Amount:         p.Amount,
		Currency:       currencyOrRUB(p.Currency),
		PayerName:      paymentPayerName(p),
		PayerINN:       strings.TrimSpace(p.PayerINN),
		PaymentPurpose: paymentPurpose(p),
		Metadata: map[string]any{
			"console_payment_id": p.ID,
			"org_id":             p.OrgID,
			"plan":               p.Plan,
			"yk_payment_id":      p.YKPaymentID,
		},
	}
	if p.Owner != nil {
		tx.ClientExternal = ClientExternalID(*p.Owner)
	} else {
		tx.Metadata["client_unresolved"] = true
	}
	return tx
}

func paymentPayerName(p CloudPayment) string {
	if v := strings.TrimSpace(p.PayerOrgName); v != "" {
		return v
	}
	if p.Owner != nil {
		return clientShortName(*p.Owner)
	}
	for _, candidate := range []string{p.CustomerEmail, p.OrgID} {
		if v := strings.TrimSpace(candidate); v != "" {
			return v
		}
	}
	return "Dada Cloud"
}

func paymentPurpose(p CloudPayment) string {
	purpose := "Оплата Dada Cloud"
	if plan := strings.TrimSpace(p.Plan); plan != "" {
		purpose += ", тариф " + plan
	}
	if inv := strings.TrimSpace(p.InvoiceNumber); inv != "" {
		purpose += ", счёт " + inv
	}
	if yk := strings.TrimSpace(p.YKPaymentID); yk != "" {
		purpose += ", платёж ЮKassa " + yk
	}
	return purpose
}

// FormatAmount renders a ruble figure the way the ingest DTO parses it: a
// plain decimal string, no thousands separators, no currency sign.
func FormatAmount(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func currencyOrRUB(c string) string {
	if v := strings.TrimSpace(c); v != "" {
		return strings.ToUpper(v)
	}
	return "RUB"
}
