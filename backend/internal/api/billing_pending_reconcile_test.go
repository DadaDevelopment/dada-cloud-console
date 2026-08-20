package api

import (
	"context"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubReconciler stands in for the YooKassa provider: it records which payment
// ids the sweeper asked about and replays a canned outcome per id.
type stubReconciler struct {
	asked    []string
	outcomes map[string]yookassa.WebhookOutcome
}

func (s *stubReconciler) ProcessWebhook(_ context.Context, ykPaymentID string) (yookassa.WebhookResult, error) {
	s.asked = append(s.asked, ykPaymentID)
	outcome, ok := s.outcomes[ykPaymentID]
	if !ok {
		outcome = yookassa.OutcomeNoop
	}
	return yookassa.WebhookResult{Outcome: outcome, OrgID: "", Plan: "startup"}, nil
}

// seedPendingPayment inserts one pending payments row with an explicit age and
// yk_payment_id, and cleans up everything it touches for that org.
func seedPendingPayment(t *testing.T, pool *pgxpool.Pool, orgID, ykPaymentID string, createdAt time.Time) uuid.UUID {
	t.Helper()
	paymentID := uuid.New()
	var ykValue any
	if ykPaymentID != "" {
		ykValue = ykPaymentID
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, created_by_sub, yk_payment_id, confirmation_url, created_at)
		VALUES ($1, $2, 'startup', '990.00', 'RUB', 'pending', 'sub-1', $3, 'https://yoomoney.example/confirm', $4)
	`, paymentID, orgID, ykValue, createdAt)
	if err != nil {
		t.Fatalf("seed pending payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE resource_name = $1`, orgID)
	})
	return paymentID
}

// seedPendingInvoice inserts one pending, payment_method='invoice' row -- the
// shape billing_invoice.go CreateInvoice produces for a legal-entity payer --
// with no yk_payment_id, since an invoice never goes through YooKassa.
func seedPendingInvoice(t *testing.T, pool *pgxpool.Pool, orgID string, createdAt time.Time) uuid.UUID {
	t.Helper()
	paymentID := uuid.New()
	invoiceNumber := "INV-TEST-" + uuid.NewString()[:8]
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, payment_method, invoice_number, created_by_sub, created_at)
		VALUES ($1, $2, 'startup', '990.00', 'RUB', 'pending', 'invoice', $3, 'sub-1', $4)
	`, paymentID, orgID, invoiceNumber, createdAt)
	if err != nil {
		t.Fatalf("seed pending invoice: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE resource_name = $1`, orgID)
	})
	return paymentID
}

func readPaymentStatus(t *testing.T, pool *pgxpool.Pool, paymentID uuid.UUID) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM payments WHERE id = $1`, paymentID,
	).Scan(&status); err != nil {
		t.Fatalf("read payment status: %v", err)
	}
	return status
}

// A pending row older than the reconcile window must be re-asked. This is the
// whole point of the sweeper: the only thing that ever settled a payment was
// an inbound webhook, so a delivery that never arrived stranded a paying
// customer forever.
func TestSweepPendingPayments_AsksYooKassaAboutStalePendingRows(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-pendrec-" + uuid.NewString()[:8]
	ykID := "yk-" + uuid.NewString()[:8]

	seedPendingPayment(t, pool, orgID, ykID, now.Add(-40*time.Minute))
	rec := &stubReconciler{outcomes: map[string]yookassa.WebhookOutcome{ykID: yookassa.OutcomeSucceeded}}

	SweepPendingPayments(context.Background(), pool, rec, now)

	found := false
	for _, id := range rec.asked {
		if id == ykID {
			found = true
		}
	}
	if !found {
		t.Fatalf("sweeper did not re-ask YooKassa about yk_payment_id=%s (asked=%v); a lost webhook would leave this payment pending forever", ykID, rec.asked)
	}
	if n := countAuditRows(t, pool, orgID, "PaymentReconciled"); n != 1 {
		t.Fatalf("PaymentReconciled audit rows for org=%s = %d, want 1: a payment that settled by sweep must leave a trail", orgID, n)
	}
}

// A payment the customer simply has not finished yet is left alone: no audit
// row, and it stays pending for the next tick.
func TestSweepPendingPayments_StillPendingAtProviderIsNotAnnounced(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-pendnoop-" + uuid.NewString()[:8]
	ykID := "yk-" + uuid.NewString()[:8]

	paymentID := seedPendingPayment(t, pool, orgID, ykID, now.Add(-40*time.Minute))
	rec := &stubReconciler{outcomes: map[string]yookassa.WebhookOutcome{ykID: yookassa.OutcomeNoop}}

	SweepPendingPayments(context.Background(), pool, rec, now)

	if got := readPaymentStatus(t, pool, paymentID); got != "pending" {
		t.Fatalf("status = %q, want \"pending\": an unfinished payment must not be closed by the sweeper", got)
	}
	if n := countAuditRows(t, pool, orgID, "PaymentReconciled"); n != 0 {
		t.Fatalf("PaymentReconciled audit rows = %d, want 0: an audit row per tick per unfinished payment turns the signal into noise", n)
	}
}

// A row younger than the reconcile window belongs to a checkout that may still
// be open in the customer's browser. Touching it would be pointless provider
// traffic at best.
func TestSweepPendingPayments_LeavesFreshRowsAlone(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-pendfresh-" + uuid.NewString()[:8]
	ykID := "yk-" + uuid.NewString()[:8]

	seedPendingPayment(t, pool, orgID, ykID, now.Add(-2*time.Minute))
	rec := &stubReconciler{outcomes: map[string]yookassa.WebhookOutcome{}}

	SweepPendingPayments(context.Background(), pool, rec, now)

	for _, id := range rec.asked {
		if id == ykID {
			t.Fatalf("sweeper asked about yk_payment_id=%s created 2 minutes ago; a live checkout page must not be reconciled out from under the customer", ykID)
		}
	}
}

// A pending row that never got a yk_payment_id has no path to becoming
// terminal -- no YooKassa payment exists, so no webhook is ever coming. After
// a day it is closed, and the closure is recorded.
func TestSweepPendingPayments_ClosesRowsThatNeverReachedYooKassa(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-pendaband-" + uuid.NewString()[:8]

	paymentID := seedPendingPayment(t, pool, orgID, "", now.Add(-30*time.Hour))
	rec := &stubReconciler{outcomes: map[string]yookassa.WebhookOutcome{}}

	SweepPendingPayments(context.Background(), pool, rec, now)

	if got := readPaymentStatus(t, pool, paymentID); got != "canceled" {
		t.Fatalf("status = %q, want \"canceled\": a row with no yk_payment_id can never settle and must not stay pending forever", got)
	}
	if n := countAuditRows(t, pool, orgID, "PaymentAbandoned"); n != 1 {
		t.Fatalf("PaymentAbandoned audit rows for org=%s = %d, want 1: closing a customer's payment attempt with no trail is how the original incident stayed invisible", orgID, n)
	}
}

// The 24h floor is real: a row without a yk_payment_id that is only a few
// hours old may still be a slow write from Checkout.
func TestSweepPendingPayments_KeepsRecentRowsWithoutYooKassaID(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-pendyoung-" + uuid.NewString()[:8]

	paymentID := seedPendingPayment(t, pool, orgID, "", now.Add(-3*time.Hour))
	rec := &stubReconciler{outcomes: map[string]yookassa.WebhookOutcome{}}

	SweepPendingPayments(context.Background(), pool, rec, now)

	if got := readPaymentStatus(t, pool, paymentID); got != "pending" {
		t.Fatalf("status = %q, want \"pending\": a 3-hour-old row may still be a slow write of yk_payment_id", got)
	}
}

// The regression this fix closes: INV-2026-00001 was a legal-entity invoice,
// inserted pending with no yk_payment_id by design (it settles by bank-statement
// match, not YooKassa), and got killed by the no-yk_payment_id rule at the
// pendingPaymentAbandonAfter (24h) mark while the wire transfer was still in
// flight. A pending invoice must survive past 24h.
func TestSweepPendingPayments_DoesNotAbandonInvoiceAt24h(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-pendinv24h-" + uuid.NewString()[:8]

	paymentID := seedPendingInvoice(t, pool, orgID, now.Add(-30*time.Hour))

	SweepPendingPayments(context.Background(), pool, nil, now)

	if got := readPaymentStatus(t, pool, paymentID); got != "pending" {
		t.Fatalf("status = %q, want \"pending\": an invoice has no yk_payment_id by design and must not be judged by the card-checkout 24h rule", got)
	}
}

// An invoice does eventually time out, just on its own, much longer, clock:
// pendingInvoiceAbandonAfter (14 days).
func TestSweepPendingPayments_AbandonsInvoiceAfter14Days(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-pendinv14d-" + uuid.NewString()[:8]

	paymentID := seedPendingInvoice(t, pool, orgID, now.Add(-15*24*time.Hour))

	SweepPendingPayments(context.Background(), pool, nil, now)

	if got := readPaymentStatus(t, pool, paymentID); got != "canceled" {
		t.Fatalf("status = %q, want \"canceled\": an invoice unpaid for 15 days must eventually close, on its own clock", got)
	}
	if n := countAuditRows(t, pool, orgID, "PaymentAbandoned"); n != 1 {
		t.Fatalf("PaymentAbandoned audit rows for org=%s = %d, want 1", orgID, n)
	}
}

// With payments unconfigured the reconciler is nil. The abandon half must
// still run -- it needs no provider at all -- and nothing may panic.
func TestSweepPendingPayments_NilReconcilerStillClosesAbandonedRows(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-pendnil-" + uuid.NewString()[:8]

	paymentID := seedPendingPayment(t, pool, orgID, "", now.Add(-30*time.Hour))

	SweepPendingPayments(context.Background(), pool, nil, now)

	if got := readPaymentStatus(t, pool, paymentID); got != "canceled" {
		t.Fatalf("status = %q, want \"canceled\": the abandon path needs no payment provider", got)
	}
}
