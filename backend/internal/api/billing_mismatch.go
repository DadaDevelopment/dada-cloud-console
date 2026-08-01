package api

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const paymentMismatchDedupWindow = 24 * time.Hour

var paymentMismatchSeen = newAuditSeen(paymentMismatchDedupWindow)

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
// leave the org holding the paid plan. Registered and ticked alongside
// SweepPlanExpiry / SweepQuotaGrace, see cmd/server/main.go.
//
// ProcessWebhook (yookassa/provider.go) flips payments.status and assigns
// the plan inside one transaction, so by code this divergence cannot happen
// -- yet on 2026-07-25 org "dada" paid 990 RUB and stayed on plan=free, and
// it took a manual join of payments against billing_accounts, a week later,
// to find it. This sweeper is that join, run continuously instead of by
// hand.
//
// A payment older than its 30-day term plus the expiry sweeper's grace
// window is excluded: a plan lapsed to free on schedule is the system
// working as designed, not a discrepancy.
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
		reportPaymentMismatch(ctx, pool, auditTo, m, now)
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

func reportPaymentMismatch(ctx context.Context, pool *pgxpool.Pool, auditTo string, m mismatchRow, now time.Time) {
	if !paymentMismatchSeen.allow(m.PaymentID.String(), now) {
		return
	}
	log.Printf("payment mismatch [ERROR]: payment=%s org=%s plan=%s paid_at=%s account_plan=%s account_has_row=%t: paid but org does not hold the plan",
		m.PaymentID, m.OrgID, m.Plan, m.PaidAt.Format(time.RFC3339), m.AccountPlan, m.HasAccount)

	meta := map[string]string{
		"payment_id":   m.PaymentID.String(),
		"paid_plan":    m.Plan,
		"account_plan": m.AccountPlan,
		"paid_at":      m.PaidAt.UTC().Format(time.RFC3339),
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
