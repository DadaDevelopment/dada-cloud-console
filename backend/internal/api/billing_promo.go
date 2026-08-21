package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The action name this handler writes to audit_events. Kept distinct from
// the pre-existing "RedeemPromo" action (backend/internal/api/growth_reactivation.go,
// a per-recipient reactivation token) -- the two features share the word
// "promo" but nothing else: that one is a single-use token minted for one
// user_id, this one is a public code shared in a Telegram chat and capped by
// max_redemptions across many orgs. A merged audit action would make the two
// funnels unreadable from the row alone.
const auditActionRedeemBillingPromo = "RedeemBillingPromo"

// The closed set of machine-readable failure codes billingPromoRedeemRequest
// can answer with. The repo has a standing rule against classifying errors by
// message text (project_frontend_mapped_errors_by_regex_on_prose.md) -- every
// failure branch below sets exactly one of these in the JSON "code" field and
// nothing else distinguishes them.
const (
	promoErrCodeRequired    = "promo_code_required"
	promoErrCodeNotFound    = "promo_code_not_found"
	promoErrCodeExpired     = "promo_code_expired"
	promoErrCodeExhausted   = "promo_code_exhausted"
	promoErrAlreadyRedeemed = "promo_already_redeemed"
	promoErrOrgUnresolved   = "promo_org_unresolved"
)

type billingPromoRedeemRequest struct {
	Code string `json:"code"`
}

// billingPromoRedemption is the outcome of one claim attempt, filled in
// regardless of whether the plan grant landed -- see applyBillingPromo.
type billingPromoRedemption struct {
	Plan    string
	Days    int
	Applied bool
}

// RedeemBillingPromo grants a paid plan for a fixed number of days against a
// promo code, independent of the payment path. It never touches `payments`:
// a promo is not a payment and must not be countable as one by the money
// ledger or by the "first succeeded payment from a non-owner" gate metric --
// it only ever writes billing_accounts.plan / plan_expires_at.
//
// @ID          redeemBillingPromo
// @Summary     Redeem a promo code for a paid plan
// @Description Grants the org owning the caller's first project a fixed-term paid plan named by the promo code. Idempotent per (code, org): redeeming the same code twice for the same org returns promo_already_redeemed rather than granting a second term.
// @Tags        billing
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body     billingPromoRedeemRequest true "Promo code"
// @Success     200  {object} map[string]interface{}
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Failure     404  {object} map[string]string
// @Failure     409  {object} map[string]string
// @Failure     410  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Router      /billing/promo/redeem [post]
func (h *Handler) RedeemBillingPromo(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	var body billingPromoRedeemRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		respondErrorCode(c, http.StatusBadRequest, promoErrCodeRequired, "укажите промокод")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(body.Code))
	if code == "" {
		respondErrorCode(c, http.StatusBadRequest, promoErrCodeRequired, "укажите промокод")
		return
	}

	ctx := c.Request.Context()

	var orgID string
	err := h.pool.QueryRow(ctx, `
		SELECT org_id FROM projects
		WHERE owner_id = $1 AND org_id IS NOT NULL AND org_id <> ''
		ORDER BY created_at
		LIMIT 1
	`, claims.UserID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && orgID == "") {
		respondErrorCode(c, http.StatusConflict, promoErrOrgUnresolved, "нет организации, на которую можно начислить план")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve organization")
		return
	}

	result, failCode, err := h.claimBillingPromo(ctx, code, orgID, claims.UserID, time.Now().UTC())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to redeem promo code")
		return
	}
	if failCode != "" {
		status := http.StatusConflict
		switch failCode {
		case promoErrCodeNotFound:
			status = http.StatusNotFound
		case promoErrCodeExpired:
			status = http.StatusGone
		}
		h.recordAudit(ctx, claims.UserID, auditEntry{
			Action:       auditActionRedeemBillingPromo,
			ResourceKind: "BillingAccount",
			ResourceName: code,
			Outcome:      auditOutcomeFailure,
			Metadata:     map[string]any{"code": code, "org_id": orgID, "reason": failCode},
		})
		respondErrorCode(c, status, failCode, billingPromoErrorMessage(failCode))
		return
	}

	h.recordAudit(ctx, claims.UserID, auditEntry{
		Action:       auditActionRedeemBillingPromo,
		ResourceKind: "BillingAccount",
		ResourceName: code,
		Outcome:      auditOutcomeSuccess,
		Metadata: map[string]any{
			"code": code, "org_id": orgID,
			"plan": result.Plan, "days": result.Days, "applied": result.Applied,
		},
	})
	c.JSON(http.StatusOK, gin.H{
		"plan":    result.Plan,
		"days":    result.Days,
		"applied": result.Applied,
	})
}

func billingPromoErrorMessage(code string) string {
	switch code {
	case promoErrCodeNotFound:
		return "промокод не найден"
	case promoErrCodeExpired:
		return "срок действия промокода истёк"
	case promoErrCodeExhausted:
		return "промокод исчерпан"
	case promoErrAlreadyRedeemed:
		return "этот промокод уже активирован для вашей организации"
	default:
		return "не удалось активировать промокод"
	}
}

// claimBillingPromo runs the whole claim atomically in one transaction:
//
//  1. SELECT ... FOR UPDATE locks the promo_codes row, so two concurrent
//     redeemers of the same code serialize on this row rather than both
//     reading a stale redeemed_count -- the "explicit lock" half of the
//     oversell guard.
//  2. The redemption row is inserted (ON CONFLICT (code, org_id) DO NOTHING)
//     BEFORE the counter moves, so an org that already redeemed this code
//     never consumes a second slot of max_redemptions.
//  3. redeemed_count is only incremented after both the lock and the
//     dedup insert succeeded, guarded by the same max_redemptions check the
//     lock makes safe to rely on.
//
// A non-empty failCode with a nil error means the claim was correctly
// refused (not an infrastructure failure) and nothing was written except the
// audit row the caller adds afterward.
func (h *Handler) claimBillingPromo(ctx context.Context, code, orgID string, userID uuid.UUID, now time.Time) (billingPromoRedemption, string, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return billingPromoRedemption{}, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var plan string
	var days, maxRedemptions, redeemedCount int
	var validUntil *time.Time
	err = tx.QueryRow(ctx, `
		SELECT plan, days, max_redemptions, redeemed_count, valid_until
		FROM promo_codes
		WHERE code = $1
		FOR UPDATE
	`, code).Scan(&plan, &days, &maxRedemptions, &redeemedCount, &validUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return billingPromoRedemption{}, promoErrCodeNotFound, nil
	}
	if err != nil {
		return billingPromoRedemption{}, "", err
	}
	if validUntil != nil && validUntil.Before(now) {
		return billingPromoRedemption{}, promoErrCodeExpired, nil
	}
	if redeemedCount >= maxRedemptions {
		return billingPromoRedemption{}, promoErrCodeExhausted, nil
	}

	var redemptionID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO promo_redemptions (code, org_id, user_id, redeemed_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (code, org_id) DO NOTHING
		RETURNING id
	`, code, orgID, userID, now).Scan(&redemptionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return billingPromoRedemption{}, promoErrAlreadyRedeemed, nil
	}
	if err != nil {
		return billingPromoRedemption{}, "", err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE promo_codes SET redeemed_count = redeemed_count + 1, updated_at = $2 WHERE code = $1
	`, code, now); err != nil {
		return billingPromoRedemption{}, "", err
	}

	applied, err := applyBillingPromoGrant(ctx, tx, orgID, plan, days, now)
	if err != nil {
		return billingPromoRedemption{}, "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return billingPromoRedemption{}, "", err
	}
	return billingPromoRedemption{Plan: plan, Days: days, Applied: applied}, "", nil
}

// applyBillingPromoGrant sets or extends the org's paid plan term. It only
// moves the plan itself when the org is currently on 'free' or already on
// this same promo plan -- an org already paying for a different plan keeps
// its own term untouched (Applied=false) so a promo code can never be used
// to downgrade or silently swap a plan a customer is actively paying for.
// Extension always takes the later of "now" and the current expiry as its
// base, so redeeming a second code before the first term runs out adds days
// on top instead of overwriting them.
func applyBillingPromoGrant(ctx context.Context, tx pgx.Tx, orgID, plan string, days int, now time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, expiry_notified_at, updated_at)
		VALUES ($1, $2, $4::timestamptz, $4::timestamptz + make_interval(days => $3), NULL, $4::timestamptz)
		ON CONFLICT (org_id) DO UPDATE
		  SET plan               = $2,
		      plan_assigned_at   = CASE WHEN billing_accounts.plan = 'free' THEN $4::timestamptz ELSE billing_accounts.plan_assigned_at END,
		      plan_expires_at    = GREATEST(COALESCE(billing_accounts.plan_expires_at, $4::timestamptz), $4::timestamptz) + make_interval(days => $3),
		      expiry_notified_at = NULL,
		      updated_at         = $4::timestamptz
		  WHERE billing_accounts.plan = 'free' OR billing_accounts.plan = $2
	`, orgID, plan, days, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
