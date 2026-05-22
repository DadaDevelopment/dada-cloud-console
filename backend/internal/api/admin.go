package api

import (
	"errors"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListAdminApprovals returns operations in WaitingForApproval scoped to projects
// where the caller holds platform-admin. AI Studio's GPU gate is the first
// consumer; v2 dangerous-action features inherit the same UI.
func (h *Handler) ListAdminApprovals(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT o.id, o.actor_id, o.project_id, o.environment_id, o.action, o.resource_kind, o.resource_name,
		       o.status, o.payload, o.validation_result, o.git_commit, o.git_path, o.argo_application,
		       o.error_code, o.error_message, o.created_at, o.updated_at,
		       p.name, COALESCE(u.display_name, u.username)
		FROM operations o
		JOIN project_members pm ON pm.project_id = o.project_id
		JOIN projects p          ON p.id = o.project_id
		LEFT JOIN users u        ON u.id = o.actor_id
		WHERE o.status = $1
		  AND pm.user_id = $2
		  AND pm.role = $3
		ORDER BY o.created_at ASC`,
		models.OperationStatusWaitingForApproval, claims.UserID, models.MemberRolePlatformAdmin,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list approvals")
		return
	}
	defer rows.Close()

	type approvalRow struct {
		Operation     models.Operation `json:"operation"`
		ProjectName   string           `json:"project_name"`
		RequestedBy   string           `json:"requested_by"`
	}

	out := []approvalRow{}
	for rows.Next() {
		var op models.Operation
		var projectName string
		var requestedBy *string
		var gitCommit, gitPath, argoApp, errorCode, errorMessage *string
		var envID *uuid.UUID
		if err := rows.Scan(
			&op.ID, &op.ActorID, &op.ProjectID, &envID,
			&op.Action, &op.ResourceKind, &op.ResourceName,
			&op.Status, &op.Payload, &op.ValidationResult,
			&gitCommit, &gitPath, &argoApp,
			&errorCode, &errorMessage, &op.CreatedAt, &op.UpdatedAt,
			&projectName, &requestedBy,
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
		out = append(out, approvalRow{Operation: op, ProjectName: projectName, RequestedBy: rb})
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

	role, err := h.getUserProjectRole(c.Request.Context(), claims.UserID, op.ProjectID)
	if errors.Is(err, pgx.ErrNoRows) || role != models.MemberRolePlatformAdmin {
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

	c.JSON(http.StatusOK, gin.H{"operation": updated})
}

// ApproveOperation transitions a WaitingForApproval operation to Created so the
// gitops-agent dispatcher picks it up on the next poll.
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
