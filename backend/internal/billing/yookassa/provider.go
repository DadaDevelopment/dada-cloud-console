package yookassa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WebhookOutcome is what ProcessWebhook did with an inbound YooKassa webhook,
// after re-fetching the authoritative payment state. Every value maps to an
// HTTP 200 response to YooKassa except when ProcessWebhook itself returns an
// error (YK API fetch failure), which the caller must answer with 500 so
// YooKassa retries.
//
// OutcomeUnknownPayment: no local row matches the yk_payment_id -- logged,
// not an error, since a payment created outside this deployment (or a stale
// test event) must not leak information back to the caller.
//
// OutcomeAlreadyProcessed: the local row is already terminal (succeeded or
// canceled); replay-safe no-op.
//
// OutcomeSucceeded: the row transitioned pending -> succeeded and the org's
// plan was assigned in the same transaction.
//
// OutcomeCanceled: the row transitioned pending -> canceled.
//
// OutcomeNoop: the authoritative YooKassa status is neither succeeded nor
// canceled (still pending/waiting_for_capture). This is also the outcome for
// the no-signature spoof case -- a webhook payload can claim anything, but
// only the re-fetched authoritative status ever drives the row.
type WebhookOutcome string

const (
	OutcomeUnknownPayment   WebhookOutcome = "unknown_payment"
	OutcomeAlreadyProcessed WebhookOutcome = "already_processed"
	OutcomeSucceeded        WebhookOutcome = "succeeded"
	OutcomeCanceled         WebhookOutcome = "canceled"
	OutcomeNoop             WebhookOutcome = "noop"
)

// YooKassaProvider implements billing.PaymentProvider (via the embedded
// ManualProvider.AssignPlan, used on the payment-success path) and adds the
// money-collection flow: checkout (create a redirect payment) and webhook
// processing (authoritative re-fetch, never trust the payload).
type YooKassaProvider struct {
	billing.ManualProvider
	Client      *Client
	ReturnURL   string
	SendReceipt bool
}

// NewProvider builds a YooKassaProvider.
func NewProvider(pool *pgxpool.Pool, client *Client, returnURL string, sendReceipt bool) *YooKassaProvider {
	return &YooKassaProvider{
		ManualProvider: billing.ManualProvider{Pool: pool},
		Client:         client,
		ReturnURL:      returnURL,
		SendReceipt:    sendReceipt,
	}
}

// Checkout starts a one-off payment for a paid plan: inserts a pending
// payments row keyed by a fresh UUID (also sent to YooKassa as the
// Idempotence-Key), creates the YooKassa payment, then stores the returned
// yk_payment_id and confirmation_url on the row. The caller resolves plan
// server-side (never trusts a client-supplied amount).
func (p *YooKassaProvider) Checkout(ctx context.Context, orgID string, plan pricing.Plan, customerEmail, createdBySub string) (paymentID, confirmationURL string, err error) {
	id := uuid.New()
	amountValue := fmt.Sprintf("%.2f", plan.PriceRUB)

	_, err = p.Pool.Exec(ctx, `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, customer_email, created_by_sub)
		VALUES ($1, $2, $3, $4, 'RUB', 'pending', $5, $6)
	`, id, orgID, plan.Key, amountValue, customerEmail, createdBySub)
	if err != nil {
		return "", "", fmt.Errorf("yookassa: insert pending payment: %w", err)
	}

	amount := Amount{Value: amountValue, Currency: "RUB"}
	req := CreatePaymentRequest{
		Amount:       amount,
		Capture:      true,
		Confirmation: Confirmation{Type: "redirect", ReturnURL: p.ReturnURL},
		Description:  fmt.Sprintf("Dada Cloud: тариф %s", plan.Name),
	}
	if p.SendReceipt && customerEmail != "" {
		req.Receipt = &Receipt{
			Customer: ReceiptCustomer{Email: customerEmail},
			Items: []ReceiptItem{{
				Description:    fmt.Sprintf("Тариф %s", plan.Name),
				Quantity:       "1.00",
				Amount:         amount,
				VatCode:        1,
				PaymentMode:    "full_payment",
				PaymentSubject: "service",
			}},
		}
	}

	payment, err := p.Client.CreatePayment(ctx, id.String(), req)
	if err != nil {
		return "", "", fmt.Errorf("yookassa: create payment: %w", err)
	}

	_, err = p.Pool.Exec(ctx, `
		UPDATE payments SET yk_payment_id = $1, confirmation_url = $2, updated_at = $3 WHERE id = $4
	`, payment.ID, payment.Confirmation.URL, time.Now().UTC(), id)
	if err != nil {
		return "", "", fmt.Errorf("yookassa: store yk payment id: %w", err)
	}

	return id.String(), payment.Confirmation.URL, nil
}

// WebhookResult is what ProcessWebhook found and did. OrgID/Plan/AmountValue/
// CustomerEmail are populated whenever a local row was matched (every
// Outcome except OutcomeUnknownPayment), so the caller can log and notify
// without a second query.
type WebhookResult struct {
	Outcome       WebhookOutcome
	OrgID         string
	Plan          string
	AmountValue   string
	CustomerEmail string
}

// ProcessWebhook handles one inbound YooKassa webhook delivery. ykPaymentID
// is the object.id from the webhook payload -- the ONLY thing trusted from
// the payload, since YooKassa webhooks carry no signature. The payment's
// actual status is always re-fetched via GetPayment before any row is
// touched. A non-nil error means the YooKassa API call itself failed and the
// caller should answer 500 so YooKassa retries; every other case is a
// terminal WebhookResult mapped to HTTP 200.
func (p *YooKassaProvider) ProcessWebhook(ctx context.Context, ykPaymentID string) (WebhookResult, error) {
	payment, err := p.Client.GetPayment(ctx, ykPaymentID)
	if err != nil {
		return WebhookResult{}, fmt.Errorf("yookassa: refetch payment %s: %w", ykPaymentID, err)
	}

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return WebhookResult{}, fmt.Errorf("yookassa: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var id uuid.UUID
	var orgID, plan, status, amountValue string
	var customerEmail *string
	err = tx.QueryRow(ctx, `
		SELECT id, org_id, plan, status, amount_value::text, customer_email
		FROM payments WHERE yk_payment_id = $1 FOR UPDATE
	`, ykPaymentID).Scan(&id, &orgID, &plan, &status, &amountValue, &customerEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookResult{Outcome: OutcomeUnknownPayment}, nil
	}
	if err != nil {
		return WebhookResult{}, fmt.Errorf("yookassa: lookup payment row: %w", err)
	}

	result := WebhookResult{OrgID: orgID, Plan: plan, AmountValue: amountValue}
	if customerEmail != nil {
		result.CustomerEmail = *customerEmail
	}

	switch payment.Status {
	case "succeeded":
		if status != "pending" {
			result.Outcome = OutcomeAlreadyProcessed
			return result, nil
		}
		now := time.Now().UTC()
		if _, err := tx.Exec(ctx, `
			UPDATE payments SET status = 'succeeded', paid_at = $1, updated_at = $1 WHERE id = $2
		`, now, id); err != nil {
			return WebhookResult{}, fmt.Errorf("yookassa: mark succeeded: %w", err)
		}
		if err := assignPlanTx(ctx, tx, orgID, plan, now); err != nil {
			return WebhookResult{}, fmt.Errorf("yookassa: assign plan: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, fmt.Errorf("yookassa: commit: %w", err)
		}
		result.Outcome = OutcomeSucceeded
		return result, nil

	case "canceled":
		if status != "pending" {
			result.Outcome = OutcomeAlreadyProcessed
			return result, nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE payments SET status = 'canceled', updated_at = $1 WHERE id = $2
		`, time.Now().UTC(), id); err != nil {
			return WebhookResult{}, fmt.Errorf("yookassa: mark canceled: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, fmt.Errorf("yookassa: commit: %w", err)
		}
		result.Outcome = OutcomeCanceled
		return result, nil

	default:
		result.Outcome = OutcomeNoop
		return result, nil
	}
}

// assignPlanTx mirrors billing.ManualProvider.AssignPlan's upsert, scoped to
// the caller's transaction so the payments-row flip and the plan assignment
// commit or roll back together.
//
// Unlike the manual/admin path (plan_expires_at stays NULL = perpetual), a
// paid assignment always carries a 30-day term: a fresh account gets
// now+30d, a renewal extends from whichever is later — the current expiry
// (early renewal keeps the remaining days) or now (a lapsed account does not
// lose the gap). expiry_notified_at resets so future expiry reminders fire
// again for the new term.
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
