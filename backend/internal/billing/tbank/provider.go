package tbank

import (
	"context"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// invoiceNumberPattern matches an invoice number embedded anywhere in a bank
// statement operation's free-text payment purpose, e.g. "Оплата по счету
// INV-2026-00042 от 17.08.2026". Must stay in sync with FormatInvoiceNumber's
// output shape.
var invoiceNumberPattern = regexp.MustCompile(`INV-\d{4}-\d{5}`)

// amountEpsilon tolerates float round-trip noise when comparing a statement
// operation's amount against a payments row's amount_value. Both are decimal
// rubles-and-kopecks strings, so anything past a hundredth of a ruble is a
// real mismatch, not rounding.
const amountEpsilon = 0.005

// rubleCurrencyCode is ISO 4217 numeric 643. The platform account is in
// rubles and invoice amounts are rubles, so an operation in any other
// currency cannot settle one.
const rubleCurrencyCode = "643"

// Provider reconciles pending invoice payments against the platform's
// T-Bank Business account statement. It embeds billing.ManualProvider so a
// matched payment can be handed straight to AssignPlan, the same plan-grant
// path every other payment method uses.
type Provider struct {
	billing.ManualProvider
	Client        *Client
	AccountNumber string
	Notifier      *notify.Notifier
	AuditEmail    string
}

// NewProvider builds a Provider. Notifier/AuditEmail stay their zero values
// when the caller has no SMTP configured -- notifyPaid degrades to a no-op,
// the same way h.auditNotifier being nil silences yookassa's own success
// mail.
func NewProvider(pool *pgxpool.Pool, client *Client, accountNumber string) *Provider {
	return &Provider{
		ManualProvider: billing.ManualProvider{Pool: pool},
		Client:         client,
		AccountNumber:  accountNumber,
	}
}

// pendingInvoice is one payments row still waiting for money to show up on
// the statement.
type pendingInvoice struct {
	ID            string
	OrgID         string
	Plan          string
	InvoiceNumber string
	AmountValue   float64
	CustomerEmail string
	CreatedAt     time.Time
}

// Reconcile runs one pass: it lists every pending invoice payment, pulls the
// account statement covering the oldest of them through now, and matches
// each statement operation to a pending row by invoice number (found via
// invoiceNumberPattern in the operation's payment purpose) plus an exact
// amount match. Only a settled incoming ruble operation is eligible: a
// debit is our own money leaving, and an unsettled hold is not money that
// has landed. A matched operation flips its row to succeeded and assigns
// the plan, guarded by "status = 'pending'" in the same UPDATE so a
// statement operation seen twice across runs (tbank_operation_id already
// recorded, or the row already flipped) can never double-apply.
//
// An operation that matches no pending row, or a pending row nothing on the
// statement matches yet, is left alone and logged -- there is no automatic
// resolution for a mismatched amount or a still-in-flight bank transfer, and
// inventing one risks crediting the wrong org.
func (p *Provider) Reconcile(ctx context.Context) (matched int, err error) {
	pending, err := p.listPending(ctx)
	if err != nil {
		return 0, fmt.Errorf("tbank: list pending invoices: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}

	from := pending[0].CreatedAt
	for _, inv := range pending[1:] {
		if inv.CreatedAt.Before(from) {
			from = inv.CreatedAt
		}
	}
	now := time.Now().UTC()

	ops, err := p.Client.Statement(ctx, p.AccountNumber, from, now)
	if err != nil {
		return 0, fmt.Errorf("tbank: fetch statement: %w", err)
	}

	byInvoiceNumber := make(map[string]pendingInvoice, len(pending))
	for _, inv := range pending {
		byInvoiceNumber[inv.InvoiceNumber] = inv
	}

	for _, op := range ops {
		if !op.IsSettledCredit() {
			continue
		}
		if op.CurrencyCode != "" && op.CurrencyCode != rubleCurrencyCode {
			log.Printf("tbank: reconcile: operation %s is in currency %s, not rubles, skipping", op.OperationID, op.CurrencyCode)
			continue
		}
		number := invoiceNumberPattern.FindString(op.Purpose)
		if number == "" {
			continue
		}
		inv, ok := byInvoiceNumber[number]
		if !ok {
			continue
		}
		if math.Abs(op.Amount-inv.AmountValue) > amountEpsilon {
			log.Printf("tbank: reconcile: invoice=%s statement amount=%.2f does not match expected %.2f, leaving pending",
				number, op.Amount, inv.AmountValue)
			continue
		}

		applied, err := p.applyPayment(ctx, inv, op.OperationID)
		if err != nil {
			log.Printf("tbank: reconcile: invoice=%s operation=%s failed to apply: %v", number, op.OperationID, err)
			continue
		}
		if applied {
			matched++
			p.notifyPaid(inv)
		}
	}

	return matched, nil
}

func (p *Provider) listPending(ctx context.Context) ([]pendingInvoice, error) {
	rows, err := p.Pool.Query(ctx, `
		SELECT id, org_id, plan, invoice_number, amount_value::text, coalesce(customer_email, ''), created_at
		FROM payments
		WHERE payment_method = 'invoice' AND status = 'pending' AND invoice_number IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pending := make([]pendingInvoice, 0)
	for rows.Next() {
		var inv pendingInvoice
		var amountText string
		if err := rows.Scan(&inv.ID, &inv.OrgID, &inv.Plan, &inv.InvoiceNumber, &amountText, &inv.CustomerEmail, &inv.CreatedAt); err != nil {
			return nil, err
		}
		amount, err := strconv.ParseFloat(amountText, 64)
		if err != nil {
			return nil, fmt.Errorf("tbank: parse amount_value %q for payment %s: %w", amountText, inv.ID, err)
		}
		inv.AmountValue = amount
		pending = append(pending, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pending, nil
}

// applyPayment marks one pending invoice payment succeeded and assigns its
// plan, in one transaction guarded by "status = 'pending'". Returns
// applied=false (no error) when the guard finds the row already settled --
// a second run matching the same statement operation is a no-op, not a
// failure.
func (p *Provider) applyPayment(ctx context.Context, inv pendingInvoice, operationID string) (applied bool, err error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	tag, err := tx.Exec(ctx, `
		UPDATE payments
		SET status = 'succeeded', paid_at = $1, updated_at = $1, tbank_operation_id = $2
		WHERE id = $3 AND status = 'pending'
	`, now, operationID, inv.ID)
	if err != nil {
		return false, fmt.Errorf("mark succeeded: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}

	if err := assignPlanTx(ctx, tx, inv.OrgID, inv.Plan, now); err != nil {
		return false, fmt.Errorf("assign plan: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

// assignPlanTx mirrors yookassa's own assignPlanTx: a paid assignment always
// carries a 30-day term, extending from whichever is later -- the current
// expiry (early payment keeps remaining days) or now (a lapsed account does
// not lose the gap).
func assignPlanTx(ctx context.Context, tx pgx.Tx, orgID, plan string, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, expiry_notified_at, updated_at)
		VALUES ($1, $2, $3::timestamptz, $3::timestamptz + interval '30 days', NULL, $3::timestamptz)
		ON CONFLICT (org_id) DO UPDATE
		  SET plan               = EXCLUDED.plan,
		      plan_assigned_at   = EXCLUDED.plan_assigned_at,
		      plan_expires_at    = GREATEST(coalesce(billing_accounts.plan_expires_at, EXCLUDED.plan_assigned_at), EXCLUDED.plan_assigned_at) + interval '30 days',
		      expiry_notified_at = NULL,
		      updated_at         = EXCLUDED.updated_at
	`, orgID, plan, now)
	return err
}

// notifyPaid sends the same customer receipt and operator copy yookassa
// sends on a successful payment. Best-effort: a mail failure never unwinds
// the payment that already landed, so failures are only logged.
func (p *Provider) notifyPaid(inv pendingInvoice) {
	if p.Notifier == nil {
		return
	}
	if inv.CustomerEmail != "" {
		subject, body := notify.ComposePaymentSuccess(inv.Plan, fmt.Sprintf("%.2f", inv.AmountValue), false)
		if err := p.Notifier.Send(inv.CustomerEmail, subject, body); err != nil {
			log.Printf("tbank: customer receipt send to %s failed: %v", inv.CustomerEmail, err)
		}
	}
	if p.AuditEmail != "" {
		createdAtUTC := time.Now().UTC().Format(time.RFC3339)
		subject, body := notify.ComposeAudit("InvoicePaymentSucceeded", inv.CustomerEmail, fmt.Sprintf("%s (%.2f RUB)", inv.Plan, inv.AmountValue), inv.OrgID, createdAtUTC)
		if err := p.Notifier.Send(p.AuditEmail, subject, body); err != nil {
			log.Printf("tbank: operator copy send to %s failed: %v", p.AuditEmail, err)
		}
	}
}
