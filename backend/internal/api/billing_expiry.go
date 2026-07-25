package api

import (
	"context"
	"log"
	"time"

	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/jackc/pgx/v5/pgxpool"
)

// planExpiryGrace is how long past plan_expires_at a paid plan keeps working
// before the sweeper lapses the account to free. Soft landing: the term ends,
// the user gets a few days to renew, nothing running is ever stopped.
const planExpiryGrace = 3 * 24 * time.Hour

// planExpiryReminderWeek / planExpiryReminderFinal are the two reminder
// stages before expiry. Each stage fires at most once per term:
// expiry_notified_at records the last send, and a stage only fires while
// expiry_notified_at predates that stage's threshold. A renewal resets
// expiry_notified_at (see yookassa.assignPlanTx), re-arming both stages.
const (
	planExpiryReminderWeek  = 7 * 24 * time.Hour
	planExpiryReminderFinal = 3 * 24 * time.Hour
)

// expiryMailer is the slice of notify.Notifier the sweeper needs; tests
// substitute a recorder.
type expiryMailer interface {
	Send(to, subject, body string) error
}

// expiryAccount is one billing_accounts row the sweeper considers: a paid
// plan with a term (plan_expires_at set). Email is the customer address from
// the org's most recent succeeded payment — the only address the billing
// schema knows; admin-assigned perpetual plans (NULL expiry) never get here.
type expiryAccount struct {
	OrgID      string
	Plan       string
	ExpiresAt  time.Time
	NotifiedAt *time.Time
	Email      *string
}

// SweepPlanExpiry runs one pass of the plan-expiry lifecycle over every paid
// account with a term: sends the 7-day and 3-day reminders, and past
// expiry+grace lapses the account to free (plan_expires_at cleared, apps
// untouched — free quotas gate only new resources). Mail failures are logged
// and swallowed; the downgrade itself never depends on SMTP health.
func SweepPlanExpiry(ctx context.Context, pool *pgxpool.Pool, mailer expiryMailer, auditTo string, now time.Time) {
	rows, err := pool.Query(ctx, `
		SELECT ba.org_id, ba.plan, ba.plan_expires_at, ba.expiry_notified_at,
		       (SELECT p.customer_email FROM payments p
		        WHERE p.org_id = ba.org_id AND p.status = 'succeeded' AND p.customer_email IS NOT NULL
		        ORDER BY p.paid_at DESC NULLS LAST LIMIT 1)
		FROM billing_accounts ba
		WHERE ba.plan <> 'free' AND ba.plan_expires_at IS NOT NULL
	`)
	if err != nil {
		log.Printf("billing expiry: list accounts: %v", err)
		return
	}
	accounts := make([]expiryAccount, 0)
	for rows.Next() {
		var a expiryAccount
		if err := rows.Scan(&a.OrgID, &a.Plan, &a.ExpiresAt, &a.NotifiedAt, &a.Email); err != nil {
			rows.Close()
			log.Printf("billing expiry: scan account: %v", err)
			return
		}
		accounts = append(accounts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("billing expiry: read accounts: %v", err)
		return
	}

	for _, a := range accounts {
		if now.After(a.ExpiresAt.Add(planExpiryGrace)) {
			downgradePlan(ctx, pool, mailer, auditTo, a, now)
			continue
		}
		remindPlanExpiry(ctx, pool, mailer, a, now)
	}
}

// downgradePlan lapses one expired account to free. The WHERE clause repeats
// the expiry predicate so a concurrent renewal (webhook committing between
// the sweep's read and this write) wins and the downgrade becomes a no-op.
func downgradePlan(ctx context.Context, pool *pgxpool.Pool, mailer expiryMailer, auditTo string, a expiryAccount, now time.Time) {
	tag, err := pool.Exec(ctx, `
		UPDATE billing_accounts
		SET plan = 'free', plan_assigned_at = $2, plan_expires_at = NULL, expiry_notified_at = NULL, updated_at = $2
		WHERE org_id = $1 AND plan = $3 AND plan_expires_at IS NOT NULL AND plan_expires_at + interval '3 days' < $2
	`, a.OrgID, now, a.Plan)
	if err != nil {
		log.Printf("billing expiry: downgrade org=%s: %v", a.OrgID, err)
		return
	}
	if tag.RowsAffected() == 0 {
		return
	}
	log.Printf("billing expiry: org=%s plan=%s lapsed to free (expired %s)", a.OrgID, a.Plan, a.ExpiresAt.Format(time.RFC3339))
	if mailer == nil {
		return
	}
	if a.Email != nil && *a.Email != "" {
		subject, body := notify.ComposePlanDowngraded(a.Plan)
		if err := mailer.Send(*a.Email, subject, body); err != nil {
			log.Printf("billing expiry: downgrade mail to %s failed: %v", *a.Email, err)
		}
	}
	if auditTo != "" {
		email := ""
		if a.Email != nil {
			email = *a.Email
		}
		subject, body := notify.ComposeAudit("PlanDowngraded", email, a.Plan, a.OrgID, now.UTC().Format(time.RFC3339))
		if err := mailer.Send(auditTo, subject, body); err != nil {
			log.Printf("billing expiry: operator copy to %s failed: %v", auditTo, err)
		}
	}
}

// remindPlanExpiry fires at most one reminder per pass: the final (3-day)
// stage when due, otherwise the week stage. expiry_notified_at advances only
// after a successful send, so an SMTP outage retries on the next tick.
func remindPlanExpiry(ctx context.Context, pool *pgxpool.Pool, mailer expiryMailer, a expiryAccount, now time.Time) {
	if mailer == nil || a.Email == nil || *a.Email == "" {
		return
	}
	var threshold time.Time
	var daysLeft int
	switch {
	case !now.Before(a.ExpiresAt.Add(-planExpiryReminderFinal)):
		threshold = a.ExpiresAt.Add(-planExpiryReminderFinal)
		daysLeft = int(planExpiryReminderFinal / (24 * time.Hour))
	case !now.Before(a.ExpiresAt.Add(-planExpiryReminderWeek)):
		threshold = a.ExpiresAt.Add(-planExpiryReminderWeek)
		daysLeft = int(planExpiryReminderWeek / (24 * time.Hour))
	default:
		return
	}
	if a.NotifiedAt != nil && !a.NotifiedAt.Before(threshold) {
		return
	}
	subject, body := notify.ComposePlanExpiryReminder(a.Plan, a.ExpiresAt.UTC().Format("2006-01-02 15:04"), daysLeft)
	if err := mailer.Send(*a.Email, subject, body); err != nil {
		log.Printf("billing expiry: reminder mail to %s failed: %v", *a.Email, err)
		return
	}
	if _, err := pool.Exec(ctx, `
		UPDATE billing_accounts SET expiry_notified_at = $2, updated_at = $2 WHERE org_id = $1
	`, a.OrgID, now); err != nil {
		log.Printf("billing expiry: mark notified org=%s: %v", a.OrgID, err)
	}
}
