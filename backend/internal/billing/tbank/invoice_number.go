package tbank

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// invoiceNumberSequencer is the subset of pgx that GenerateInvoiceNumber
// needs to pull the next value of invoice_number_seq. Both *pgxpool.Pool and
// pgx.Tx satisfy it, so a caller can generate a number either standalone or
// inside the same transaction as the payments insert -- the latter is what
// CreateInvoice does, so a failed insert cannot burn a sequence value that
// is never printed on any invoice.
type invoiceNumberSequencer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// FormatInvoiceNumber renders one invoice_number_seq value as the printed
// invoice number: INV-{year}-{seq zero-padded to 5 digits}. Split out from
// GenerateInvoiceNumber so the format itself is unit-testable without a
// database.
func FormatInvoiceNumber(year int, seq int64) string {
	return fmt.Sprintf("INV-%04d-%05d", year, seq)
}

// GenerateInvoiceNumber pulls the next invoice_number_seq value and formats
// it against the current year. The sequence guarantees the seq portion is
// unique and monotonic even under concurrent checkouts; nothing here needs
// its own locking.
func GenerateInvoiceNumber(ctx context.Context, db invoiceNumberSequencer, now time.Time) (string, error) {
	var seq int64
	if err := db.QueryRow(ctx, `SELECT nextval('invoice_number_seq')`).Scan(&seq); err != nil {
		return "", fmt.Errorf("tbank: next invoice number: %w", err)
	}
	return FormatInvoiceNumber(now.UTC().Year(), seq), nil
}
