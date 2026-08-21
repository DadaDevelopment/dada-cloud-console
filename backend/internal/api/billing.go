package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
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

// consumptionExceededError is returned by checkConsumption when a free org has
// burned more list-price consumption this calendar month than its plan includes,
// multiplied by BILLING_OVERAGE_BLOCK_FACTOR.
type consumptionExceededError struct {
	SpentRUB    float64
	IncludedRUB float64
	Factor      float64
}

func (e *consumptionExceededError) Error() string {
	return fmt.Sprintf("consumption exceeded: spent=%.2f included=%.2f factor=%.1f", e.SpentRUB, e.IncludedRUB, e.Factor)
}

// orgMonthConsumptionRub sums the org's list-price consumption since the start
// of the current calendar month from the hourly app_usage ledger.
//
// The window is the calendar month because that is the period the allowance is
// stated in and the period a bill would settle over. A rolling 30 days would
// carry a spike the customer has already been billed for into a month they have
// not.
func (h *Handler) orgMonthConsumptionRub(ctx context.Context, orgID string) (float64, error) {
	var spent float64
	err := h.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(cost_rub), 0)::float8 FROM app_usage
		WHERE org_id = $1 AND hour_start >= date_trunc('month', now() AT TIME ZONE 'utc')
	`, orgID).Scan(&spent)
	return spent, err
}

// checkConsumption is the degradation the overage alert escalates to: a free
// org that has burned more than BILLING_OVERAGE_BLOCK_FACTOR times its included
// consumption stops being allowed to GROW.
//
// It only ever blocks growth. Running apps keep running, and shrinking is
// always allowed, because the point is to stop an unpaid footprint from getting
// bigger while nobody has decided to carry it -- not to take a service away
// from someone who was never told a rule. Counted quotas cannot do this job:
// they cap how MANY things an account has, and the accounts that cost real
// money are within every count while running fat replicas around the clock.
//
// Paid plans are excluded. Consumption past a plan the customer actually pays
// for is an invoice to raise, not a wall to hit; blocking there would punish
// exactly the accounts worth keeping.
//
// Every failure fails OPEN. A DB hiccup or an unreadable cluster-cost file must
// not turn into "nobody can deploy anything": the gate exists to slow down a
// handful of free accounts, and its own unavailability is not evidence about
// any of them.
func (h *Handler) checkConsumption(ctx context.Context, orgID string) error {
	if !h.cfg.BillingEnabled || h.cfg.BillingOverageBlockFactor <= 0 {
		return nil
	}
	if h.quotaExempt(orgID) {
		return nil
	}
	plan, err := h.planFor(ctx, orgID)
	if err != nil {
		log.Printf("billing: consumption gate open for org %s: plan lookup failed: %v", orgID, err)
		return nil
	}
	if plan.Key != "free" {
		return nil
	}
	included := pricing.IncludedConsumptionRub(plan, h.billingUnit)
	if included <= 0 {
		return nil
	}
	if h.quotaGraceActive(ctx, orgID) {
		return nil
	}
	spent, err := h.orgMonthConsumptionRub(ctx, orgID)
	if err != nil {
		log.Printf("billing: consumption gate open for org %s: ledger read failed: %v", orgID, err)
		return nil
	}
	if spent <= included*h.cfg.BillingOverageBlockFactor {
		return nil
	}
	return &consumptionExceededError{SpentRUB: spent, IncludedRUB: included, Factor: h.cfg.BillingOverageBlockFactor}
}

// recordConsumptionBlock leaves a trail every time the gate refuses growth. An
// account that suddenly cannot deploy will ask why, and the answer has to be
// findable without re-deriving the ledger by hand.
func (h *Handler) recordConsumptionBlock(ctx context.Context, orgID string, e *consumptionExceededError) {
	log.Printf("billing: GROWTH BLOCKED org=%s spent=%.2f included=%.2f factor=%.1f -- free account over its included consumption",
		orgID, e.SpentRUB, e.IncludedRUB, e.Factor)
	h.recordSystemAudit(ctx, auditEntry{
		Action:       "ConsumptionBlocked",
		ResourceKind: "BillingAccount",
		ResourceName: orgID,
		Outcome:      auditOutcomeSuccess,
		Metadata: map[string]string{
			"spent_rub":    strconv.FormatFloat(e.SpentRUB, 'f', 2, 64),
			"included_rub": strconv.FormatFloat(e.IncludedRUB, 'f', 2, 64),
			"factor":       strconv.FormatFloat(e.Factor, 'f', -1, 64),
		},
	})
}

// respondConsumptionExceeded writes the 403 a blocked growth request gets. It
// names both numbers: a wall whose reason a customer cannot check is a support
// ticket, and the numbers are the same ones on their own usage page.
func respondConsumptionExceeded(c *gin.Context, e *consumptionExceededError) {
	c.JSON(http.StatusForbidden, gin.H{
		"error":        "consumption_exceeded",
		"spent_rub":    e.SpentRUB,
		"included_rub": e.IncludedRUB,
		"upgrade":      true,
		"message":      "Free plan consumption exceeded. Existing apps keep running; upgrade or scale down to add or grow resources.",
	})
}

// billingBlockAudit renders a gate refusal into audit metadata and reports
// whether the error came from a gate at all. Call sites merge their own fields
// on top; the shared keys are here so "why was this refused" reads the same in
// the audit log whichever resource was asked for.
func billingBlockAudit(err error) (map[string]any, bool) {
	var qe *quotaExceededError
	if errors.As(err, &qe) {
		return map[string]any{"reason": "quota_exceeded", "resource": qe.Resource, "limit": qe.Limit}, true
	}
	var ce *consumptionExceededError
	if errors.As(err, &ce) {
		return map[string]any{"reason": "consumption_exceeded", "spent_rub": ce.SpentRUB, "included_rub": ce.IncludedRUB}, true
	}
	return nil, false
}

// respondBillingBlocked writes the response for whichever billing gate refused
// the request and reports whether it handled the error. Growth paths route both
// gates through one helper so a new gate cannot be added and then silently
// ignored at a call site that only type-asserted the older one.
func (h *Handler) respondBillingBlocked(c *gin.Context, orgID string, err error) bool {
	var qe *quotaExceededError
	if errors.As(err, &qe) {
		respondQuotaExceeded(c, qe.Resource, qe.Limit)
		return true
	}
	var ce *consumptionExceededError
	if errors.As(err, &ce) {
		h.recordConsumptionBlock(c.Request.Context(), orgID, ce)
		respondConsumptionExceeded(c, ce)
		return true
	}
	return false
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
			WHERE p.org_id = $1 AND rs.kind = 'App' AND `+notOrphanedSnapshot+`
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

	case "app_servers":
		var n int
		err := h.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM app_servers a
			JOIN projects p ON p.id = a.project_id
			WHERE p.org_id = $1 AND a.status != 'Deleted'
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

// checkQuota is the hard gate every growth path already calls, so the
// consumption gate lives inside it rather than beside it: a path that forgets to
// call the newer gate is a path where an over-consuming account keeps growing,
// and there is no way to notice that from reading the newer gate. It returns a
// *quotaExceededError when the org is at or over its plan limit (a limit of 0
// means unlimited, i.e. Enterprise), or a *consumptionExceededError when the org
// is over what its free plan includes. Callers route both through
// respondBillingBlocked.
//
// Grace does not skip the count. An org inside its grandfathering window is
// still allowed through -- that promise is not being taken back -- but the
// breach is now recorded rather than passed over in silence. Silence was the
// bug: an org could drift three apps past a one-app plan for weeks, see
// nothing anywhere, and then hit a wall the day grace ended. recordQuotaBreach
// gives the console banner and the grace reminder mail something factual to
// shout about while there is still time to act.
//
// Grace stops at a hard zero. A limit of 0 that is not the Enterprise
// unlimited convention means the plan includes none of the resource at all,
// so there is no prior legal usage to grandfather -- letting it through would
// hand a free org a resource it never had rather than protecting one it did.
// app_servers is the live case: every free org carries grace to 2026-09-25
// and none of them owns a VM [live 2026-08-13], so grace was about to gift
// seven free public-IP machines through the exact gate that exists to stop
// farming. Orgs that really do hold over-limit resources still keep their
// window, because those resources sit on plans with a non-zero limit.
func (h *Handler) checkQuota(ctx context.Context, orgID, resource string) error {
	if !h.cfg.BillingEnabled {
		return nil
	}
	if h.quotaExempt(orgID) {
		return nil
	}
	if err := h.checkConsumption(ctx, orgID); err != nil {
		return err
	}
	inGrace := h.quotaGraceActive(ctx, orgID)
	plan, err := h.planFor(ctx, orgID)
	if err != nil {
		if inGrace {
			return nil
		}
		return err
	}
	limit, known := pricing.Quota(plan, resource)
	if !known {
		return nil
	}
	if limit == 0 && zeroLimitMeansUnlimited(resource, plan.Key) {
		return nil
	}
	if limit == 0 {
		return &quotaExceededError{Resource: resource, Limit: limit}
	}
	count, err := h.countResource(ctx, orgID, resource)
	if err != nil {
		if inGrace {
			return nil
		}
		return err
	}
	if count < limit {
		return nil
	}
	if inGrace {
		h.recordQuotaBreach(ctx, orgID, plan.Key, resource, count, limit)
		return nil
	}
	return &quotaExceededError{Resource: resource, Limit: limit}
}

// zeroLimitMeansUnlimited reports whether a plan's zero-value quota for a
// resource should be read the traditional way (Enterprise, unlimited) rather
// than as a real cap. Every resource except app_servers only ever carries
// zero on the Enterprise plan, so a bare zero has always meant unlimited
// there. app_servers is the first resource where the Free plan's real,
// enforced limit is zero -- a VM is a standing box with a public IP handed to
// any authenticated signup, the exact shape of the farming vector the plan
// gate exists to close -- so a zero on any plan but Enterprise blocks instead
// of waving the request through.
func zeroLimitMeansUnlimited(resource, planKey string) bool {
	if planKey == "enterprise" {
		return true
	}
	return resource != "app_servers"
}

// recordQuotaBreach is the loud half of grandfathering: a warning line in the
// server log, a counter on the account, and an audit event, every time grace
// lets a resource through that the plan does not cover. None of it blocks the
// request -- it exists so the over-limit state is visible somewhere other than
// the customer's future error message.
func (h *Handler) recordQuotaBreach(ctx context.Context, orgID, planKey, resource string, count, limit int) {
	log.Printf("billing: QUOTA BREACH ALLOWED BY GRACE org=%s plan=%s resource=%s used=%d limit=%d -- creation will start failing when the grace window ends",
		orgID, planKey, resource, count+1, limit)
	if _, err := h.pool.Exec(ctx, `
		UPDATE billing_accounts
		SET quota_breach_count = quota_breach_count + 1, quota_breach_last_at = $2, updated_at = $2
		WHERE org_id = $1
	`, orgID, time.Now().UTC()); err != nil {
		log.Printf("billing: record quota breach for org %s: %v", orgID, err)
	}
	h.recordSystemAudit(ctx, auditEntry{
		Action:       "QuotaBreachAllowed",
		ResourceKind: "BillingAccount",
		ResourceName: orgID,
		Outcome:      auditOutcomeSuccess,
		Metadata: map[string]string{
			"plan":     planKey,
			"resource": resource,
			"used":     strconv.Itoa(count + 1),
			"limit":    strconv.Itoa(limit),
		},
	})
}

// autopayNextCharge is when automatic renewal will next take money, or nil
// when it will not. The console shows this instead of the term end date: what
// a customer with autopay on needs to know is the day their card is charged,
// which is a day earlier.
func autopayNextCharge(enabled bool, methodTitle string, planExpiresAt *time.Time) *time.Time {
	if !enabled || methodTitle == "" || planExpiresAt == nil {
		return nil
	}
	at := planExpiresAt.Add(-autopayLeadTime)
	return &at
}

// overQuotaLines lists the resources an org is currently over the limit on,
// with the numbers the console needs to name them. Empty means compliant.
func overQuotaLines(usage map[string]gin.H) []gin.H {
	over := make([]gin.H, 0)
	for _, res := range []string{"apps", "databases", "domains", "team_members"} {
		row, ok := usage[res]
		if !ok {
			continue
		}
		used, uok := row["used"].(int)
		limit, lok := row["limit"].(int)
		if !uok || !lok || limit == 0 || used <= limit {
			continue
		}
		over = append(over, gin.H{"resource": res, "used": used, "limit": limit})
	}
	return over
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

// orgStorageBytes sums the storage the org actually holds: the persistent
// volumes attached to its apps plus the on-disk size of its managed databases.
// The database half reads db_quota_state, the same rows the quota worker writes
// from pg_database_size, so the gigabytes on the billing page and the gigabytes
// that put a database into read-only are one measurement rather than two that
// drift apart.
//
// Reported, not enforced. The plan's storage_gb still gates a single app volume
// at create time (storageCapBytes) and managed databases are gated by their own
// per-database tier; this figure exists because a customer whose database has
// grown to 15 GB currently sees that number nowhere in the console.
func (h *Handler) orgStorageBytes(ctx context.Context, orgID string) (int64, error) {
	var total int64
	rows, err := h.pool.Query(ctx, `
		SELECT rs.summary_json->'volume'->>'size'
		FROM resource_snapshots rs
		JOIN projects p ON p.id = rs.project_id
		WHERE p.org_id = $1 AND rs.kind = 'App' AND `+notOrphanedSnapshot+`
		  AND rs.summary_json->'volume'->>'size' IS NOT NULL
	`, orgID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var size string
		if err := rows.Scan(&size); err != nil {
			return 0, err
		}
		total += quantityBytes(size)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var dbBytes int64
	if err := h.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(q.size_bytes), 0)
		FROM db_quota_state q
		JOIN projects p ON p.id = q.project_id
		WHERE p.org_id = $1
	`, orgID).Scan(&dbBytes); err != nil {
		return 0, err
	}
	return total + dbBytes, nil
}

// storageUsedGB rounds bytes up to whole gigabytes. Rounding up rather than
// truncating keeps a customer holding 1.4 GB from reading "1 GB of 10" and
// concluding the page ignores what they just wrote; the plan ladder is in whole
// gigabytes, so a fraction has nowhere else to go.
func storageUsedGB(b int64) int {
	if b <= 0 {
		return 0
	}
	const gib = int64(1) << 30
	return int((b + gib - 1) / gib)
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
	var autopayEnabled bool
	var autopayMethodTitle string
	var autopayFailures int
	if err := h.pool.QueryRow(c.Request.Context(), `
		SELECT plan_expires_at, quota_grace_until, autopay_enabled, autopay_method_title, autopay_failures
		FROM billing_accounts WHERE org_id = $1
	`, orgID).Scan(&planExpiresAt, &quotaGraceUntil, &autopayEnabled, &autopayMethodTitle, &autopayFailures); err != nil && !errors.Is(err, pgx.ErrNoRows) {
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
		"quota_over_limit":  overQuotaLines(usage),
		"autopay": gin.H{
			"enabled":      autopayEnabled,
			"methodTitle":  autopayMethodTitle,
			"failures":     autopayFailures,
			"nextChargeAt": autopayNextCharge(autopayEnabled, autopayMethodTitle, planExpiresAt),
			"supported":    h.cfg.YooKassaRecurringEnabled,
		},
		"quotas": plan.Quotas,
		"usage":  usage,
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
	projectID, parseErr := uuid.Parse(c.Param("projectId"))

	var body struct {
		Plan string `json:"plan" binding:"required"`
	}
	audit := func(outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			Action:       "AssignPlan",
			ResourceKind: "BillingAccount",
			ResourceName: body.Plan,
			Outcome:      outcome,
			Metadata:     meta,
		})
	}
	reject := func(status int, reason string) {
		audit(auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
	}

	if !isGod(claims) {
		reject(http.StatusForbidden, "not_a_platform_admin")
		respondForbidden(c)
		return
	}
	if parseErr != nil {
		projectID = uuid.Nil
		reject(http.StatusNotFound, "bad_project_id")
		respondNotFound(c)
		return
	}
	_, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		reject(http.StatusNotFound, "not_a_member")
		respondNotFound(c)
		return
	}
	if err != nil {
		reject(http.StatusInternalServerError, "membership_check_failed")
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		reject(http.StatusBadRequest, "malformed_body")
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
		reject(http.StatusBadRequest, "unknown_plan")
		respondError(c, http.StatusBadRequest, "unknown plan key: "+body.Plan)
		return
	}
	orgID, err := h.projectOrg(c.Request.Context(), projectID)
	if err != nil {
		reject(http.StatusNotFound, "org_lookup_failed")
		respondNotFound(c)
		return
	}
	provider := &billing.ManualProvider{Pool: h.pool}
	if err := provider.AssignPlan(c.Request.Context(), orgID, body.Plan); err != nil {
		reject(http.StatusInternalServerError, "assign_failed")
		respondError(c, http.StatusInternalServerError, "failed to assign plan")
		return
	}
	audit(auditOutcomeSuccess, map[string]any{"org_id": orgID, "plan": body.Plan})
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

	bytes, err := h.orgStorageBytes(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("billing: storage usage: %w", err)
	}
	storageLimit, _ := pricing.Quota(plan, "storage_gb")
	out["storage_gb"] = gin.H{"used": storageUsedGB(bytes), "limit": storageLimit}
	return out, nil
}
