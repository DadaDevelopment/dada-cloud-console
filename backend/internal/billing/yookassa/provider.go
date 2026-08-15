package yookassa

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
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
// money-collection flow: checkout (create a redirect payment), webhook
// processing (authoritative re-fetch, never trust the payload) and the
// recurring charge against a saved payment method.
//
// SendReceipt turns on the 54-FZ fiscal receipt block. It must stay off for a
// shop without fiscalization enabled — YooKassa rejects the create call
// outright — and on for any shop taking real money from Russian customers,
// where issuing a receipt is a legal duty rather than a nicety. VatCode and
// TaxSystemCode come from configuration because both depend on the merchant's
// own tax registration, not on anything this code can derive.
type YooKassaProvider struct {
	billing.ManualProvider
	Client        *Client
	ReturnURL     string
	SendReceipt   bool
	VatCode       int
	TaxSystemCode int
}

// NewProvider builds a YooKassaProvider. vatCode 0 falls back to 1 ("no VAT"),
// the only value that is safe for a merchant on a simplified tax regime;
// taxSystemCode 0 means the shop has a single tax system and the field is
// omitted from the receipt.
func NewProvider(pool *pgxpool.Pool, client *Client, returnURL string, sendReceipt bool, vatCode, taxSystemCode int) *YooKassaProvider {
	if vatCode == 0 {
		vatCode = 1
	}
	return &YooKassaProvider{
		ManualProvider: billing.ManualProvider{Pool: pool},
		Client:         client,
		ReturnURL:      returnURL,
		SendReceipt:    sendReceipt,
		VatCode:        vatCode,
		TaxSystemCode:  taxSystemCode,
	}
}

// ErrReceiptEmailRequired reports a payer with no known email on a shop that
// fiscalises. YooKassa answers such a charge with 400 "Receipt is missing or
// illegal", so the charge is impossible rather than merely receiptless, and the
// caller is owed that verdict before any money flow is attempted.
var ErrReceiptEmailRequired = errors.New("yookassa: fiscal receipt requires a customer email")

// requireReceiptEmail refuses a charge that fiscalization makes impossible.
// With SendReceipt on, an empty email is not a missing nicety: the shop rejects
// the whole payment, and finding that out from a 400 after a pending row exists
// leaves a payment row that can never settle.
func (p *YooKassaProvider) requireReceiptEmail(customerEmail string) error {
	if p.SendReceipt && customerEmail == "" {
		return ErrReceiptEmailRequired
	}
	return nil
}

// receiptFor builds the 54-FZ receipt block for one plan charge, or nil when
// fiscalization is off or no customer email is known — a receipt without a
// delivery address is not a receipt the customer will ever see, and YooKassa
// rejects it.
func (p *YooKassaProvider) receiptFor(plan pricing.Plan, amount Amount, customerEmail string) *Receipt {
	if !p.SendReceipt || customerEmail == "" {
		return nil
	}
	return &Receipt{
		Customer:      ReceiptCustomer{Email: customerEmail},
		TaxSystemCode: p.TaxSystemCode,
		Items: []ReceiptItem{{
			Description:    fmt.Sprintf("Тариф %s, доступ на 30 дней", plan.Name),
			Quantity:       "1.00",
			Amount:         amount,
			VatCode:        p.VatCode,
			PaymentMode:    "full_payment",
			PaymentSubject: "service",
		}},
	}
}

// Checkout starts a customer-present payment for a paid plan: inserts a
// pending payments row keyed by a fresh UUID (also sent to YooKassa as the
// Idempotence-Key), creates the YooKassa payment, then stores the returned
// yk_payment_id and confirmation_url on the row. The caller resolves plan
// server-side (never trusts a client-supplied amount).
//
// projectID, when non-empty, is carried onto the return URL as
// ?project=...&payment=... so the console's return page can poll this exact
// payment's status instead of showing a blind thank-you.
//
// saveMethod asks YooKassa to keep the payment method reusable so the plan
// can renew itself later. It reflects a checkbox the customer ticked, and
// nothing else: a recurring charge the payer did not agree to is a chargeback
// with extra steps.
//
// A CreatePayment failure after the pending row was inserted (P0-PAY-CHECKOUT,
// 2026-08-15: a real payer's two checkout attempts both left bare pending rows
// with empty yk_payment_id/confirmation_url that could never settle -- no
// webhook was ever coming for a payment YooKassa never created) marks that row
// "canceled" before the error is returned, mirroring ChargeSaved. A row must
// never sit in "pending" once its one and only path to becoming non-pending --
// a webhook for a yk_payment_id that does not exist -- is already impossible.
func (p *YooKassaProvider) Checkout(ctx context.Context, orgID string, plan pricing.Plan, customerEmail, createdBySub, projectID string, saveMethod bool) (paymentID, confirmationURL string, err error) {
	if err = p.requireReceiptEmail(customerEmail); err != nil {
		return "", "", err
	}

	id := uuid.New()
	amountValue := fmt.Sprintf("%.2f", plan.PriceRUB)

	_, err = p.Pool.Exec(ctx, `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, customer_email, created_by_sub)
		VALUES ($1, $2, $3, $4, 'RUB', 'pending', $5, $6)
	`, id, orgID, plan.Key, amountValue, customerEmail, createdBySub)
	if err != nil {
		return "", "", fmt.Errorf("yookassa: insert pending payment: %w", err)
	}

	returnURL := p.ReturnURL
	if projectID != "" {
		sep := "?"
		if strings.Contains(returnURL, "?") {
			sep = "&"
		}
		returnURL += sep + "project=" + url.QueryEscape(projectID) + "&payment=" + id.String()
	}

	amount := Amount{Value: amountValue, Currency: "RUB"}
	req := CreatePaymentRequest{
		Amount:            amount,
		Capture:           true,
		Confirmation:      &Confirmation{Type: "redirect", ReturnURL: returnURL},
		Description:       fmt.Sprintf("Dada Cloud: тариф %s", plan.Name),
		Receipt:           p.receiptFor(plan, amount, customerEmail),
		SavePaymentMethod: saveMethod,
	}

	payment, err := p.Client.CreatePayment(ctx, id.String(), req)
	if err != nil {
		if _, uerr := p.Pool.Exec(ctx, `
			UPDATE payments SET status = 'canceled', updated_at = $1 WHERE id = $2
		`, time.Now().UTC(), id); uerr != nil {
			return "", "", fmt.Errorf("yookassa: create payment: %w (also failed to mark canceled: %v)", err, uerr)
		}
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
// AutopayArmed reports that this payment also left the org with a reusable
// payment method, so the plan will renew itself instead of lapsing. The
// caller says so in the success mail: a charge the customer forgot they
// authorised is the most expensive kind of support ticket.
type WebhookResult struct {
	Outcome       WebhookOutcome
	OrgID         string
	Plan          string
	AmountValue   string
	CustomerEmail string
	AutopayArmed  bool
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
		if payment.PaymentMethod.Saved && payment.PaymentMethod.ID != "" {
			if err := storeAutopayMethodTx(ctx, tx, orgID, payment.PaymentMethod, now); err != nil {
				return WebhookResult{}, fmt.Errorf("yookassa: store payment method: %w", err)
			}
			result.AutopayArmed = true
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

// ChargeOutcome is what one recurring charge attempt did.
//
// ChargeSucceeded: YooKassa took the money synchronously and the plan term
// was extended in the same transaction. Nothing further is expected.
//
// ChargePending: the charge was accepted but is not final yet. The payments
// row stays pending and the webhook finishes it; the caller must NOT count
// this as a failure and must not retry, or the customer pays twice.
//
// ChargeFailed: YooKassa refused (declined card, expired method, no funds).
// The caller counts the failure and eventually tells the customer to renew
// by hand.
type ChargeOutcome string

const (
	ChargeSucceeded ChargeOutcome = "succeeded"
	ChargePending   ChargeOutcome = "pending"
	ChargeFailed    ChargeOutcome = "failed"
)

// ChargeResult reports one recurring charge. Reason is the YooKassa-supplied
// explanation on ChargeFailed, safe to put in front of a customer.
type ChargeResult struct {
	Outcome     ChargeOutcome
	PaymentID   string
	AmountValue string
	Reason      string
}

// autopayCreatedBySub marks the payments rows nobody clicked for. Every other
// row carries the OIDC subject of the person who pressed Pay; a renewal has no
// such person, and inventing one would put a customer's name on a charge they
// were asleep for.
const autopayCreatedBySub = "system:autopay"

// ChargeSaved renews a plan without the customer present, using the payment
// method saved during their last checkout. The amount always comes from the
// plan catalog passed in by the caller, never from the stored payments
// history: a price change must take effect on renewal, and a stale amount
// copied forward is how a subscription quietly charges the wrong number.
//
// The payments row is inserted BEFORE the API call, with the same UUID used
// as the Idempotence-Key, so a timeout between the call and its answer cannot
// produce a second charge on retry: YooKassa collapses the retry onto the
// same payment, and the row is already there to match it.
//
// A synchronously succeeded charge extends the term here. Anything else is
// left to the webhook, which is the authoritative path for every payment.
func (p *YooKassaProvider) ChargeSaved(ctx context.Context, orgID string, plan pricing.Plan, methodID, customerEmail string) (ChargeResult, error) {
	if methodID == "" {
		return ChargeResult{}, errors.New("yookassa: charge without a saved payment method")
	}
	if err := p.requireReceiptEmail(customerEmail); err != nil {
		return ChargeResult{}, err
	}
	id := uuid.New()
	amountValue := fmt.Sprintf("%.2f", plan.PriceRUB)

	if _, err := p.Pool.Exec(ctx, `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, customer_email, created_by_sub, is_recurring)
		VALUES ($1, $2, $3, $4, 'RUB', 'pending', $5, $6, TRUE)
	`, id, orgID, plan.Key, amountValue, customerEmail, autopayCreatedBySub); err != nil {
		return ChargeResult{}, fmt.Errorf("yookassa: insert recurring payment: %w", err)
	}

	amount := Amount{Value: amountValue, Currency: "RUB"}
	payment, err := p.Client.CreatePayment(ctx, id.String(), CreatePaymentRequest{
		Amount:          amount,
		Capture:         true,
		Description:     fmt.Sprintf("Dada Cloud: продление тарифа %s", plan.Name),
		Receipt:         p.receiptFor(plan, amount, customerEmail),
		PaymentMethodID: methodID,
	})
	if err != nil {
		reason := err.Error()
		var apiErr *Error
		if errors.As(err, &apiErr) && apiErr.Description != "" {
			reason = apiErr.Description
		}
		if _, uerr := p.Pool.Exec(ctx, `
			UPDATE payments SET status = 'canceled', updated_at = $1 WHERE id = $2
		`, time.Now().UTC(), id); uerr != nil {
			return ChargeResult{}, fmt.Errorf("yookassa: mark recurring payment failed: %w", uerr)
		}
		return ChargeResult{Outcome: ChargeFailed, PaymentID: id.String(), AmountValue: amountValue, Reason: reason}, nil
	}

	now := time.Now().UTC()
	if _, err := p.Pool.Exec(ctx, `
		UPDATE payments SET yk_payment_id = $1, updated_at = $2 WHERE id = $3
	`, payment.ID, now, id); err != nil {
		return ChargeResult{}, fmt.Errorf("yookassa: store recurring yk payment id: %w", err)
	}

	switch payment.Status {
	case "succeeded":
		tx, err := p.Pool.Begin(ctx)
		if err != nil {
			return ChargeResult{}, fmt.Errorf("yookassa: begin recurring tx: %w", err)
		}
		defer tx.Rollback(ctx)
		tag, err := tx.Exec(ctx, `
			UPDATE payments SET status = 'succeeded', paid_at = $1, updated_at = $1 WHERE id = $2 AND status = 'pending'
		`, now, id)
		if err != nil {
			return ChargeResult{}, fmt.Errorf("yookassa: mark recurring succeeded: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ChargeResult{Outcome: ChargeSucceeded, PaymentID: id.String(), AmountValue: amountValue}, nil
		}
		if err := assignPlanTx(ctx, tx, orgID, plan.Key, now); err != nil {
			return ChargeResult{}, fmt.Errorf("yookassa: extend plan after recurring charge: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return ChargeResult{}, fmt.Errorf("yookassa: commit recurring charge: %w", err)
		}
		return ChargeResult{Outcome: ChargeSucceeded, PaymentID: id.String(), AmountValue: amountValue}, nil

	case "canceled":
		if _, err := p.Pool.Exec(ctx, `
			UPDATE payments SET status = 'canceled', updated_at = $1 WHERE id = $2 AND status = 'pending'
		`, now, id); err != nil {
			return ChargeResult{}, fmt.Errorf("yookassa: mark recurring canceled: %w", err)
		}
		return ChargeResult{Outcome: ChargeFailed, PaymentID: id.String(), AmountValue: amountValue, Reason: "платёж отклонён"}, nil

	default:
		return ChargeResult{Outcome: ChargePending, PaymentID: id.String(), AmountValue: amountValue}, nil
	}
}

// storeAutopayMethodTx arms auto-renewal for an org: the saved method handle,
// its display title, consent switched on, and the failure counter cleared so
// a card replaced after three declines gets a full set of retries again.
func storeAutopayMethodTx(ctx context.Context, tx pgx.Tx, orgID string, method PaymentMethod, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE billing_accounts
		SET autopay_enabled = TRUE,
		    autopay_method_id = $2,
		    autopay_method_title = $3,
		    autopay_failures = 0,
		    updated_at = $4
		WHERE org_id = $1
	`, orgID, method.ID, method.Title, now)
	return err
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
