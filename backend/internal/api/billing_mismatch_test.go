package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func countAuditRows(t *testing.T, pool *pgxpool.Pool, orgID, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_events WHERE resource_name = $1 AND action = $2`,
		orgID, action,
	).Scan(&n); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	return n
}

func TestSweepPaymentPlanMismatch_FindsManualDiscrepancy(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-mismatch-" + uuid.NewString()[:8]

	paymentID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, created_by_sub, paid_at)
		VALUES ($1, $2, 'startup', '990.00', 'RUB', 'succeeded', 'sub-1', $3)
	`, paymentID, orgID, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("seed succeeded payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE resource_name = $1`, orgID)
	})

	SweepPaymentPlanMismatch(context.Background(), pool, "", now)

	n := countAuditRows(t, pool, orgID, "PaymentPlanMismatchDetected")
	if n != 1 {
		t.Fatalf("audit rows for org=%s = %d, want exactly 1: a succeeded payment with no billing_accounts row must be flagged", orgID, n)
	}
}

func TestSweepPaymentPlanMismatch_HealthyPairIsSilent(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-healthy-" + uuid.NewString()[:8]

	paymentID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, created_by_sub, paid_at)
		VALUES ($1, $2, 'startup', '990.00', 'RUB', 'succeeded', 'sub-1', $3)
	`, paymentID, orgID, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("seed succeeded payment: %v", err)
	}
	_, err = pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, updated_at)
		VALUES ($1, 'startup', $2::timestamptz, $2::timestamptz + interval '29 days', $2::timestamptz)
	`, orgID, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("seed matching billing_accounts row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE resource_name = $1`, orgID)
	})

	SweepPaymentPlanMismatch(context.Background(), pool, "", now)

	n := countAuditRows(t, pool, orgID, "PaymentPlanMismatchDetected")
	if n != 0 {
		t.Fatalf("audit rows for org=%s = %d, want 0: a payment matched by an active paid plan must not be flagged", orgID, n)
	}
}

func TestSweepPaymentPlanMismatch_HonestlyExpiredIsSilent(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-lapsed-" + uuid.NewString()[:8]

	paymentID := uuid.New()
	longAgo := now.Add(-40 * 24 * time.Hour)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, created_by_sub, paid_at)
		VALUES ($1, $2, 'startup', '990.00', 'RUB', 'succeeded', 'sub-1', $3)
	`, paymentID, orgID, longAgo)
	if err != nil {
		t.Fatalf("seed old succeeded payment: %v", err)
	}
	_, err = pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, updated_at)
		VALUES ($1, 'free', $2, $2)
	`, orgID, now)
	if err != nil {
		t.Fatalf("seed lapsed-to-free billing_accounts row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE resource_name = $1`, orgID)
	})

	SweepPaymentPlanMismatch(context.Background(), pool, "", now)

	n := countAuditRows(t, pool, orgID, "PaymentPlanMismatchDetected")
	if n != 0 {
		t.Fatalf("audit rows for org=%s = %d, want 0: a 40-day-old payment whose term (30d+grace) has honestly lapsed is not a discrepancy", orgID, n)
	}
}

func TestSweepPaymentPlanMismatch_DedupesAcrossTicks(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-dedup-" + uuid.NewString()[:8]

	paymentID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, created_by_sub, paid_at)
		VALUES ($1, $2, 'startup', '990.00', 'RUB', 'succeeded', 'sub-1', $3)
	`, paymentID, orgID, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("seed succeeded payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE resource_name = $1`, orgID)
	})

	SweepPaymentPlanMismatch(context.Background(), pool, "", now)
	SweepPaymentPlanMismatch(context.Background(), pool, "", now.Add(time.Minute))
	SweepPaymentPlanMismatch(context.Background(), pool, "", now.Add(2*time.Minute))

	n := countAuditRows(t, pool, orgID, "PaymentPlanMismatchDetected")
	if n != 1 {
		t.Fatalf("audit rows for org=%s after 3 ticks = %d, want exactly 1: an unresolved discrepancy must not be re-announced every tick", orgID, n)
	}
}
