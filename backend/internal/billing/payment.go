package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PaymentProvider is the boundary between the billing system and the payment
// gateway. MVP implementation is ManualProvider (admin-driven plan assignment
// only). YooKassa (cards + SBP + 54-ФЗ receipts) will implement this
// interface in a future slice.
type PaymentProvider interface {
	// AssignPlan updates the org's billing plan. The implementation is
	// responsible for any payment collection, receipt generation, or external
	// record-keeping. The ManualProvider simply persists the new plan row.
	AssignPlan(ctx context.Context, orgID, plan string) error
}

// ManualProvider is the no-op payment implementation: plan assignment is an
// admin write to billing_accounts with no money movement. YooKassa replaces
// this when real payment collection is wired.
type ManualProvider struct {
	Pool *pgxpool.Pool
}

// AssignPlan upserts billing_accounts with the chosen plan key.
func (m *ManualProvider) AssignPlan(ctx context.Context, orgID, plan string) error {
	_, err := m.Pool.Exec(ctx, `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, updated_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (org_id) DO UPDATE
		  SET plan             = EXCLUDED.plan,
		      plan_assigned_at = EXCLUDED.plan_assigned_at,
		      updated_at       = EXCLUDED.updated_at
	`, orgID, plan, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("billing: assign plan: %w", err)
	}
	return nil
}
