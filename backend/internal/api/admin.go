package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListAdminApprovals returns operations in WaitingForApproval scoped to projects
// where the caller holds platform-admin. AI Studio's GPU gate is the first
// consumer; v2 dangerous-action features inherit the same UI.
//
// @ID          listAdminApprovals
// @Summary     List operations awaiting admin approval
// @Description Returns operations in the WaitingForApproval state across all projects where the caller is a platform-admin, each with the project name and requester. Read-only. The GPU-model creation gate is the first consumer.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{} "object with an approvals array"
// @Failure     401 {object} map[string]string
// @Router      /admin/operations [get]
func (h *Handler) ListAdminApprovals(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	// Scope is derived purely from native claims (ADR-009): the projects where the
	// caller holds an explicit Owner/Admin role, plus every project owned by an
	// org where the caller is org Owner/Admin (cascade). God (/platform-admins)
	// sees every project. Multi-org: a caller may administer many orgs at once.
	god := isGod(claims)
	var adminProjectIDs []uuid.UUID
	for pid, role := range claims.ProjectRoles() {
		if isOrgAdmin(models.MemberRole(role)) {
			if id, perr := uuid.Parse(pid); perr == nil {
				adminProjectIDs = append(adminProjectIDs, id)
			}
		}
	}
	adminOrgs := adminOrgIDs(claims)

	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT o.id, o.actor_id, o.project_id, o.environment_id, o.action, o.resource_kind, o.resource_name,
		       o.status, o.payload, o.validation_result, o.git_commit, o.git_path, o.argo_application,
		       o.error_code, o.error_message, o.created_at, o.updated_at,
		       p.name, COALESCE(p.display_name, ''), COALESCE(u.display_name, u.username)
		FROM operations o
		JOIN projects p   ON p.id = o.project_id
		LEFT JOIN users u ON u.id = o.actor_id
		WHERE o.status = $1
		  AND ( $2
		        OR p.id = ANY($3)
		        OR p.org_id = ANY($4) )
		ORDER BY o.created_at ASC`,
		models.OperationStatusWaitingForApproval, god, adminProjectIDs, adminOrgs,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list approvals")
		return
	}
	defer rows.Close()

	type approvalRow struct {
		Operation          models.Operation `json:"operation"`
		ProjectName        string           `json:"project_name"`
		ProjectDisplayName string           `json:"project_display_name"`
		RequestedBy        string           `json:"requested_by"`
	}

	out := []approvalRow{}
	for rows.Next() {
		var op models.Operation
		var projectName, projectDisplayName string
		var requestedBy *string
		var gitCommit, gitPath, argoApp, errorCode, errorMessage *string
		var envID *uuid.UUID
		if err := rows.Scan(
			&op.ID, &op.ActorID, &op.ProjectID, &envID,
			&op.Action, &op.ResourceKind, &op.ResourceName,
			&op.Status, &op.Payload, &op.ValidationResult,
			&gitCommit, &gitPath, &argoApp,
			&errorCode, &errorMessage, &op.CreatedAt, &op.UpdatedAt,
			&projectName, &projectDisplayName, &requestedBy,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan approval")
			return
		}
		op.EnvironmentID = envID
		if gitCommit != nil {
			op.GitCommit = *gitCommit
		}
		if gitPath != nil {
			op.GitPath = *gitPath
		}
		if argoApp != nil {
			op.ArgoApplication = *argoApp
		}
		if errorCode != nil {
			op.ErrorCode = *errorCode
		}
		if errorMessage != nil {
			op.ErrorMessage = *errorMessage
		}
		rb := ""
		if requestedBy != nil {
			rb = *requestedBy
		}
		out = append(out, approvalRow{Operation: op, ProjectName: projectName, ProjectDisplayName: projectDisplayName, RequestedBy: rb})
	}

	c.JSON(http.StatusOK, gin.H{"approvals": out})
}

// approvalDecision is the shared transition logic for approve / reject.
// Verifies the caller is platform-admin in the operation's project and that
// the operation is still in WaitingForApproval. Records who decided + why.
func (h *Handler) approvalDecision(c *gin.Context, target models.OperationStatus, reason string) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	opID, err := uuid.Parse(c.Param("opId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`SELECT id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		        status, payload, validation_result, git_commit, git_path, argo_application,
		        error_code, error_message, created_at, updated_at
		 FROM operations WHERE id = $1`,
		opID,
	)
	if err := scanOperation(row, &op); errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to fetch operation")
		return
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, op.ProjectID)
	if errors.Is(err, pgx.ErrNoRows) || !isOrgAdmin(role) {
		respondForbidden(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	if op.Status != models.OperationStatusWaitingForApproval {
		respondError(c, http.StatusConflict, "operation is not awaiting approval")
		return
	}

	// Carry the decision context in error_message; for approve it stays empty
	// unless the admin supplied an explanatory note.
	var updated models.Operation
	updateRow := h.pool.QueryRow(c.Request.Context(),
		`UPDATE operations
		 SET status = $1,
		     error_message = NULLIF($2, ''),
		     updated_at = NOW()
		 WHERE id = $3 AND status = $4
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		target, reason, opID, models.OperationStatusWaitingForApproval,
	)
	if err := scanOperation(updateRow, &updated); errors.Is(err, pgx.ErrNoRows) {
		// Lost the race — someone else decided first.
		respondError(c, http.StatusConflict, "operation already decided")
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to update operation")
		return
	}

	// Immutable audit row referencing both the admin (actor_id) and the
	// original requester (metadata.requested_by). D17 / S6 require this.
	auditMeta, _ := json.Marshal(map[string]any{
		"decision":     string(target),
		"reason":       reason,
		"requested_by": op.ActorID,
	})
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		claims.UserID, op.ProjectID, op.ID, "ApprovalDecision",
		op.ResourceKind, op.ResourceName, auditMeta,
	)

	c.JSON(http.StatusOK, gin.H{"operation": updated})
}

// ApproveOperation transitions a WaitingForApproval operation to Created so the
// gitops-agent dispatcher picks it up on the next poll.
//
// @ID          approveOperation
// @Summary     Approve an operation awaiting approval
// @Description Approves a WaitingForApproval operation, transitioning it to Created so the dispatcher executes it. Platform-admin only. An optional note is recorded in the audit trail.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       opId path     string                 true  "Operation UUID"
// @Param       body body     map[string]interface{} false "Optional object with a note string"
// @Success     200  {object} map[string]interface{} "object with the updated operation"
// @Failure     401  {object} map[string]string
// @Failure     403  {object} map[string]string
// @Failure     404  {object} map[string]string
// @Failure     409  {object} map[string]string
// @Router      /admin/operations/{opId}/approve [post]
func (h *Handler) ApproveOperation(c *gin.Context) {
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body)
	h.approvalDecision(c, models.OperationStatusCreated, body.Note)
}

// RejectOperation transitions a WaitingForApproval operation to Cancelled with
// the supplied reason. The reason is required so the requester gets a useful
// error message in the operations timeline.
//
// @ID          rejectOperation
// @Summary     Reject an operation awaiting approval
// @Description Rejects a WaitingForApproval operation, transitioning it to Cancelled. Platform-admin only. A reason is required and surfaced to the requester in the operations timeline.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       opId path     string                 true "Operation UUID"
// @Param       body body     map[string]interface{} true "Object with a required reason string"
// @Success     200  {object} map[string]interface{} "object with the updated operation"
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Failure     403  {object} map[string]string
// @Failure     404  {object} map[string]string
// @Failure     409  {object} map[string]string
// @Router      /admin/operations/{opId}/reject [post]
func (h *Handler) RejectOperation(c *gin.Context) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Reason == "" {
		respondError(c, http.StatusBadRequest, "reason is required")
		return
	}
	h.approvalDecision(c, models.OperationStatusCancelled, body.Reason)
}

// auditEventRow is one row of the god-admin audit dashboard: an audit_events
// record enriched with the actor's email and the project's display name so
// the frontend never has to make a second round trip.
type auditEventRow struct {
	ID           uuid.UUID  `json:"id"`
	CreatedAt    time.Time  `json:"created_at"`
	ActorEmail   string     `json:"actor_email"`
	Action       string     `json:"action"`
	ResourceKind string     `json:"resource_kind"`
	ResourceName string     `json:"resource_name"`
	ProjectID    *uuid.UUID `json:"project_id,omitempty"`
	ProjectName  string     `json:"project_name"`
	ProjectSlug  string     `json:"project_slug,omitempty"`
}

const (
	auditEventsDefaultLimit = 50
	auditEventsMaxLimit     = 200
)

// ListAuditEvents returns a paginated, filterable view of audit_events for the
// god-admin dashboard. Platform-admin only (/orgs/*/... membership does not
// grant access — this is the hidden staff group, ADR-009 §4).
//
// @ID          listAuditEvents
// @Summary     List audit events (platform-admin only)
// @Description Returns audit_events rows joined with the actor's email and the project's display name, newest first. Filters by exact action and a case-insensitive email substring. Platform-admin only (/platform-admins group); every other caller gets 403.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Param       action query    string false "Exact audit_events.action value"
// @Param       user   query    string false "Case-insensitive substring match on the actor's email"
// @Param       limit  query    int    false "Max rows to return (default 50, max 200)"
// @Param       offset query    int    false "Rows to skip"
// @Success     200 {object} map[string]interface{} "object with an events array and a total count"
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Router      /admin/audit [get]
func (h *Handler) ListAuditEvents(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
		return
	}

	action := strings.TrimSpace(c.Query("action"))
	userSubstr := strings.TrimSpace(c.Query("user"))

	limit := auditEventsDefaultLimit
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > auditEventsMaxLimit {
		limit = auditEventsMaxLimit
	}
	offset := 0
	if v, err := strconv.Atoi(c.Query("offset")); err == nil && v >= 0 {
		offset = v
	}

	var actionFilter, userFilter *string
	if action != "" {
		actionFilter = &action
	}
	if userSubstr != "" {
		userFilter = &userSubstr
	}

	var total int
	if err := h.pool.QueryRow(c.Request.Context(), `
		SELECT count(*)
		FROM audit_events a
		JOIN users u ON u.id = a.actor_id
		WHERE ($1::text IS NULL OR a.action = $1)
		  AND ($2::text IS NULL OR u.email ILIKE '%' || $2 || '%')`,
		actionFilter, userFilter,
	).Scan(&total); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to count audit events")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT a.id, a.created_at, u.email, a.action, a.resource_kind, a.resource_name,
		       p.id, COALESCE(p.display_name, ''), COALESCE(p.name, '')
		FROM audit_events a
		JOIN users u        ON u.id = a.actor_id
		LEFT JOIN projects p ON p.id = a.project_id
		WHERE ($1::text IS NULL OR a.action = $1)
		  AND ($2::text IS NULL OR u.email ILIKE '%' || $2 || '%')
		ORDER BY a.created_at DESC
		LIMIT $3 OFFSET $4`,
		actionFilter, userFilter, limit, offset,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list audit events")
		return
	}
	defer rows.Close()

	out := []auditEventRow{}
	for rows.Next() {
		var e auditEventRow
		var resourceKind, resourceName *string
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.ActorEmail, &e.Action, &resourceKind, &resourceName, &e.ProjectID, &e.ProjectName, &e.ProjectSlug); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan audit event")
			return
		}
		if resourceKind != nil {
			e.ResourceKind = *resourceKind
		}
		if resourceName != nil {
			e.ResourceName = *resourceName
		}
		out = append(out, e)
	}

	c.JSON(http.StatusOK, gin.H{"events": out, "total": total, "limit": limit, "offset": offset})
}
