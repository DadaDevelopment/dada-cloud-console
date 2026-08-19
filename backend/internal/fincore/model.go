package fincore

import (
	"fmt"
	"strings"
	"time"
)

// SourceSystem is what every row this package pushes carries in source_system.
// FinCore namespaces the caller's idempotency key with it
// (app/entities/ingest/service.py: build_source_identity) and resolves clients
// by (source_system, external_id), so it must stay stable across releases.
const SourceSystem = "dada_cloud"

// Direction mirrors FinCore's vocabulary: money in is CREDIT, money out DEBIT.
type Direction string

const (
	// DirectionCredit is money arriving -- revenue.
	DirectionCredit Direction = "CREDIT"
	// DirectionDebit is money leaving -- expense.
	DirectionDebit Direction = "DEBIT"
)

// ClientUpsert is one Dada Cloud user presented to FinCore as a client.
//
// Field set is exactly IngestClientUpsertIn; the endpoint is declared
// extra="forbid", so an unknown key fails the whole batch with 422.
type ClientUpsert struct {
	ExternalID    string `json:"external_id"`
	ShortName     string `json:"short_name,omitempty"`
	INN           string `json:"iin,omitempty"`
	ContactPerson string `json:"contact_person,omitempty"`
	Phone         string `json:"phone,omitempty"`
	Email         string `json:"email,omitempty"`
	Requisites    string `json:"requisites,omitempty"`
	Comment       string `json:"comment,omitempty"`
	IncomeSource  string `json:"income_source,omitempty"`
}

// ClientResult is FinCore's per-item answer: the client id it settled on and
// whether this call is what created it.
type ClientResult struct {
	ExternalID string `json:"external_id"`
	ClientID   int64  `json:"client_id"`
	Created    bool   `json:"created"`
}

// ClientsUpsertResult is the batch outcome for a client sync.
type ClientsUpsertResult struct {
	Received int            `json:"received"`
	Created  int            `json:"created"`
	Updated  int            `json:"updated"`
	Results  []ClientResult `json:"results"`
}

// Transaction is one money fact handed to FinCore's ingest endpoint.
//
// WallTime is a timestamp serialised without a zone offset.
//
// FinCore stores an operation date in sber_statements.operation_date, a
// TIMESTAMP WITHOUT TIME ZONE, and its ingest DTO hands whatever pydantic
// parsed straight to asyncpg. An RFC3339 value carrying an offset therefore
// reaches a naive column and the driver refuses it -- the whole batch comes
// back as "http 503: database_unavailable". Emitting local wall-clock time
// keeps the value the reader expects (Moscow business hours) and lands.
type WallTime time.Time

// MarshalJSON renders the instant as a naive local timestamp.
func (t WallTime) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Time(t).Format("2006-01-02T15:04:05") + `"`), nil
}

// UnmarshalJSON accepts a naive timestamp and, for tolerance, an RFC3339 one.
func (t *WallTime) UnmarshalJSON(b []byte) error {
	raw := strings.Trim(string(b), `"`)
	for _, layout := range []string{"2006-01-02T15:04:05", time.RFC3339} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			*t = WallTime(parsed)
			return nil
		}
	}
	return fmt.Errorf("fincore: cannot parse operation_date %q", raw)
}

// SourceIdentity is supplied by this side and is what makes a replayed
// backfill harmless: FinCore upserts on (SourceSystem, SourceIdentity) instead
// of minting its own key. It is NOT prefixed with the source system here --
// FinCore does that itself.
//
// CounterpartyName lives in payer_name for a CREDIT and payee_name for a
// DEBIT; the endpoint rejects an item that leaves the direction's side empty.
type Transaction struct {
	SourceIdentity string         `json:"source_identity"`
	AccountNumber  string         `json:"account_number,omitempty"`
	OperationDate  WallTime       `json:"operation_date"`
	Direction      Direction      `json:"direction"`
	Amount         string         `json:"amount"`
	Currency       string         `json:"currency"`
	PayerName      string         `json:"payer_name,omitempty"`
	PayerINN       string         `json:"payer_inn,omitempty"`
	PayeeName      string         `json:"payee_name,omitempty"`
	PayeeINN       string         `json:"payee_inn,omitempty"`
	PaymentPurpose string         `json:"payment_purpose,omitempty"`
	ClientExternal string         `json:"client_external_id,omitempty"`
	ProjectID      int            `json:"project_id,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// TransactionResult is FinCore's per-item answer. Status is created, updated
// or unchanged -- a replayed backfill is expected to come back unchanged.
type TransactionResult struct {
	SourceIdentity       string `json:"source_identity"`
	StatementID          int64  `json:"statement_id"`
	FactID               *int64 `json:"fact_id"`
	Status               string `json:"status"`
	ClientID             *int64 `json:"client_id"`
	ProjectID            *int64 `json:"project_id"`
	ClassificationStatus string `json:"classification_status"`
}

// IngestResult is the batch outcome for a transaction push.
type IngestResult struct {
	Received  int                 `json:"received"`
	Created   int                 `json:"created"`
	Updated   int                 `json:"updated"`
	Unchanged int                 `json:"unchanged"`
	Results   []TransactionResult `json:"results"`
}
