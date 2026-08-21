package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// autopayLeadTime is how long before the term ends the first charge is
// attempted. A day is enough for a decline to be noticed, retried, and fixed
// by hand before anything expires, and short enough that the customer is
// charged for the month they are actually about to use.
const autopayLeadTime = 24 * time.Hour

// autopayRetryInterval is the minimum gap between two attempts on one
// account. It also serves as the cross-replica claim window: an account whose
// last attempt is newer than this is not a candidate, so three replicas
// sharing one ticker cannot each charge the same card.
const autopayRetryInterval = 6 * time.Hour

// autopayMaxAttempts bounds the retries. After the third decline autopay is
// switched off and the customer is told to renew by hand -- continuing to
// hammer a dead card is how a payment provider starts asking questions.
const autopayMaxAttempts = 3

// autopayCharger is the slice of yookassa.YooKassaProvider the sweeper needs;
// tests substitute a stub.
type autopayCharger interface {
	ChargeSaved(ctx context.Context, orgID string, plan pricing.Plan, methodID, customerEmail string) (yookassa.ChargeResult, error)
}

// NewAutopayCharger builds the charger the sweeper uses, or returns nil when
// payments are unconfigured. Returning an untyped nil is the point: a typed
// nil pointer behind the interface would pass the sweeper's nil check and
// then panic on the first candidate.
func NewAutopayCharger(pool *pgxpool.Pool, cfg *config.Config) autopayCharger {
	if cfg.YooKassaShopID == "" || cfg.YooKassaSecretKey == "" {
		return nil
	}
	client := yookassa.New(cfg.YooKassaShopID, cfg.YooKassaSecretKey)
	return yookassa.NewProvider(pool, client, cfg.YooKassaReturnURL, cfg.YooKassaSendReceipt, cfg.YooKassaVatCode, cfg.YooKassaTaxSystemCode)
}

// autopayCandidate is one account due for automatic renewal.
type autopayCandidate struct {
	OrgID     string
	Plan      string
	ExpiresAt time.Time
	MethodID  string
	Failures  int
	Email     *string
}

// SweepAutopay runs one pass of automatic renewal: every paid account whose
// term ends within autopayLeadTime, that has a saved payment method and a live
// consent, is charged for another month.
//
// The window closes at expiry + planExpiryGrace, the same moment
// SweepPlanExpiry lapses the account to free -- past that point the customer
// has been downgraded and a surprise charge would be for a plan they no
// longer have.
//
// Every outcome is reported to the customer by mail. Charging silently is
// never an option, and neither is a mail failure blocking the charge: mail
// errors are logged and swallowed, the money path is authoritative.
func SweepAutopay(ctx context.Context, pool *pgxpool.Pool, charger autopayCharger, mailer expiryMailer, auditTo string, plans []pricing.Plan, now time.Time) {
	if charger == nil {
		return
	}
	rows, err := pool.Query(ctx, `
		SELECT ba.org_id, ba.plan, ba.plan_expires_at, ba.autopay_method_id, ba.autopay_failures,
		       (SELECT p.customer_email FROM payments p
		        WHERE p.org_id = ba.org_id AND p.status = 'succeeded' AND p.customer_email IS NOT NULL
		        ORDER BY p.paid_at DESC NULLS LAST LIMIT 1)
		FROM billing_accounts ba
		WHERE ba.plan <> 'free'
		  AND ba.plan_expires_at IS NOT NULL
		  AND ba.autopay_enabled
		  AND ba.autopay_method_id <> ''
		  AND ba.autopay_failures < $2
		  AND ba.plan_expires_at <= $1::timestamptz + make_interval(secs => $3)
		  AND ba.plan_expires_at + make_interval(secs => $4) > $1::timestamptz
		  AND (ba.autopay_last_attempt_at IS NULL OR ba.autopay_last_attempt_at < $1::timestamptz - make_interval(secs => $5))
	`, now, autopayMaxAttempts, autopayLeadTime.Seconds(), planExpiryGrace.Seconds(), autopayRetryInterval.Seconds())
	if err != nil {
		log.Printf("billing autopay: list candidates: %v", err)
		return
	}
	candidates := make([]autopayCandidate, 0)
	for rows.Next() {
		var a autopayCandidate
		if err := rows.Scan(&a.OrgID, &a.Plan, &a.ExpiresAt, &a.MethodID, &a.Failures, &a.Email); err != nil {
			rows.Close()
			log.Printf("billing autopay: scan candidate: %v", err)
			return
		}
		candidates = append(candidates, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("billing autopay: read candidates: %v", err)
		return
	}

	for _, a := range candidates {
		chargeAutopay(ctx, pool, charger, mailer, auditTo, plans, a, now)
	}
}

// chargeAutopay attempts one renewal. The attempt is claimed with a
// conditional UPDATE first: the same predicate the candidate query used, so a
// replica that lost the race writes zero rows and skips the charge entirely.
func chargeAutopay(ctx context.Context, pool *pgxpool.Pool, charger autopayCharger, mailer expiryMailer, auditTo string, plans []pricing.Plan, a autopayCandidate, now time.Time) {
	var plan *pricing.Plan
	for i := range plans {
		if plans[i].Key == a.Plan {
			plan = &plans[i]
			break
		}
	}
	if plan == nil || plan.PriceRUB <= 0 {
		log.Printf("billing autopay: org=%s plan=%s is not chargeable, skipping", a.OrgID, a.Plan)
		return
	}

	tag, err := pool.Exec(ctx, `
		UPDATE billing_accounts
		SET autopay_last_attempt_at = $2::timestamptz, updated_at = $2::timestamptz
		WHERE org_id = $1
		  AND autopay_enabled
		  AND autopay_method_id = $3
		  AND (autopay_last_attempt_at IS NULL OR autopay_last_attempt_at < $2::timestamptz - make_interval(secs => $4))
	`, a.OrgID, now, a.MethodID, autopayRetryInterval.Seconds())
	if err != nil {
		log.Printf("billing autopay: claim attempt org=%s: %v", a.OrgID, err)
		return
	}
	if tag.RowsAffected() == 0 {
		return
	}

	email := ""
	if a.Email != nil {
		email = *a.Email
	}
	result, err := charger.ChargeSaved(ctx, a.OrgID, *plan, a.MethodID, email)
	if err != nil {
		log.Printf("billing autopay: charge org=%s plan=%s: %v", a.OrgID, a.Plan, err)
		return
	}

	switch result.Outcome {
	case yookassa.ChargeSucceeded:
		if _, err := pool.Exec(ctx, `
			UPDATE billing_accounts SET autopay_failures = 0, updated_at = $2 WHERE org_id = $1
		`, a.OrgID, now); err != nil {
			log.Printf("billing autopay: reset failures org=%s: %v", a.OrgID, err)
		}
		log.Printf("billing autopay: org=%s plan=%s charged %s RUB", a.OrgID, a.Plan, result.AmountValue)
		notifyAutopayCharged(ctx, pool, mailer, auditTo, a, plan.Name, result, email, now)

	case yookassa.ChargePending:
		log.Printf("billing autopay: org=%s plan=%s charge pending, webhook will finish it", a.OrgID, a.Plan)

	case yookassa.ChargeFailed:
		attempt := a.Failures + 1
		final := attempt >= autopayMaxAttempts
		if _, err := pool.Exec(ctx, `
			UPDATE billing_accounts
			SET autopay_failures = $2,
			    autopay_enabled = CASE WHEN $3 THEN FALSE ELSE autopay_enabled END,
			    updated_at = $4
			WHERE org_id = $1
		`, a.OrgID, attempt, final, now); err != nil {
			log.Printf("billing autopay: record failure org=%s: %v", a.OrgID, err)
		}
		log.Printf("billing autopay: org=%s plan=%s declined (attempt %d/%d, final=%t): %s",
			a.OrgID, a.Plan, attempt, autopayMaxAttempts, final, result.Reason)
		if mailer != nil && email != "" {
			subject, body := notify.ComposeAutopayFailed(plan.Name, result.AmountValue, result.Reason,
				a.ExpiresAt.UTC().Format("2006-01-02 15:04"), attempt, autopayMaxAttempts, final)
			if err := mailer.Send(email, subject, body); err != nil {
				log.Printf("billing autopay: decline mail to %s failed: %v", email, err)
			}
		}
		if mailer != nil && auditTo != "" && final {
			subject, body := notify.ComposeAudit("AutopayDisabled", email, a.Plan, a.OrgID, now.UTC().Format(time.RFC3339))
			if err := mailer.Send(auditTo, subject, body); err != nil {
				log.Printf("billing autopay: operator copy to %s failed: %v", auditTo, err)
			}
		}
	}
}

// notifyAutopayCharged mails the customer the renewal notice, reading the new
// term back from the row the charge just extended so the date in the mail is
// the date in the database rather than an arithmetic guess.
func notifyAutopayCharged(ctx context.Context, pool *pgxpool.Pool, mailer expiryMailer, auditTo string, a autopayCandidate, planName string, result yookassa.ChargeResult, email string, now time.Time) {
	if mailer == nil {
		return
	}
	expiresAt := a.ExpiresAt.Add(30 * 24 * time.Hour)
	var fresh *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT plan_expires_at FROM billing_accounts WHERE org_id = $1`, a.OrgID,
	).Scan(&fresh); err != nil {
		log.Printf("billing autopay: read new term org=%s: %v", a.OrgID, err)
	} else if fresh != nil {
		expiresAt = *fresh
	}
	if email != "" {
		subject, body := notify.ComposeAutopayCharged(planName, result.AmountValue, expiresAt.UTC().Format("2006-01-02 15:04"))
		if err := mailer.Send(email, subject, body); err != nil {
			log.Printf("billing autopay: charge mail to %s failed: %v", email, err)
		}
	}
	if auditTo != "" {
		subject, body := notify.ComposeAudit("AutopayCharged", email, a.Plan+" ("+result.AmountValue+" RUB)", a.OrgID, now.UTC().Format(time.RFC3339))
		if err := mailer.Send(auditTo, subject, body); err != nil {
			log.Printf("billing autopay: operator copy to %s failed: %v", auditTo, err)
		}
	}
}

// SetBillingAutopay switches automatic renewal on or off for the org owning
// the project. Requires write role.
//
// Turning it off keeps the saved method and only lowers the flag. It used to
// erase the method too, which conflated two different decisions: "do not
// charge me automatically" and "forget my card". A user who paused renewal
// then had no way back except a full checkout, and the console could not show
// a payment method at all -- there was nothing left to show. Withdrawal of the
// instrument is its own action, DeleteBillingPaymentMethod, which erases the
// method and lowers the flag together.
//
// Turning it on is only possible when a saved method already exists -- consent
// without an instrument is a setting that silently does nothing.
//
// @ID          setBillingAutopay
// @Summary     Enable or disable automatic renewal
// @Description Switches YooKassa recurring charges for the org that owns the project. Disabling keeps the saved payment method so renewal can be resumed in one click; use DELETE /billing/payment-method to forget the card. Enabling requires a payment method saved by an earlier checkout. Requires write role on the project.
// @Tags        billing
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string          true "Project UUID"
// @Param       body      body     map[string]bool true "Autopay flag"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/billing/autopay [put]
func (h *Handler) SetBillingAutopay(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	var body struct {
		Enabled *bool `json:"enabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if *body.Enabled && !h.cfg.YooKassaRecurringEnabled {
		respondRecurringNotSupported(c)
		return
	}

	orgID, err := h.projectOrg(c.Request.Context(), projectID)
	if err != nil || orgID == "" {
		respondNotFound(c)
		return
	}

	now := time.Now().UTC()
	if !*body.Enabled {
		var disabledTitle string
		if err := h.pool.QueryRow(c.Request.Context(), `
			UPDATE billing_accounts
			SET autopay_enabled = FALSE, autopay_failures = 0, updated_at = $2
			WHERE org_id = $1
			RETURNING autopay_method_title
		`, orgID, now).Scan(&disabledTitle); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to disable autopay")
			return
		}
		h.recordSystemAudit(c.Request.Context(), auditEntry{
			Action:       "AutopayDisabled",
			ResourceKind: "BillingAccount",
			ResourceName: orgID,
			Outcome:      auditOutcomeSuccess,
		})
		c.JSON(http.StatusOK, gin.H{"autopay_enabled": false, "autopay_method_title": disabledTitle})
		return
	}

	var methodTitle string
	tag, err := h.pool.Exec(c.Request.Context(), `
		UPDATE billing_accounts
		SET autopay_enabled = TRUE, autopay_failures = 0, updated_at = $2
		WHERE org_id = $1 AND autopay_method_id <> ''
	`, orgID, now)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to enable autopay")
		return
	}
	if tag.RowsAffected() == 0 {
		respondError(c, http.StatusConflict, "no_saved_payment_method")
		return
	}
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT autopay_method_title FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&methodTitle); err != nil {
		log.Printf("billing autopay: read method title org=%s: %v", orgID, err)
	}
	h.recordSystemAudit(c.Request.Context(), auditEntry{
		Action:       "AutopayEnabled",
		ResourceKind: "BillingAccount",
		ResourceName: orgID,
		Outcome:      auditOutcomeSuccess,
	})
	c.JSON(http.StatusOK, gin.H{"autopay_enabled": true, "autopay_method_title": methodTitle})
}

// DeleteBillingPaymentMethod forgets the card saved by an earlier checkout and
// switches automatic renewal off with it.
//
// This is the half of the old disable behaviour that is genuinely about the
// instrument. Splitting it out is what lets SetBillingAutopay(false) keep the
// method: pausing renewal and withdrawing a card are different intents, and a
// user who only wanted the first should not have to re-enter a card to undo
// it. Autopay is lowered here too because consent cannot outlive the
// instrument it was given for -- a charge armed against a method we no longer
// hold is a charge that can only fail.
//
// Idempotent: an account with no saved method is already in the requested
// state, and answering 200 keeps a double-click from reading as an error.
//
// @ID          deleteBillingPaymentMethod
// @Summary     Forget the saved payment method
// @Description Deletes the card saved for recurring charges and turns automatic renewal off. Idempotent. Requires write role on the project.
// @Tags        billing
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/billing/payment-method [delete]
func (h *Handler) DeleteBillingPaymentMethod(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	orgID, err := h.projectOrg(c.Request.Context(), projectID)
	if err != nil || orgID == "" {
		respondNotFound(c)
		return
	}

	tag, err := h.pool.Exec(c.Request.Context(), `
		UPDATE billing_accounts
		SET autopay_enabled = FALSE, autopay_method_id = '', autopay_method_title = '', autopay_failures = 0, updated_at = $2
		WHERE org_id = $1 AND (autopay_method_id <> '' OR autopay_enabled)
	`, orgID, time.Now().UTC())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to delete payment method")
		return
	}
	if tag.RowsAffected() > 0 {
		h.recordSystemAudit(c.Request.Context(), auditEntry{
			Action:       "PaymentMethodDetached",
			ResourceKind: "BillingAccount",
			ResourceName: orgID,
			Outcome:      auditOutcomeSuccess,
		})
	}
	c.JSON(http.StatusOK, gin.H{"autopay_enabled": false, "autopay_method_title": ""})
}
