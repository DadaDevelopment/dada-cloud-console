package api

import (
	"net/http"
	"strconv"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
)

const (
	auditCoverageDefaultDays = 30
	auditCoverageMaxDays     = 90
)

// auditCoverageGap is one action whose operations did not all leave a trace.
type auditCoverageGap struct {
	Action     string `json:"action"`
	Operations int    `json:"operations"`
	Audited    int    `json:"audited"`
	Missing    int    `json:"missing"`
}

// GetAuditCoverage answers "which finished operations left no trace in the audit
// trail", per action, over a window.
//
// It measures the half of the trail no static check can see. The source gate
// (TestEveryMutatingHandlerAudits) reads handler bodies, so it only knows about
// actions a handler starts; an operation enqueued by an agent as a follow-up has
// no handler to inspect. That is exactly where the trail went quiet: DeployStack
// is enqueued by the gitops agent and AttachDefaultDomain by the platform's own
// self-repair, so neither was audited at enqueue, and both reached audit_events
// only when they FAILED -- path analysis read them as actions that never work.
//
// The join is on operation_id and nothing else. Joining the two tables by action
// name lies in both directions: ResizeApp showed 24 operations against zero audit
// rows while being fully covered, because the user path is audited as
// UpdateAppProfile and the autoscaler's as AutoscaleApp.
//
// Reported per action rather than as one number so a gap is actionable: the
// count alone cannot say whether an action is unaudited by design or newly
// broken.
//
// @ID          getAuditCoverage
// @Summary     Operations that left no audit row (platform-admin only)
// @Description Returns, per action, how many operations finished in the window and how many of them have a row in audit_events, listing only the actions with a shortfall. Read access follows the rest of the admin dashboards (/platform-admins and /platform-analysts); every other caller gets 403.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Param       days query    int false "Length of the window in days (default 30, max 90)"
// @Success     200 {object} map[string]interface{}
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Router      /admin/audit/coverage [get]
func (h *Handler) GetAuditCoverage(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isAdminReader(claims) {
		respondForbidden(c)
		return
	}

	days := auditCoverageDefaultDays
	if v, err := strconv.Atoi(c.Query("days")); err == nil && v > 0 {
		days = v
	}
	if days > auditCoverageMaxDays {
		days = auditCoverageMaxDays
	}

	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT o.action, count(*), count(a.id)
		  FROM operations o
		  LEFT JOIN LATERAL (
			SELECT id FROM audit_events a WHERE a.operation_id = o.id LIMIT 1
		  ) a ON true
		 WHERE o.created_at >= now() - ($1 * INTERVAL '1 day')
		 GROUP BY o.action
		HAVING count(*) > count(a.id)
		 ORDER BY count(*) - count(a.id) DESC, o.action`, days)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to measure audit coverage")
		return
	}
	defer rows.Close()

	gaps := []auditCoverageGap{}
	totalMissing := 0
	for rows.Next() {
		var g auditCoverageGap
		if err := rows.Scan(&g.Action, &g.Operations, &g.Audited); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan audit coverage")
			return
		}
		g.Missing = g.Operations - g.Audited
		totalMissing += g.Missing
		gaps = append(gaps, g)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to measure audit coverage")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"days":          days,
		"gaps":          gaps,
		"total_missing": totalMissing,
	})
}
