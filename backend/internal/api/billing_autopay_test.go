package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubCharger stands in for the YooKassa provider. It records every org it was
// asked to charge, which is what most of these tests assert on: the sweeper's
// job is deciding WHO gets charged and how often, not how a charge is made.
type stubCharger struct {
	outcome yookassa.ChargeOutcome
	reason  string
	err     error
	charged []string
}

func (s *stubCharger) ChargeSaved(ctx context.Context, orgID string, plan pricing.Plan, methodID, customerEmail string) (yookassa.ChargeResult, error) {
	s.charged = append(s.charged, orgID)
	if s.err != nil {
		return yookassa.ChargeResult{}, s.err
	}
	return yookassa.ChargeResult{
		Outcome:     s.outcome,
		PaymentID:   uuid.NewString(),
		AmountValue: "990.00",
		Reason:      s.reason,
	}, nil
}

func (s *stubCharger) chargedOrg(orgID string) int {
	n := 0
	for _, id := range s.charged {
		if id == orgID {
			n++
		}
	}
	return n
}

// seedAutopayAccount creates a paid account armed for automatic renewal.
func seedAutopayAccount(t *testing.T, pool *pgxpool.Pool, expiresAt time.Time, failures int, lastAttempt *time.Time, email string) string {
	t.Helper()
	orgID := "org-autopay-" + uuid.NewString()[:8]
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at,
		                              autopay_enabled, autopay_method_id, autopay_method_title,
		                              autopay_failures, autopay_last_attempt_at, updated_at)
		VALUES ($1, 'startup', now(), $2, TRUE, 'pm_stub', 'Bank card *4444', $3, $4, now())
	`, orgID, expiresAt, failures, lastAttempt); err != nil {
		t.Fatalf("seed autopay account: %v", err)
	}
	if email != "" {
		if _, err := pool.Exec(ctx, `
			INSERT INTO payments (id, org_id, plan, amount_value, currency, status, customer_email, created_by_sub, paid_at)
			VALUES ($1, $2, 'startup', '990.00', 'RUB', 'succeeded', $3, 'sub-test', now())
		`, uuid.New(), orgID, email); err != nil {
			t.Fatalf("seed succeeded payment: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})
	return orgID
}

func autopayState(t *testing.T, pool *pgxpool.Pool, orgID string) (enabled bool, failures int, lastAttempt *time.Time) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT autopay_enabled, autopay_failures, autopay_last_attempt_at FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&enabled, &failures, &lastAttempt); err != nil {
		t.Fatalf("read autopay state: %v", err)
	}
	return enabled, failures, lastAttempt
}

func TestSweepAutopay_InsideLeadTime_ChargesAndMails(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedAutopayAccount(t, pool, now.Add(12*time.Hour), 0, nil, "buyer@example.com")

	charger := &stubCharger{outcome: yookassa.ChargeSucceeded}
	mailer := &recordingMailer{}
	SweepAutopay(context.Background(), pool, charger, mailer, "ops@example.com", testPlans(), now)

	if charger.chargedOrg(orgID) != 1 {
		t.Fatalf("charges for %s = %d want 1", orgID, charger.chargedOrg(orgID))
	}
	if len(mailer.sends) != 2 {
		t.Fatalf("sends=%d want 2 (customer receipt notice + operator copy); a silent charge is a chargeback waiting to happen", len(mailer.sends))
	}
	if mailer.sends[0].to != "buyer@example.com" {
		t.Fatalf("first send to=%q want the customer", mailer.sends[0].to)
	}
	_, failures, lastAttempt := autopayState(t, pool, orgID)
	if failures != 0 {
		t.Fatalf("failures=%d want 0 after a successful charge", failures)
	}
	if lastAttempt == nil {
		t.Fatal("autopay_last_attempt_at still NULL; without it the retry spacing and the cross-replica claim both stop working")
	}
}

func TestSweepAutopay_OutsideLeadTime_DoesNotCharge(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedAutopayAccount(t, pool, now.Add(10*24*time.Hour), 0, nil, "buyer@example.com")

	charger := &stubCharger{outcome: yookassa.ChargeSucceeded}
	SweepAutopay(context.Background(), pool, charger, &recordingMailer{}, "", testPlans(), now)

	if n := charger.chargedOrg(orgID); n != 0 {
		t.Fatalf("charges=%d for a term ending in 10 days; renewing that early takes money for a month the customer has not reached", n)
	}
}

func TestSweepAutopay_PastGrace_DoesNotCharge(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedAutopayAccount(t, pool, now.Add(-4*24*time.Hour), 0, nil, "buyer@example.com")

	charger := &stubCharger{outcome: yookassa.ChargeSucceeded}
	SweepAutopay(context.Background(), pool, charger, &recordingMailer{}, "", testPlans(), now)

	if n := charger.chargedOrg(orgID); n != 0 {
		t.Fatalf("charges=%d past expiry+grace; SweepPlanExpiry has already lapsed that account to free, so this is money for a plan the customer no longer has", n)
	}
}

func TestSweepAutopay_RecentAttempt_IsNotRetried(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)
	orgID := seedAutopayAccount(t, pool, now.Add(12*time.Hour), 1, &recent, "buyer@example.com")

	charger := &stubCharger{outcome: yookassa.ChargeSucceeded}
	SweepAutopay(context.Background(), pool, charger, &recordingMailer{}, "", testPlans(), now)

	if n := charger.chargedOrg(orgID); n != 0 {
		t.Fatalf("charges=%d one hour after the last attempt; the retry gap is %s and it is also what stops three replicas charging the same card on one tick",
			n, autopayRetryInterval)
	}
}

func TestSweepAutopay_SecondSweepInSameTick_ChargesOnce(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedAutopayAccount(t, pool, now.Add(12*time.Hour), 0, nil, "buyer@example.com")

	charger := &stubCharger{outcome: yookassa.ChargeSucceeded}
	SweepAutopay(context.Background(), pool, charger, &recordingMailer{}, "", testPlans(), now)
	SweepAutopay(context.Background(), pool, charger, &recordingMailer{}, "", testPlans(), now)

	if n := charger.chargedOrg(orgID); n != 1 {
		t.Fatalf("charges=%d want exactly 1; two replicas running the same tick must not both take 990 RUB", n)
	}
}

func TestSweepAutopay_Declined_CountsFailureAndKeepsAutopayArmed(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedAutopayAccount(t, pool, now.Add(12*time.Hour), 0, nil, "buyer@example.com")

	charger := &stubCharger{outcome: yookassa.ChargeFailed, reason: "Insufficient funds"}
	mailer := &recordingMailer{}
	SweepAutopay(context.Background(), pool, charger, mailer, "ops@example.com", testPlans(), now)

	enabled, failures, _ := autopayState(t, pool, orgID)
	if !enabled || failures != 1 {
		t.Fatalf("enabled=%t failures=%d want true/1; one decline is a topped-up card away from succeeding", enabled, failures)
	}
	if len(mailer.sends) != 1 || mailer.sends[0].to != "buyer@example.com" {
		t.Fatalf("sends=%v want exactly one mail to the customer (no operator page until the final decline)", mailer.sends)
	}
}

func TestSweepAutopay_FinalDecline_DisablesAutopayAndPagesOperator(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	old := now.Add(-12 * time.Hour)
	orgID := seedAutopayAccount(t, pool, now.Add(12*time.Hour), autopayMaxAttempts-1, &old, "buyer@example.com")

	charger := &stubCharger{outcome: yookassa.ChargeFailed, reason: "Payment method is expired"}
	mailer := &recordingMailer{}
	SweepAutopay(context.Background(), pool, charger, mailer, "ops@example.com", testPlans(), now)

	enabled, failures, _ := autopayState(t, pool, orgID)
	if enabled {
		t.Fatalf("autopay still enabled after %d declines; hammering a dead card is how a merchant account gets closed", autopayMaxAttempts)
	}
	if failures != autopayMaxAttempts {
		t.Fatalf("failures=%d want %d", failures, autopayMaxAttempts)
	}
	if len(mailer.sends) != 2 {
		t.Fatalf("sends=%d want 2 (customer + operator copy on the final decline)", len(mailer.sends))
	}
	if mailer.sends[1].to != "ops@example.com" {
		t.Fatalf("second send to=%q want the operator address", mailer.sends[1].to)
	}
}

func TestSweepAutopay_AutopayOff_IsNeverCharged(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedExpiryAccount(t, pool, "startup", now.Add(12*time.Hour), "buyer@example.com")

	charger := &stubCharger{outcome: yookassa.ChargeSucceeded}
	SweepAutopay(context.Background(), pool, charger, &recordingMailer{}, "", testPlans(), now)

	if n := charger.chargedOrg(orgID); n != 0 {
		t.Fatalf("charges=%d for an account that never consented to recurring payments", n)
	}
}

func TestSweepAutopay_ChargerError_LeavesFailureCountAlone(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedAutopayAccount(t, pool, now.Add(12*time.Hour), 0, nil, "buyer@example.com")

	charger := &stubCharger{err: errors.New("connection refused")}
	SweepAutopay(context.Background(), pool, charger, &recordingMailer{}, "", testPlans(), now)

	enabled, failures, _ := autopayState(t, pool, orgID)
	if !enabled || failures != 0 {
		t.Fatalf("enabled=%t failures=%d want true/0; our own network trouble is not the customer's card being declined and must not burn a retry",
			enabled, failures)
	}
}

func TestSweepAutopay_NilCharger_IsANoop(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedAutopayAccount(t, pool, now.Add(12*time.Hour), 0, nil, "buyer@example.com")

	SweepAutopay(context.Background(), pool, nil, &recordingMailer{}, "", testPlans(), now)

	_, _, lastAttempt := autopayState(t, pool, orgID)
	if lastAttempt != nil {
		t.Fatal("attempt claimed with payments unconfigured; a deployment without YooKassa credentials must not touch the money path at all")
	}
}

func TestAutopayNextCharge(t *testing.T) {
	expiry := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	if at := autopayNextCharge(true, "Bank card *4444", &expiry); at == nil || !at.Equal(expiry.Add(-autopayLeadTime)) {
		t.Fatalf("nextChargeAt=%v want %v", at, expiry.Add(-autopayLeadTime))
	}
	if at := autopayNextCharge(false, "Bank card *4444", &expiry); at != nil {
		t.Fatalf("nextChargeAt=%v want nil when autopay is off", at)
	}
	if at := autopayNextCharge(true, "", &expiry); at != nil {
		t.Fatalf("nextChargeAt=%v want nil when no method is saved", at)
	}
	if at := autopayNextCharge(true, "Bank card *4444", nil); at != nil {
		t.Fatalf("nextChargeAt=%v want nil for a plan with no term", at)
	}
}
