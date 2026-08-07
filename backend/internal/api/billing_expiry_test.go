package api

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func expiryTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping plan-expiry DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type recordingMailer struct {
	sends []struct{ to, subject string }
	html  []string
}

func (m *recordingMailer) Send(to, subject, body string) error {
	m.sends = append(m.sends, struct{ to, subject string }{to, subject})
	return nil
}

func (m *recordingMailer) SendHTML(to, subject, textBody, htmlBody string) error {
	m.html = append(m.html, htmlBody)
	return m.Send(to, subject, textBody)
}

func seedExpiryAccount(t *testing.T, pool *pgxpool.Pool, plan string, expiresAt time.Time, email string) string {
	t.Helper()
	orgID := "org-sweep-" + uuid.NewString()[:8]
	_, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, updated_at)
		VALUES ($1, $2, now(), $3, now())
	`, orgID, plan, expiresAt)
	if err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	if email != "" {
		_, err = pool.Exec(context.Background(), `
			INSERT INTO payments (id, org_id, plan, amount_value, currency, status, customer_email, created_by_sub, paid_at)
			VALUES ($1, $2, $3, '990.00', 'RUB', 'succeeded', $4, 'sub-test', now())
		`, uuid.New(), orgID, plan, email)
		if err != nil {
			t.Fatalf("seed succeeded payment: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})
	return orgID
}

func sweepAccountState(t *testing.T, pool *pgxpool.Pool, orgID string) (plan string, expires, notified *time.Time) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT plan, plan_expires_at, expiry_notified_at FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&plan, &expires, &notified)
	if err != nil {
		t.Fatalf("read billing account: %v", err)
	}
	return plan, expires, notified
}

func TestSweepPlanExpiry_PastGrace_LapsesToFree(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedExpiryAccount(t, pool, "startup", now.Add(-4*24*time.Hour), "buyer@example.com")

	mailer := &recordingMailer{}
	SweepPlanExpiry(context.Background(), pool, mailer, "ops@example.com", now)

	plan, expires, notified := sweepAccountState(t, pool, orgID)
	if plan != "free" {
		t.Fatalf("plan=%q want free after expiry+grace", plan)
	}
	if expires != nil || notified != nil {
		t.Fatalf("expires=%v notified=%v want both NULL after lapse", expires, notified)
	}
	if len(mailer.sends) != 2 {
		t.Fatalf("sends=%d want 2 (customer + operator copy)", len(mailer.sends))
	}
	if mailer.sends[0].to != "buyer@example.com" {
		t.Fatalf("first send to=%q want customer", mailer.sends[0].to)
	}
}

func TestSweepPlanExpiry_InsideGrace_KeepsPlan(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedExpiryAccount(t, pool, "startup", now.Add(-24*time.Hour), "buyer@example.com")

	mailer := &recordingMailer{}
	SweepPlanExpiry(context.Background(), pool, mailer, "", now)

	plan, _, _ := sweepAccountState(t, pool, orgID)
	if plan != "startup" {
		t.Fatalf("plan=%q want startup kept inside the 3-day grace", plan)
	}
}

func TestSweepPlanExpiry_ReminderFiresOncePerStage(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := seedExpiryAccount(t, pool, "startup", now.Add(5*24*time.Hour), "buyer@example.com")

	mailer := &recordingMailer{}
	SweepPlanExpiry(context.Background(), pool, mailer, "", now)
	if len(mailer.sends) != 1 {
		t.Fatalf("sends=%d want 1 week-stage reminder", len(mailer.sends))
	}

	SweepPlanExpiry(context.Background(), pool, mailer, "", now.Add(time.Hour))
	if len(mailer.sends) != 1 {
		t.Fatalf("sends=%d want still 1 -- same stage must not re-fire", len(mailer.sends))
	}

	SweepPlanExpiry(context.Background(), pool, mailer, "", now.Add(3*24*time.Hour))
	if len(mailer.sends) != 2 {
		t.Fatalf("sends=%d want 2 after the final (3-day) stage becomes due", len(mailer.sends))
	}

	plan, _, notified := sweepAccountState(t, pool, orgID)
	if plan != "startup" {
		t.Fatalf("plan=%q want startup untouched by reminders", plan)
	}
	if notified == nil {
		t.Fatal("expiry_notified_at must record the last reminder send")
	}
}

func TestSweepPlanExpiry_PerpetualAndFreeAccounts_Untouched(t *testing.T) {
	pool := expiryTestPool(t)
	now := time.Now().UTC()
	orgID := "org-perp-" + uuid.NewString()[:8]
	_, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, updated_at)
		VALUES ($1, 'business', now(), now())
	`, orgID)
	if err != nil {
		t.Fatalf("seed perpetual account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})

	mailer := &recordingMailer{}
	SweepPlanExpiry(context.Background(), pool, mailer, "", now)

	plan, _, _ := sweepAccountState(t, pool, orgID)
	if plan != "business" {
		t.Fatalf("plan=%q want business -- NULL plan_expires_at means perpetual, sweeper must skip it", plan)
	}
	if len(mailer.sends) != 0 {
		t.Fatalf("sends=%d want 0 for a perpetual account", len(mailer.sends))
	}
}
