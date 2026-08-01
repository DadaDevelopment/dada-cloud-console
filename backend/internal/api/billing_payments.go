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
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// paymentResponse is one row of GET /projects/{projectId}/billing/payments.
type paymentResponse struct {
	ID          string     `json:"id"`
	Plan        string     `json:"plan"`
	AmountValue string     `json:"amount_value"`
	Currency    string     `json:"currency"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	PaidAt      *time.Time `json:"paid_at,omitempty"`
}

// BillingCheckout starts a YooKassa payment for a paid plan on the org owning
// the project. Requires write role. Price is always resolved server-side
// from the loaded plan catalog -- the client only names the plan key.
//
// @ID          billingCheckout
// @Summary     Start a plan checkout
// @Description Creates a YooKassa payment for the requested plan and returns the confirmation redirect URL. Requires write role on the project.
// @Tags        billing
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string            true "Project UUID"
// @Param       body      body     map[string]string true "Plan key"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/billing/checkout [post]
func (h *Handler) BillingCheckout(c *gin.Context) {
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
		Plan string `json:"plan" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if body.Plan == "free" || body.Plan == "enterprise" {
		respondError(c, http.StatusBadRequest, "plan is not payable: "+body.Plan)
		return
	}
	var plan *pricing.Plan
	for i := range h.billingPlans {
		if h.billingPlans[i].Key == body.Plan {
			plan = &h.billingPlans[i]
			break
		}
	}
	if plan == nil {
		respondError(c, http.StatusBadRequest, "unknown plan key: "+body.Plan)
		return
	}

	if h.yookassa == nil {
		respondError(c, http.StatusConflict, "payments_not_configured")
		return
	}

	orgID, err := h.projectOrg(c.Request.Context(), projectID)
	if err != nil {
		respondNotFound(c)
		return
	}
	if orgID == "" {
		log.Printf("payments: checkout refused, project=%s has no org_id", projectID)
		respondError(c, http.StatusConflict, "org_unresolved")
		return
	}

	paymentID, confirmationURL, err := h.yookassa.Checkout(c.Request.Context(), orgID, *plan, claims.Email, claims.Subject, projectID.String())
	if err != nil {
		log.Printf("payments: checkout failed org=%s plan=%s: %v", orgID, body.Plan, err)
		respondError(c, http.StatusInternalServerError, "failed to start checkout")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"payment_id":       paymentID,
		"confirmation_url": confirmationURL,
	})
}

// YooKassaWebhook ingests a YooKassa payment.succeeded / payment.canceled
// webhook. Public route (no JWT) -- YooKassa webhooks carry no signature, so
// the payload's claimed status is NEVER trusted; the authoritative status is
// always re-fetched from the YooKassa API by object.id before any row changes.
//
// @ID          yookassaWebhook
// @Summary     YooKassa webhook (public)
// @Description Re-fetches the authoritative payment status by id and applies it. Never trusts the webhook payload's status field.
// @Tags        billing
// @Accept      json
// @Produce     json
// @Success     200 {object} map[string]interface{}
// @Failure     400 {object} map[string]string
// @Failure     500 {object} map[string]string
// @Router      /webhooks/yookassa [post]
func (h *Handler) YooKassaWebhook(c *gin.Context) {
	var payload struct {
		Event  string `json:"event"`
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if payload.Object.ID == "" {
		respondError(c, http.StatusBadRequest, "missing object.id")
		return
	}
	if h.yookassa == nil {
		log.Printf("payments: webhook received but payments not configured, yk_id=%s", payload.Object.ID)
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}

	result, err := h.yookassa.ProcessWebhook(c.Request.Context(), payload.Object.ID)
	if err != nil {
		log.Printf("payments: webhook re-fetch failed yk_id=%s: %v", payload.Object.ID, err)
		respondError(c, http.StatusInternalServerError, "failed to verify payment")
		return
	}

	switch result.Outcome {
	case yookassa.OutcomeUnknownPayment:
		log.Printf("payments: webhook for unknown yk_payment_id=%s", payload.Object.ID)
	case yookassa.OutcomeSucceeded:
		log.Printf("payments: succeeded org=%s plan=%s amount=%s", result.OrgID, result.Plan, result.AmountValue)
		h.notifyPaymentSuccess(result)
	case yookassa.OutcomeCanceled:
		log.Printf("payments: canceled org=%s plan=%s amount=%s", result.OrgID, result.Plan, result.AmountValue)
	}
	h.recordPaymentOutcomeAudit(c.Request.Context(), payload.Object.ID, result)

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// recordPaymentOutcomeAudit writes one audit_events row per terminal webhook
// outcome (succeeded / canceled / already_processed / unknown_payment). This
// is the only durable trail a payment leaves outside the payments table
// itself -- before this, a payment that succeeded without the plan landing
// (P0-PAY-5) left zero rows anywhere and was only found a week later by
// manually joining two tables. Best-effort: an audit failure must never flip
// the webhook's HTTP response, so writeAudit's own swallow-and-log is relied
// on here rather than re-implemented.
func (h *Handler) recordPaymentOutcomeAudit(ctx context.Context, ykPaymentID string, result yookassa.WebhookResult) {
	meta := map[string]string{
		"yk_payment_id": ykPaymentID,
		"outcome":       string(result.Outcome),
	}
	if result.Plan != "" {
		meta["plan"] = result.Plan
	}
	if result.AmountValue != "" {
		meta["amount_value"] = result.AmountValue
	}
	outcome := auditOutcomeSuccess
	if result.Outcome == yookassa.OutcomeUnknownPayment {
		outcome = auditOutcomeFailure
	}
	h.recordSystemAudit(ctx, auditEntry{
		Action:       "PaymentWebhook",
		ResourceKind: "Payment",
		ResourceName: result.OrgID,
		Outcome:      outcome,
		Metadata:     meta,
	})
}

// notifyPaymentSuccess sends the customer receipt email and an operator copy
// off the webhook's hot path, mirroring notifyAuditEvent's fire-and-forget
// pattern. Every failure is logged and swallowed -- a mail outage must never
// affect webhook processing (YooKassa expects a fast 200).
func (h *Handler) notifyPaymentSuccess(result yookassa.WebhookResult) {
	if h.auditNotifier == nil {
		return
	}
	notifier := h.auditNotifier
	auditTo := h.auditNotifyEmail
	res := result

	go func() {
		_, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if res.CustomerEmail != "" {
			subject, body := notify.ComposePaymentSuccess(res.Plan, res.AmountValue)
			if err := notifier.Send(res.CustomerEmail, subject, body); err != nil {
				log.Printf("payments: customer receipt send to %s failed: %v", res.CustomerEmail, err)
			}
		}
		if auditTo != "" {
			createdAtUTC := time.Now().UTC().Format(time.RFC3339)
			subject, body := notify.ComposeAudit("PaymentSucceeded", res.CustomerEmail, res.Plan+" ("+res.AmountValue+" RUB)", res.OrgID, createdAtUTC)
			if err := notifier.Send(auditTo, subject, body); err != nil {
				log.Printf("payments: operator copy send to %s failed: %v", auditTo, err)
			}
		}
	}()
}

// GetBillingPayments returns the last 20 payments for the org owning the
// project, newest first. Any project member (read role or above) can view it.
//
// @ID          getBillingPayments
// @Summary     List recent payments
// @Description Returns the last 20 payments rows for the org that owns the project, newest first.
// @Tags        billing
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/billing/payments [get]
func (h *Handler) GetBillingPayments(c *gin.Context) {
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
	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	orgID, err := h.projectOrg(c.Request.Context(), projectID)
	if err != nil {
		respondNotFound(c)
		return
	}

	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT id, plan, amount_value::text, currency, status, created_at, paid_at
		FROM payments WHERE org_id = $1 ORDER BY created_at DESC LIMIT 20
	`, orgID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list payments")
		return
	}
	defer rows.Close()

	payments := make([]paymentResponse, 0)
	for rows.Next() {
		var p paymentResponse
		if err := rows.Scan(&p.ID, &p.Plan, &p.AmountValue, &p.Currency, &p.Status, &p.CreatedAt, &p.PaidAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read payments")
			return
		}
		payments = append(payments, p)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read payments")
		return
	}

	c.JSON(http.StatusOK, gin.H{"payments": payments})
}
