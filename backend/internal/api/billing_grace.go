package api

import (
	"context"
	"log"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// graceReminderStages are the notice periods before the grandfathering window
// closes, longest first. Each stage fires at most once per account:
// grace_notified_at records the last send, and a stage only fires while that
// timestamp predates the stage's own threshold.
//
// Thirty days is the first stage on purpose. The people this reaches were
// promised a free tier and are, in some cases, already above it; the first
// time they hear that limits are coming must not be the week they arrive.
var graceReminderStages = []time.Duration{
	30 * 24 * time.Hour,
	7 * 24 * time.Hour,
	24 * time.Hour,
}

// graceAccount is one grandfathered org the sweeper considers.
type graceAccount struct {
	OrgID      string
	GraceUntil time.Time
	NotifiedAt *time.Time
}

// SweepQuotaGrace runs one pass over every org still inside its
// grandfathering window and sends the 30/7/1-day notices.
//
// The window itself is silent by construction: quotaGraceActive simply stops
// blocking, so an org drifts toward a date nobody told them about and then
// meets a 403 that reads like a bug. This is the telling.
//
// It never changes an account. The end of grace is not a downgrade -- nothing
// running stops, the free limits merely start applying to new resources --
// so there is no state to flip, only a message to send.
func SweepQuotaGrace(ctx context.Context, pool *pgxpool.Pool, mailer expiryMailer, auditTo string, plans []pricing.Plan, now time.Time) {
	freePlan, ok := freePlanOf(plans)
	if !ok {
		log.Printf("quota grace: no free plan loaded, skipping sweep")
		return
	}
	rows, err := pool.Query(ctx, `
		SELECT org_id, quota_grace_until, grace_notified_at
		FROM billing_accounts
		WHERE plan = 'free' AND quota_grace_until IS NOT NULL AND quota_grace_until > $1
	`, now)
	if err != nil {
		log.Printf("quota grace: list accounts: %v", err)
		return
	}
	accounts := make([]graceAccount, 0)
	for rows.Next() {
		var a graceAccount
		if err := rows.Scan(&a.OrgID, &a.GraceUntil, &a.NotifiedAt); err != nil {
			rows.Close()
			log.Printf("quota grace: scan account: %v", err)
			return
		}
		accounts = append(accounts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("quota grace: read accounts: %v", err)
		return
	}

	for _, a := range accounts {
		stage, ok := dueGraceStage(a, now)
		if !ok {
			continue
		}
		sendGraceReminder(ctx, pool, mailer, auditTo, freePlan, a, stage, now)
	}
}

// dueGraceStage returns the tightest reminder stage that is due for an
// account, if any. A stage is due once the deadline is within it and the
// account has not been mailed since that stage opened; taking the tightest
// one means an account that appears late (a backfill two days before the
// deadline) gets exactly one notice rather than three at once, and that the
// one it gets is the urgent one.
//
// The stage is only the gate. What the mail says is the real remaining time
// -- see graceDaysLeft -- so a late arrival is never told it has a week when
// it has two days.
func dueGraceStage(a graceAccount, now time.Time) (time.Duration, bool) {
	remaining := a.GraceUntil.Sub(now)
	var due time.Duration
	found := false
	for _, stage := range graceReminderStages {
		if remaining > stage {
			continue
		}
		if a.NotifiedAt != nil && !a.NotifiedAt.Before(a.GraceUntil.Add(-stage)) {
			continue
		}
		due = stage
		found = true
	}
	return due, found
}

// sendGraceReminder claims the stage, then mails one org its own numbers.
//
// The claim is the UPDATE itself: it repeats the freshness predicate in its
// WHERE, so of two console replicas ticking at the same second exactly one
// gets a row back and the other sends nothing. The same conditional write
// means the timestamp lands before the mail attempt, on purpose -- a grace
// notice is worth one attempt, not a retry every hour against a broken
// address for a month, and the console banner carries the same information
// for anyone whose mail never arrives.
func sendGraceReminder(ctx context.Context, pool *pgxpool.Pool, mailer expiryMailer, auditTo string, freePlan pricing.Plan, a graceAccount, stage time.Duration, now time.Time) {
	tag, err := pool.Exec(ctx, `
		UPDATE billing_accounts
		SET grace_notified_at = $2, updated_at = $2
		WHERE org_id = $1
		  AND (grace_notified_at IS NULL OR grace_notified_at < $3)
	`, a.OrgID, now, a.GraceUntil.Add(-stage))
	if err != nil {
		log.Printf("quota grace: mark notified org=%s: %v", a.OrgID, err)
		return
	}
	if tag.RowsAffected() == 0 {
		return
	}

	usage := graceUsageLines(ctx, pool, freePlan, a.OrgID)
	daysLeft := graceDaysLeft(a.GraceUntil, now)
	subject, body := notify.ComposeQuotaGraceReminder(a.GraceUntil.Format("2006-01-02"), daysLeft, usage)

	if mailer == nil {
		return
	}
	if to := graceRecipient(ctx, pool, a.OrgID); to != "" {
		if err := mailer.Send(to, subject, body); err != nil {
			log.Printf("quota grace: send to %s failed: %v", to, err)
		} else {
			log.Printf("quota grace: notified org=%s days_left=%d", a.OrgID, daysLeft)
		}
	} else {
		log.Printf("quota grace: no recipient for org=%s, days_left=%d", a.OrgID, daysLeft)
	}
	if auditTo != "" {
		if err := mailer.Send(auditTo, "[ops] "+subject+" ("+a.OrgID+")", body); err != nil {
			log.Printf("quota grace: operator copy failed: %v", err)
		}
	}
}

// graceUsageLines renders the org's countable resources against the free
// limits, keeping only the ones that already exceed them.
//
// The whole point of the notice is "here is what changes for YOU": a generic
// "limits are coming" mail is noise for the org with one app and useless to
// the org with three. An org that is entirely inside the free tier gets an
// empty list and a correspondingly reassuring message.
func graceUsageLines(ctx context.Context, pool *pgxpool.Pool, freePlan pricing.Plan, orgID string) []notify.QuotaLine {
	counts := map[string]string{
		"apps":      "приложения",
		"databases": "базы данных",
		"domains":   "домены",
	}
	order := []string{"apps", "databases", "domains"}

	lines := make([]notify.QuotaLine, 0, len(order))
	for _, res := range order {
		limit, ok := pricing.Quota(freePlan, res)
		if !ok || limit == 0 {
			continue
		}
		used, err := countOrgResource(ctx, pool, orgID, res)
		if err != nil {
			log.Printf("quota grace: count %s for org=%s: %v", res, orgID, err)
			continue
		}
		if used > limit {
			lines = append(lines, notify.QuotaLine{Label: counts[res], Used: used, Limit: limit})
		}
	}
	return lines
}

// countOrgResource is countResource's pool-only twin, for the background
// sweepers that have no Handler. Only the resources the grace notice names
// are supported; anything else returns 0 rather than an error, because a
// notice must not fail over an unknown resource name.
func countOrgResource(ctx context.Context, pool *pgxpool.Pool, orgID, resource string) (int, error) {
	var n int
	switch resource {
	case "apps":
		err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM resource_snapshots rs
			JOIN projects p ON p.id = rs.project_id
			WHERE p.org_id = $1 AND rs.kind = 'App' AND `+notOrphanedSnapshot+`
		`, orgID).Scan(&n)
		return n, err
	case "databases":
		err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM resource_snapshots rs
			JOIN projects p ON p.id = rs.project_id
			WHERE p.org_id = $1 AND rs.kind IN ('ServiceDatabase', 'ServiceDatabaseV2')
		`, orgID).Scan(&n)
		return n, err
	case "domains":
		err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM domain_authorizations da
			JOIN projects p ON p.id = da.project_id
			WHERE p.org_id = $1
		`, orgID).Scan(&n)
		return n, err
	}
	return 0, nil
}

// graceDaysLeft is the whole days remaining, rounded up, floored at 1. The
// sweeper ticks hourly, so "0 дн." would be a routine outcome on the last day
// and reads as a mistake; the last notice says one day.
func graceDaysLeft(graceUntil, now time.Time) int {
	remaining := graceUntil.Sub(now)
	days := int((remaining + 24*time.Hour - time.Nanosecond) / (24 * time.Hour))
	if days < 1 {
		return 1
	}
	return days
}

// freePlanOf picks the free plan out of the loaded catalog. Unlike planFor it
// does not fall back to "whatever plan is first": a notice built from the
// wrong limits would tell people numbers that are not theirs, so a catalog
// without a free plan skips the sweep instead.
func freePlanOf(plans []pricing.Plan) (pricing.Plan, bool) {
	for _, p := range plans {
		if p.Key == "free" {
			return p, true
		}
	}
	return pricing.Plan{}, false
}

// graceRecipient resolves who to write to for an org by running the org's
// oldest project through the existing anti-drop recipient ladder, so a grace
// notice reaches exactly the addresses an outage alert would.
func graceRecipient(ctx context.Context, pool *pgxpool.Pool, orgID string) string {
	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM projects WHERE org_id = $1 ORDER BY created_at LIMIT 1`, orgID,
	).Scan(&projectID); err != nil {
		log.Printf("quota grace: no project for org=%s: %v", orgID, err)
		return ""
	}
	email, _ := alertRecipientForProject(ctx, pool, projectID)
	return email
}
