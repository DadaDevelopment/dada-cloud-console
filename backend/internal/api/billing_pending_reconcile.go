package api

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pendingPaymentReconcileAfter is how long a pending row is left alone before
// the sweeper asks YooKassa what actually happened to it. A customer-present
// payment either completes or is abandoned within a few minutes; fifteen is
// long enough that a live checkout page is never interrupted by a
// reconciliation, and short enough that a lost webhook costs the customer at
// most one hour of not having the plan they paid for.
const pendingPaymentReconcileAfter = 15 * time.Minute

// pendingPaymentAbandonAfter bounds how long a row with no yk_payment_id may
// stay pending. Such a row has no path to becoming terminal -- no YooKassa
// payment exists, so no webhook is ever coming -- and every one of them is a
// customer who tried to pay and could not. A day is enough for a slow write of
// the id (Checkout stores it immediately after CreatePayment) to have landed.
const pendingPaymentAbandonAfter = 24 * time.Hour

// pendingPaymentSweepLimit bounds one pass. Each candidate costs one YooKassa
// API call, and a backlog is better spread over several hourly ticks than
// turned into a burst against the provider.
const pendingPaymentSweepLimit = 200

// pendingPaymentReconciler is the slice of yookassa.YooKassaProvider this
// sweeper needs. Reconciliation deliberately goes through ProcessWebhook
// rather than through its own copy of the terminal-status logic: a payment
// that settles by sweep must leave exactly the same rows as one that settles
// by webhook (payment flipped, plan assigned, method saved -- one
// transaction), and a second implementation of that is a second place for the
// two to drift apart.
type pendingPaymentReconciler interface {
	ProcessWebhook(ctx context.Context, ykPaymentID string) (yookassa.WebhookResult, error)
}

// NewPendingPaymentReconciler builds the reconciler the sweeper uses, or
// returns an untyped nil when payments are unconfigured -- mirroring
// NewAutopayCharger, and for the same reason: a typed nil pointer behind the
// interface passes the sweeper's nil check and then panics on the first
// candidate.
func NewPendingPaymentReconciler(pool *pgxpool.Pool, cfg *config.Config) pendingPaymentReconciler {
	if cfg.YooKassaShopID == "" || cfg.YooKassaSecretKey == "" {
		return nil
	}
	client := yookassa.New(cfg.YooKassaShopID, cfg.YooKassaSecretKey)
	return yookassa.NewProvider(pool, client, cfg.YooKassaReturnURL, cfg.YooKassaSendReceipt, cfg.YooKassaVatCode, cfg.YooKassaTaxSystemCode)
}

// pendingPaymentRow is one payments row the sweeper is about to resolve.
type pendingPaymentRow struct {
	ID          uuid.UUID
	OrgID       string
	Plan        string
	YKPaymentID string
	CreatedAt   time.Time
}

// SweepPendingPayments resolves payments that stopped halfway.
//
// A YooKassa webhook is the only thing that ever moved a payments row out of
// "pending", so a delivery that never arrived left the row -- and the customer
// -- stuck forever: on 2026-08-14 a real payer's two attempts sat pending with
// nothing anywhere saying so, and were found a day later only by scanning the
// table by hand. Autopay, expiry, grace and plan-mismatch all had sweepers;
// this path did not.
//
// Two populations, two remedies:
//
// Rows WITH a yk_payment_id older than pendingPaymentReconcileAfter are
// re-asked through ProcessWebhook, which re-fetches the authoritative status
// and applies it exactly as an inbound webhook would. A payment still pending
// at YooKassa stays pending here (OutcomeNoop) and is retried on the next
// tick.
//
// Rows WITHOUT a yk_payment_id older than pendingPaymentAbandonAfter are
// closed as canceled: no YooKassa payment was ever created for them, so
// nothing can ever settle them, and leaving them pending both lies to the
// customer's payment history and inflates every "payments in flight" count.
//
// Registered and ticked alongside the other billing sweepers, see
// cmd/server/main.go.
func SweepPendingPayments(ctx context.Context, pool *pgxpool.Pool, rec pendingPaymentReconciler, now time.Time) {
	if rec != nil {
		for _, row := range listPendingPaymentsToReconcile(ctx, pool, now) {
			reconcilePendingPayment(ctx, pool, rec, row)
		}
	}
	for _, row := range listAbandonedPendingPayments(ctx, pool, now) {
		abandonPendingPayment(ctx, pool, row, now)
	}
	metrics.MarkPaymentSweep(time.Now())
}

// listPendingPaymentsToReconcile returns the pending rows that have a YooKassa
// payment to ask about. Oldest first: a backlog longer than one pass should
// drain from the end that has been waiting longest, not from whatever the
// planner happened to return.
func listPendingPaymentsToReconcile(ctx context.Context, pool *pgxpool.Pool, now time.Time) []pendingPaymentRow {
	rows, err := pool.Query(ctx, `
		SELECT id, org_id, plan, yk_payment_id, created_at
		FROM payments
		WHERE status = 'pending'
		  AND yk_payment_id IS NOT NULL AND yk_payment_id <> ''
		  AND created_at < $1::timestamptz
		ORDER BY created_at
		LIMIT $2
	`, now.Add(-pendingPaymentReconcileAfter), pendingPaymentSweepLimit)
	if err != nil {
		log.Printf("pending payments: list reconcilable: %v", err)
		return nil
	}
	defer rows.Close()

	out := make([]pendingPaymentRow, 0)
	for rows.Next() {
		var r pendingPaymentRow
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Plan, &r.YKPaymentID, &r.CreatedAt); err != nil {
			log.Printf("pending payments: scan reconcilable row: %v", err)
			return out
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		log.Printf("pending payments: read reconcilable rows: %v", err)
	}
	return out
}

// reconcilePendingPayment asks YooKassa what happened to one payment and lets
// ProcessWebhook apply the answer. Only a terminal outcome is announced: a
// payment the customer has simply not finished yet is not news, and an audit
// row per tick per unfinished payment is how a useful signal becomes noise.
func reconcilePendingPayment(ctx context.Context, pool *pgxpool.Pool, rec pendingPaymentReconciler, row pendingPaymentRow) {
	result, err := rec.ProcessWebhook(ctx, row.YKPaymentID)
	if err != nil {
		log.Printf("pending payments: reconcile payment=%s yk=%s: %v", row.ID, row.YKPaymentID, err)
		return
	}
	switch result.Outcome {
	case yookassa.OutcomeSucceeded, yookassa.OutcomeCanceled:
		metrics.RecordPaymentReconciled(string(result.Outcome))
		log.Printf("pending payments: reconciled payment=%s org=%s plan=%s outcome=%s",
			row.ID, row.OrgID, row.Plan, result.Outcome)
		writeAuditRow(ctx, pool, systemDeployActorID, auditEntry{
			Action:       "PaymentReconciled",
			ResourceKind: "Payment",
			ResourceName: row.OrgID,
			Outcome:      auditOutcomeSuccess,
			Metadata: map[string]string{
				"payment_id":    row.ID.String(),
				"yk_payment_id": row.YKPaymentID,
				"plan":          row.Plan,
				"outcome":       string(result.Outcome),
				"pending_since": row.CreatedAt.UTC().Format(time.RFC3339),
			},
		})
	}
}

// listAbandonedPendingPayments returns the pending rows that never got a
// YooKassa payment at all.
func listAbandonedPendingPayments(ctx context.Context, pool *pgxpool.Pool, now time.Time) []pendingPaymentRow {
	rows, err := pool.Query(ctx, `
		SELECT id, org_id, plan, created_at
		FROM payments
		WHERE status = 'pending'
		  AND (yk_payment_id IS NULL OR yk_payment_id = '')
		  AND created_at < $1::timestamptz
		ORDER BY created_at
		LIMIT $2
	`, now.Add(-pendingPaymentAbandonAfter), pendingPaymentSweepLimit)
	if err != nil {
		log.Printf("pending payments: list abandoned: %v", err)
		return nil
	}
	defer rows.Close()

	out := make([]pendingPaymentRow, 0)
	for rows.Next() {
		var r pendingPaymentRow
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Plan, &r.CreatedAt); err != nil {
			log.Printf("pending payments: scan abandoned row: %v", err)
			return out
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		log.Printf("pending payments: read abandoned rows: %v", err)
	}
	return out
}

// abandonPendingPayment closes one unsettleable row and records why, both in
// the SAME transaction. The split is what let the original incident go
// unnoticed: a row that turns canceled on its own says nothing about having
// happened, and the only other witness was a pod's stdout.
//
// The UPDATE re-checks status = 'pending' so a webhook that lands between the
// listing query and this write is never overwritten -- zero rows affected
// means somebody else settled it, which is the desired end state, not an
// error, and no audit row is written for it.
func abandonPendingPayment(ctx context.Context, pool *pgxpool.Pool, row pendingPaymentRow, now time.Time) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Printf("pending payments: begin abandon tx payment=%s: %v", row.ID, err)
		return
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE payments SET status = 'canceled', updated_at = $1 WHERE id = $2 AND status = 'pending'
	`, now, row.ID)
	if err != nil {
		log.Printf("pending payments: mark abandoned payment=%s: %v", row.ID, err)
		return
	}
	if tag.RowsAffected() == 0 {
		return
	}

	meta, merr := json.Marshal(map[string]string{
		"payment_id":    row.ID.String(),
		"plan":          row.Plan,
		"pending_since": row.CreatedAt.UTC().Format(time.RFC3339),
		"reason":        "no_yk_payment_id",
	})
	if merr != nil {
		meta = []byte(`{}`)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_id, action, resource_kind, resource_name, outcome, metadata, actor_type)
		VALUES ($1, 'PaymentAbandoned', 'Payment', $2, 'failure', $3, 'system')
	`, systemDeployActorID, row.OrgID, meta); err != nil {
		log.Printf("pending payments: audit abandoned payment=%s: %v", row.ID, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("pending payments: commit abandon payment=%s: %v", row.ID, err)
		return
	}
	metrics.RecordPaymentReconciled("abandoned")
	log.Printf("pending payments: abandoned payment=%s org=%s plan=%s created_at=%s (never reached YooKassa)",
		row.ID, row.OrgID, row.Plan, row.CreatedAt.Format(time.RFC3339))
}
