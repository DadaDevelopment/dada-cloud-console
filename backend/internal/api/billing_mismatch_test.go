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

type accountState struct {
	plan       string
	assignedAt time.Time
	expiresAt  *time.Time
}

func readAccount(t *testing.T, pool *pgxpool.Pool, orgID string) (accountState, bool) {
	t.Helper()
	var st accountState
	err := pool.QueryRow(context.Background(),
		`SELECT plan, plan_assigned_at, plan_expires_at FROM billing_accounts WHERE org_id = $1`,
		orgID,
	).Scan(&st.plan, &st.assignedAt, &st.expiresAt)
	if err != nil {
		return accountState{}, false
	}
	return st, true
}

func seedSucceededPayment(t *testing.T, pool *pgxpool.Pool, orgID string, paidAt time.Time) uuid.UUID {
	t.Helper()
	paymentID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payments (id, org_id, plan, amount_value, currency, status, created_by_sub, paid_at)
		VALUES ($1, $2, 'startup', '990.00', 'RUB', 'succeeded', 'sub-1', $3)
	`, paymentID, orgID, paidAt)
	if err != nil {
		t.Fatalf("seed succeeded payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE resource_name = $1`, orgID)
	})
	return paymentID
}

func TestSweepPaymentPlanMismatch_GrantsThePaidPlan(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-mismatch-" + uuid.NewString()[:8]
	paidAt := now.Add(-12 * 24 * time.Hour)

	seedSucceededPayment(t, pool, orgID, paidAt)

	SweepPaymentPlanMismatch(context.Background(), pool, "", now)

	st, ok := readAccount(t, pool, orgID)
	if !ok {
		t.Fatalf("org=%s has no billing_accounts row: a succeeded payment must leave the org holding the plan it paid for", orgID)
	}
	if st.plan != "startup" {
		t.Fatalf("plan for org=%s = %q, want \"startup\"", orgID, st.plan)
	}
	if st.expiresAt == nil {
		t.Fatalf("plan_expires_at for org=%s is NULL, want a 30-day term", orgID)
	}
	wantExpiry := paidAt.Add(30 * 24 * time.Hour)
	if delta := st.expiresAt.Sub(wantExpiry); delta > time.Minute || delta < -time.Minute {
		t.Fatalf("plan_expires_at for org=%s = %s, want %s: the term the payment bought starts at paid_at, not at repair time",
			orgID, st.expiresAt.UTC().Format(time.RFC3339), wantExpiry.Format(time.RFC3339))
	}
	if n := countAuditRows(t, pool, orgID, "PaymentPlanReconciled"); n != 1 {
		t.Fatalf("PaymentPlanReconciled rows for org=%s = %d, want exactly 1", orgID, n)
	}
	if n := countAuditRows(t, pool, orgID, "PaymentPlanMismatchDetected"); n != 0 {
		t.Fatalf("PaymentPlanMismatchDetected rows for org=%s = %d, want 0: a repaired discrepancy is not an unresolved one", orgID, n)
	}
}

func TestSweepPaymentPlanMismatch_HealthyPairIsSilent(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-healthy-" + uuid.NewString()[:8]

	seedSucceededPayment(t, pool, orgID, now.Add(-1*time.Hour))
	_, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, updated_at)
		VALUES ($1, 'startup', $2::timestamptz, $2::timestamptz + interval '29 days', $2::timestamptz)
	`, orgID, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("seed matching billing_accounts row: %v", err)
	}

	SweepPaymentPlanMismatch(context.Background(), pool, "", now)

	if n := countAuditRows(t, pool, orgID, "PaymentPlanReconciled"); n != 0 {
		t.Fatalf("PaymentPlanReconciled rows for org=%s = %d, want 0: a payment matched by an active paid plan must not be touched", orgID, n)
	}
	st, _ := readAccount(t, pool, orgID)
	if delta := st.expiresAt.Sub(now.Add(-1 * time.Hour).Add(29 * 24 * time.Hour)); delta > time.Minute || delta < -time.Minute {
		t.Fatalf("plan_expires_at for org=%s moved to %s: the sweeper must never extend a healthy term", orgID, st.expiresAt.UTC().Format(time.RFC3339))
	}
}

func TestSweepPaymentPlanMismatch_HonestlyExpiredIsSilent(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-lapsed-" + uuid.NewString()[:8]

	seedSucceededPayment(t, pool, orgID, now.Add(-40*24*time.Hour))
	_, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, updated_at)
		VALUES ($1, 'free', $2, $2)
	`, orgID, now)
	if err != nil {
		t.Fatalf("seed lapsed-to-free billing_accounts row: %v", err)
	}

	SweepPaymentPlanMismatch(context.Background(), pool, "", now)

	if n := countAuditRows(t, pool, orgID, "PaymentPlanReconciled"); n != 0 {
		t.Fatalf("PaymentPlanReconciled rows for org=%s = %d, want 0: a 40-day-old payment whose term has honestly lapsed must not be revived", orgID, n)
	}
	st, _ := readAccount(t, pool, orgID)
	if st.plan != "free" {
		t.Fatalf("plan for org=%s = %q, want \"free\": an expired term stays expired", orgID, st.plan)
	}
}

func TestSweepPaymentPlanMismatch_RepeatedTicksRepairOnce(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-dedup-" + uuid.NewString()[:8]
	paidAt := now.Add(-1 * time.Hour)

	seedSucceededPayment(t, pool, orgID, paidAt)

	SweepPaymentPlanMismatch(context.Background(), pool, "", now)
	SweepPaymentPlanMismatch(context.Background(), pool, "", now.Add(time.Minute))
	SweepPaymentPlanMismatch(context.Background(), pool, "", now.Add(2*time.Minute))

	if n := countAuditRows(t, pool, orgID, "PaymentPlanReconciled"); n != 1 {
		t.Fatalf("PaymentPlanReconciled rows for org=%s after 3 ticks = %d, want exactly 1: once repaired, the org is no longer a candidate", orgID, n)
	}
	st, _ := readAccount(t, pool, orgID)
	wantExpiry := paidAt.Add(30 * 24 * time.Hour)
	if delta := st.expiresAt.Sub(wantExpiry); delta > time.Minute || delta < -time.Minute {
		t.Fatalf("plan_expires_at for org=%s = %s, want %s: repeated ticks must not stack terms",
			orgID, st.expiresAt.UTC().Format(time.RFC3339), wantExpiry.Format(time.RFC3339))
	}
}

func TestSweepPaymentPlanMismatch_UnrepairableIsReportedOncePerWindow(t *testing.T) {
	pool := testPaymentsPool(t)
	now := time.Now().UTC()
	orgID := "org-unrepairable-" + uuid.NewString()[:8]

	m := mismatchRow{
		PaymentID:   uuid.New(),
		OrgID:       orgID,
		Plan:        "startup",
		PaidAt:      now.Add(-2 * time.Hour),
		HasAccount:  false,
		AccountPlan: "free",
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE resource_name = $1`, orgID)
	})

	cause := context.DeadlineExceeded
	reportPaymentMismatch(context.Background(), pool, "", m, now, cause)
	reportPaymentMismatch(context.Background(), pool, "", m, now.Add(time.Minute), cause)
	reportPaymentMismatch(context.Background(), pool, "", m, now.Add(2*time.Hour), cause)

	if n := countAuditRows(t, pool, orgID, "PaymentPlanMismatchDetected"); n != 1 {
		t.Fatalf("PaymentPlanMismatchDetected rows for org=%s = %d, want exactly 1: the dedup window lives in audit_events, so it survives restarts and a second replica", orgID, n)
	}

	reportPaymentMismatch(context.Background(), pool, "", m, now.Add(paymentMismatchDedupWindow+time.Minute), cause)
	if n := countAuditRows(t, pool, orgID, "PaymentPlanMismatchDetected"); n != 2 {
		t.Fatalf("PaymentPlanMismatchDetected rows for org=%s after the window elapsed = %d, want 2: a still-broken payment must be re-announced once the window passes", orgID, n)
	}
}
