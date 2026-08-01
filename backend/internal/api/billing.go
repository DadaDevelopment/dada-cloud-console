package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// quotaExceededError is returned by checkQuota when the org is at or over
// its plan limit for a countable resource.
type quotaExceededError struct {
	Resource string
	Limit    int
}

func (e *quotaExceededError) Error() string {
	return fmt.Sprintf("quota exceeded: %s limit=%d", e.Resource, e.Limit)
}

// planFor resolves the org's current plan from billing_accounts and finds
// the matching pricing.Plan from the handler's loaded plan set. If the org
// has no billing_accounts row the free plan is used.
func (h *Handler) planFor(ctx context.Context, orgID string) (pricing.Plan, error) {
	var planKey string
	err := h.pool.QueryRow(ctx,
		`SELECT plan FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&planKey)
	if errors.Is(err, pgx.ErrNoRows) {
		planKey = "free"
	} else if err != nil {
		return pricing.Plan{}, fmt.Errorf("billing: planFor: %w", err)
	}

	for _, p := range h.billingPlans {
		if p.Key == planKey {
			return p, nil
		}
	}
	for _, p := range h.billingPlans {
		if p.Key == "free" {
			return p, nil
		}
	}
	if len(h.billingPlans) > 0 {
		return h.billingPlans[0], nil
	}
	return pricing.Plan{}, fmt.Errorf("billing: no plans loaded")
}

// countResource counts the live number of a resource type owned by the org
// across all its projects.
func (h *Handler) countResource(ctx context.Context, orgID, resource string) (int, error) {
	switch resource {
	case "apps":
		var n int
		err := h.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM resource_snapshots rs
			JOIN projects p ON p.id = rs.project_id
			WHERE p.org_id = $1 AND rs.kind = 'App'
		`, orgID).Scan(&n)
		return n, err

	case "databases":
		var n int
		err := h.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM resource_snapshots rs
			JOIN projects p ON p.id = rs.project_id
			WHERE p.org_id = $1 AND rs.kind IN ('ServiceDatabase', 'ServiceDatabaseV2')
		`, orgID).Scan(&n)
		return n, err

	case "domains":
		var n int
		err := h.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM domain_authorizations da
			JOIN projects p ON p.id = da.project_id
			WHERE p.org_id = $1
		`, orgID).Scan(&n)
		return n, err

	case "box_minutes":
		// The quota gate on box minutes reads the SAME function the meter writes
		// usage_records from (countOrgBoxMinutes), so the figure a customer sees on
		// /billing/account and the figure that refuses their next box are one query.
		// Two queries here would eventually disagree, and a customer refused a box
		// while their own usage page shows them under the limit has been given a
		// reason to distrust every other number we publish.
		return countOrgBoxMinutes(ctx, h.pool, orgID, h.clock())

	case "team_members":
		return 0, nil
	}
	return 0, fmt.Errorf("billing: unknown resource %q", resource)
}

// quotaExempt reports whether the org is outside the customer plan ladder
// entirely (BILLING_EXEMPT_ORGS — the platform's own org and its demo/e2e
// estate).
func (h *Handler) quotaExempt(orgID string) bool {
	for _, exempt := range h.cfg.BillingExemptOrgs {
		if exempt == orgID {
			return true
		}
	}
	return false
}

// quotaGraceActive reports whether the org is inside its grandfathering
// window. Orgs that already exceeded the free quotas when enforcement was
// switched on (migration 055) carry a 60-day grace so enforcement never
// blocks work that was legal when it started; they see the upgrade prompt,
// not a wall. A missing row or a NULL/past value means no grace.
func (h *Handler) quotaGraceActive(ctx context.Context, orgID string) bool {
	var graceUntil *time.Time
	err := h.pool.QueryRow(ctx,
		`SELECT quota_grace_until FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&graceUntil)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("billing: quota grace lookup failed for org %s: %v", orgID, err)
		}
		return false
	}
	return graceUntil != nil && graceUntil.After(time.Now().UTC())
}

// checkQuota is the hard gate for countable resources. It returns a
// *quotaExceededError when the org is at or over its plan limit. A limit of 0
// means unlimited (Enterprise).
func (h *Handler) checkQuota(ctx context.Context, orgID, resource string) error {
	if !h.cfg.BillingEnabled {
		return nil
	}
	if h.quotaExempt(orgID) || h.quotaGraceActive(ctx, orgID) {
		return nil
	}
	plan, err := h.planFor(ctx, orgID)
	if err != nil {
		return err
	}
	limit, known := pricing.Quota(plan, resource)
	if !known || limit == 0 {
		return nil
	}
	count, err := h.countResource(ctx, orgID, resource)
	if err != nil {
		return err
	}
	if count >= limit {
		return &quotaExceededError{Resource: resource, Limit: limit}
	}
	return nil
}

// respondQuotaExceeded writes the quota-exceeded 403 JSON body.
func respondQuotaExceeded(c *gin.Context, resource string, limit int) {
	c.JSON(http.StatusForbidden, gin.H{
		"error":    "quota_exceeded",
		"resource": resource,
		"limit":    limit,
		"upgrade":  true,
		"message":  "Upgrade your plan to add more " + resource,
	})
}

// storageCapBytes resolves the plan-aware ceiling on a single app's
// persistent volume for the given org, in bytes, plus the same value in GB
// for the quota-exceeded error body. A cap of 0 means unlimited (Enterprise
// plan, or an exempt org).
//
// Billing-disabled deployments keep the legacy flat 10Gi ceiling. Exempt
// orgs are unlimited. quotaGraceActive is deliberately NOT consulted here:
// grace is about not blocking orgs that were already over quota when
// enforcement switched on, and for storage that is handled by the
// current-size allowance in UpdateAppStorage instead.
func (h *Handler) storageCapBytes(ctx context.Context, orgID string) (int64, int, error) {
	if !h.cfg.BillingEnabled {
		return quantityBytes("10Gi"), 10, nil
	}
	if h.quotaExempt(orgID) {
		return 0, 0, nil
	}
	plan, err := h.planFor(ctx, orgID)
	if err != nil {
		return 0, 0, err
	}
	gb := plan.Quotas.StorageGB
	if gb == 0 {
		return 0, 0, nil
	}
	return int64(gb) << 30, gb, nil
}

// GetBillingPlans returns all loaded plans.
//
// @ID          getBillingPlans
// @Summary     List billing plans
// @Description Returns all available billing plans with quotas, capabilities, and pricing.
// @Tags        billing
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{}
// @Router      /billing/plans [get]
func (h *Handler) GetBillingPlans(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"plans": h.billingPlans})
}

// GetBillingAccount returns the org's plan, per-resource quotas + usage, and
// an invoice preview for the current calendar month.
//
// @ID          getBillingAccount
// @Summary     Get billing account
// @Description Returns the plan, quota usage, and monthly invoice preview for the org that owns the project. quota_enforced tells the caller whether quotas actually block new resource creation for this org right now (false when billing is disabled platform-wide, the org is exempt, or the org is inside its grace window) -- used=limit is informational, not a blocker, unless this is true.
// @Tags        billing
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/billing/account [get]
func (h *Handler) GetBillingAccount(c *gin.Context) {
	_, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	orgID, err := h.projectOrg(c.Request.Context(), projectID)
	if err != nil {
		respondNotFound(c)
		return
	}
	plan, err := h.planFor(c.Request.Context(), orgID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve billing plan")
		return
	}
	usage, err := h.buildUsage(c.Request.Context(), orgID, plan)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to compute usage")
		return
	}
	now := time.Now().UTC()
	var planExpiresAt, quotaGraceUntil *time.Time
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT plan_expires_at, quota_grace_until FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&planExpiresAt, &quotaGraceUntil); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("billing: plan term lookup skipped for org %s: %v", orgID, err)
	}
	if quotaGraceUntil != nil && !quotaGraceUntil.After(now) {
		quotaGraceUntil = nil
	}
	period := fmt.Sprintf("%d-%02d", now.Year(), now.Month())
	quotaEnforced := h.cfg.BillingEnabled && !h.quotaExempt(orgID) && !h.quotaGraceActive(c.Request.Context(), orgID)

	lineItems := []gin.H{
		{"kind": "plan", "label": plan.Key, "amount": plan.PriceRUB},
	}
	total := plan.PriceRUB

	from, to := currentBillingMonthUTC(now)
	if bill, aerr := h.agentTokenBillForOrg(c.Request.Context(), orgID, from, to); aerr != nil {
		log.Printf("billing: agent-token line skipped for org %s: %v", orgID, aerr)
	} else if bill.RevenueRUB > 0 {
		lineItems = append(lineItems, gin.H{
			"kind":    "agent_tokens",
			"label":   "AI agent usage",
			"amount":  bill.RevenueRUB,
			"tokens":  bill.TotalTokens,
			"costUSD": bill.CostUSD,
		})
		total += bill.RevenueRUB
	}

	c.JSON(http.StatusOK, gin.H{
		"plan":              plan.Key,
		"plan_expires_at":   planExpiresAt,
		"quota_grace_until": quotaGraceUntil,
		"quota_enforced":    quotaEnforced,
		"quotas":            plan.Quotas,
		"usage":             usage,
		"invoicePreview": gin.H{
			"period":    period,
			"amount":    total,
			"currency":  "RUB",
			"status":    "preview",
			"lineItems": lineItems,
		},
	})
}

// GetBillingUsage returns per-resource usage vs quota for the org owning the project.
//
// @ID          getBillingUsage
// @Summary     Get billing usage
// @Description Returns per-resource current usage versus plan quota for the org that owns the project.
// @Tags        billing
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/billing/usage [get]
func (h *Handler) GetBillingUsage(c *gin.Context) {
	_, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	orgID, err := h.projectOrg(c.Request.Context(), projectID)
	if err != nil {
		respondNotFound(c)
		return
	}
	plan, err := h.planFor(c.Request.Context(), orgID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve billing plan")
		return
	}
	usage, err := h.buildUsage(c.Request.Context(), orgID, plan)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to compute usage")
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": usage})
}

// RecommendPlan accepts a Need body and returns the cheapest fitting plan.
//
// @ID          recommendBillingPlan
// @Summary     Recommend a billing plan
// @Description Returns the cheapest plan that satisfies the supplied resource requirements.
// @Tags        billing
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body     pricing.Need true "Resource requirements"
// @Success     200  {object} map[string]interface{}
// @Failure     400  {object} map[string]string
// @Router      /billing/recommend-plan [post]
func (h *Handler) RecommendPlan(c *gin.Context) {
	var need pricing.Need
	if err := c.ShouldBindJSON(&need); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	plan, reason := pricing.RecommendPlan(need, h.billingPlans)
	c.JSON(http.StatusOK, gin.H{"recommended": plan.Key, "reason": reason})
}

// AssignPlan upserts billing_accounts for the org owning the project.
// Platform-admin only (/platform-admins group) -- a self-service caller could
// otherwise assign themselves a paid plan for free. Plan key must exist in
// the loaded plan set. Real plan changes go through YooKassaProvider.Checkout
// (checkout + webhook), which flips the plan only after payment succeeds;
// this endpoint stays for manual/support-driven overrides.
//
// @ID          assignBillingPlan
// @Summary     Assign a billing plan (platform-admin only)
// @Description Upserts the org's plan in billing_accounts. Platform-admin only (/platform-admins group); every other caller gets 403.
// @Tags        billing
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                 true "Project UUID"
// @Param       body      body     map[string]string      true "Plan key"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/billing/plan [put]
func (h *Handler) AssignPlan(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
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
	var body struct {
		Plan string `json:"plan" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	var found bool
	for _, p := range h.billingPlans {
		if p.Key == body.Plan {
			found = true
			break
		}
	}
	if !found {
		respondError(c, http.StatusBadRequest, "unknown plan key: "+body.Plan)
		return
	}
	orgID, err := h.projectOrg(c.Request.Context(), projectID)
	if err != nil {
		respondNotFound(c)
		return
	}
	provider := &billing.ManualProvider{Pool: h.pool}
	if err := provider.AssignPlan(c.Request.Context(), orgID, body.Plan); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to assign plan")
		return
	}
	c.JSON(http.StatusOK, gin.H{"org_id": orgID, "plan": body.Plan})
}

// buildUsage constructs the per-resource {used, limit} map for GetBillingAccount
// and GetBillingUsage.
func (h *Handler) buildUsage(ctx context.Context, orgID string, plan pricing.Plan) (map[string]gin.H, error) {
	resources := []string{"apps", "databases", "domains", "team_members"}
	out := make(map[string]gin.H, len(resources))
	for _, res := range resources {
		used, err := h.countResource(ctx, orgID, res)
		if err != nil {
			return nil, fmt.Errorf("billing: count %s: %w", res, err)
		}
		limit, _ := pricing.Quota(plan, res)
		out[res] = gin.H{"used": used, "limit": limit}
	}
	return out, nil
}
