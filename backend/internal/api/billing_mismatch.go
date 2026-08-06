package api

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const paymentMismatchDedupWindow = 24 * time.Hour

type mismatchRow struct {
	PaymentID     uuid.UUID
	OrgID         string
	Plan          string
	PaidAt        time.Time
	HasAccount    bool
	AccountPlan   string
	AccountExpiry *time.Time
}

// SweepPaymentPlanMismatch finds orgs whose latest succeeded payment did not
// leave the org holding the paid plan, and gives them the plan they paid for.
// Registered and ticked alongside SweepPlanExpiry / SweepQuotaGrace, see
// cmd/server/main.go.
//
// ProcessWebhook (yookassa/provider.go) flips payments.status and assigns
// the plan inside one transaction, so by code this divergence cannot happen
// -- yet on 2026-07-25 org "dada" paid 990 RUB and stayed on plan=free, and
// it took a manual join of payments against billing_accounts, a week later,
// to find it. Reporting alone kept that org on free for twelve more days:
// nobody acts on a log line, so the sweeper now repairs what it finds and
// reports only what it could not repair.
//
// The repair direction is deliberately one-way. It grants a plan that a
// succeeded payment already paid for; it never revokes, downgrades or
// shortens one -- SweepPlanExpiry owns the lapse path.
//
// A payment older than its 30-day term plus the expiry sweeper's grace
// window is excluded: a plan lapsed to free on schedule is the system
// working as designed, not a discrepancy.
//
// Unlike its three neighbours it also runs once at boot rather than waiting
// for the first tick: an org that paid and did not get its plan should not
// wait an hour, and the backend redeploys often enough that a pod is not
// guaranteed to live that long. Running it early is safe because the repair
// is idempotent and only grants what a succeeded payment already bought --
// the sweepers that move money or revoke plans stay on the tick.
func SweepPaymentPlanMismatch(ctx context.Context, pool *pgxpool.Pool, auditTo string, now time.Time) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (p.org_id)
		       p.id, p.org_id, p.plan, p.paid_at,
		       (ba.org_id IS NOT NULL), COALESCE(ba.plan, 'free'), ba.plan_expires_at
		FROM payments p
		LEFT JOIN billing_accounts ba ON ba.org_id = p.org_id
		WHERE p.status = 'succeeded' AND p.paid_at IS NOT NULL
		ORDER BY p.org_id, p.paid_at DESC
	`)
	if err != nil {
		log.Printf("payment mismatch: list latest payments: %v", err)
		return
	}
	candidates := make([]mismatchRow, 0)
	for rows.Next() {
		var m mismatchRow
		if err := rows.Scan(&m.PaymentID, &m.OrgID, &m.Plan, &m.PaidAt,
			&m.HasAccount, &m.AccountPlan, &m.AccountExpiry); err != nil {
			rows.Close()
			log.Printf("payment mismatch: scan row: %v", err)
			return
		}
		candidates = append(candidates, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("payment mismatch: read rows: %v", err)
		return
	}

	for _, m := range candidates {
		if !paymentPlanMismatched(m, now) {
			continue
		}
		if err := reconcilePaymentPlan(ctx, pool, m, now); err != nil {
			reportPaymentMismatch(ctx, pool, auditTo, m, now, err)
			continue
		}
		log.Printf("payment mismatch repaired: payment=%s org=%s plan=%s paid_at=%s previous_plan=%s",
			m.PaymentID, m.OrgID, m.Plan, m.PaidAt.Format(time.RFC3339), m.AccountPlan)
		writeAuditRow(ctx, pool, systemDeployActorID, auditEntry{
			Action:       "PaymentPlanReconciled",
			ResourceKind: "Payment",
			ResourceName: m.OrgID,
			Outcome:      auditOutcomeSuccess,
			Metadata: map[string]string{
				"payment_id":      m.PaymentID.String(),
				"paid_plan":       m.Plan,
				"previous_plan":   m.AccountPlan,
				"paid_at":         m.PaidAt.UTC().Format(time.RFC3339),
				"plan_expires_at": m.PaidAt.Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339),
			},
		})
	}
}

func paymentPlanMismatched(m mismatchRow, now time.Time) bool {
	if now.Sub(m.PaidAt) > 30*24*time.Hour+planExpiryGrace {
		return false
	}
	if !m.HasAccount {
		return true
	}
	if m.AccountPlan == "free" {
		return true
	}
	if m.AccountExpiry != nil && m.AccountExpiry.Before(m.PaidAt) {
		return true
	}
	return false
}

// reconcilePaymentPlan grants the paid plan for the term the payment bought:
// the clock starts at paid_at, not at repair time, so a discrepancy found on
// day twelve does not silently extend the org's month.
//
// The statement is idempotent and safe to race: the DO UPDATE guard only
// overwrites a free plan or one whose term already ended before this payment,
// so two replicas sweeping at once cannot stack terms and neither can undo a
// better plan assigned in between. Zero rows affected means another writer got
// there first -- that is the desired end state, not an error.
func reconcilePaymentPlan(ctx context.Context, pool *pgxpool.Pool, m mismatchRow, now time.Time) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, expiry_notified_at, updated_at)
		VALUES ($1, $2, $3::timestamptz, $3::timestamptz + interval '30 days', NULL, $4::timestamptz)
		ON CONFLICT (org_id) DO UPDATE
		  SET plan               = EXCLUDED.plan,
		      plan_assigned_at   = EXCLUDED.plan_assigned_at,
		      plan_expires_at    = EXCLUDED.plan_expires_at,
		      expiry_notified_at = NULL,
		      updated_at         = EXCLUDED.updated_at
		WHERE billing_accounts.plan = 'free'
		   OR billing_accounts.plan_expires_at IS NULL
		   OR billing_accounts.plan_expires_at < $3::timestamptz
	`, m.OrgID, m.Plan, m.PaidAt, now)
	return err
}

// reportPaymentMismatch records a discrepancy the sweeper could not repair.
//
// The dedup window lives in audit_events rather than in process memory: the
// backend runs two replicas and redeploys several times a day, so a per-pod
// map re-announced the same unresolved payment on every restart and from every
// replica -- one stuck payment produced thirty audit rows in a week and read
// as noise, which is exactly why it went unnoticed for twelve days.
func reportPaymentMismatch(ctx context.Context, pool *pgxpool.Pool, auditTo string, m mismatchRow, now time.Time, cause error) {
	log.Printf("payment mismatch [ERROR]: payment=%s org=%s plan=%s paid_at=%s account_plan=%s account_has_row=%t: paid but org does not hold the plan, repair failed: %v",
		m.PaymentID, m.OrgID, m.Plan, m.PaidAt.Format(time.RFC3339), m.AccountPlan, m.HasAccount, cause)

	var reported bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM audit_events
			WHERE action = 'PaymentPlanMismatchDetected'
			  AND metadata->>'payment_id' = $1
			  AND created_at > $2::timestamptz
		)
	`, m.PaymentID.String(), now.Add(-paymentMismatchDedupWindow)).Scan(&reported); err != nil {
		log.Printf("payment mismatch: dedup lookup for payment=%s: %v", m.PaymentID, err)
	} else if reported {
		return
	}

	meta := map[string]string{
		"payment_id":   m.PaymentID.String(),
		"paid_plan":    m.Plan,
		"account_plan": m.AccountPlan,
		"paid_at":      m.PaidAt.UTC().Format(time.RFC3339),
		"repair_error": cause.Error(),
	}
	if m.AccountExpiry != nil {
		meta["account_plan_expires_at"] = m.AccountExpiry.UTC().Format(time.RFC3339)
	}
	writeAuditRow(ctx, pool, systemDeployActorID, auditEntry{
		Action:       "PaymentPlanMismatchDetected",
		ResourceKind: "Payment",
		ResourceName: m.OrgID,
		Outcome:      auditOutcomeFailure,
		Metadata:     meta,
	})
}
