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
type CloudUser struct {
	ID            string
	Username      string
	Email         string
	DisplayName   string
	CreatedAt     time.Time
	SignupChannel string
	SignupSource  string
}

// CloudPayment is one YooKassa payment as the console recorded it.
type CloudPayment struct {
	ID            string
	OrgID         string
	Plan          string
	Amount        string
	Currency      string
	YKPaymentID   string
	CustomerEmail string
	PaidAt        time.Time
	Owner         *CloudUser
}

// ClientExternalID is the key a Dada Cloud user is known by inside FinCore.
// It is the console's own user id: usernames and emails both change, the
// primary key does not, and a changed key would fork the client into two.
func ClientExternalID(u CloudUser) string { return u.ID }

// PaymentSourceIdentity keys a payment fact. FinCore prefixes it with the
// source system, so the stored key ends up "dada_cloud:payment:<uuid>".
func PaymentSourceIdentity(paymentID string) string { return "payment:" + paymentID }

// ClientFromUser maps a console user onto FinCore's client shape.
func ClientFromUser(u CloudUser) ClientUpsert {
	return ClientUpsert{
		ExternalID:    ClientExternalID(u),
		ShortName:     clientShortName(u),
		ContactPerson: strings.TrimSpace(u.DisplayName),
		Email:         strings.TrimSpace(u.Email),
		IncomeSource:  incomeSource(u),
		Comment:       clientComment(u),
	}
}

func clientShortName(u CloudUser) string {
	for _, candidate := range []string{u.DisplayName, u.Username, u.Email} {
		if v := strings.TrimSpace(candidate); v != "" {
			return v
		}
	}
	return u.ID
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
