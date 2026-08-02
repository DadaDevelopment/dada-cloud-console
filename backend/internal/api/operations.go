package api

import (
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// scanOperation scans a row into an Operation, handling nullable string columns safely.
func scanOperation(scanner interface {
	Scan(dest ...any) error
}, op *models.Operation) error {
	var gitCommit, gitPath, argoApp, errorCode, errorMessage *string
	var envID *uuid.UUID
	err := scanner.Scan(
		&op.ID, &op.ActorID, &op.ProjectID, &envID,
		&op.Action, &op.ResourceKind, &op.ResourceName,
		&op.Status, &op.Payload, &op.ValidationResult,
		&gitCommit, &gitPath, &argoApp,
		&errorCode, &errorMessage, &op.CreatedAt, &op.UpdatedAt,
	)
	if err != nil {
		return err
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
	return nil
}

// GetOperation returns the current state of an async platform operation.
//
// @ID          getOperation
// @Summary     Get the state of an async operation
// @Description Returns the current state (status, error, git/argo metadata) of a single async operation. Read-only. Poll this after any create/update/delete call until the operation reaches a terminal status (Ready or Failed).
// @Tags        operation
// @Produce     json
// @Security    BearerAuth
// @Param       projectId   path     string true "Project UUID"
// @Param       operationId path     string true "Operation UUID"
// @Success     200         {object} map[string]interface{} "object with the operation"
// @Failure     401         {object} map[string]string
// @Failure     404         {object} map[string]string
// @Router      /projects/{projectId}/operations/{operationId} [get]
func (h *Handler) GetOperation(c *gin.Context) {
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
	operationID, err := uuid.Parse(c.Param("operationId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	// Verify project membership (404 to avoid enumeration)
	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`SELECT id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		        status, payload, validation_result, git_commit, git_path, argo_application,
		        error_code, error_message, created_at, updated_at
		 FROM operations WHERE id = $1 AND project_id = $2`,
		operationID, projectID,
	)
	if err = scanOperation(row, &op); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to fetch operation")
		return
	}

	c.JSON(http.StatusOK, gin.H{"operation": op})
}

// RetryOperation re-queues a failed operation for another processing attempt.
//
// @ID          retryOperation
// @Summary     Retry a failed operation
// @Description Re-queues a failed operation for another processing attempt (resets it to Created and clears the prior error). Only operations in the Failed state can be retried. Asynchronous: returns 202 with the re-queued operation; poll it until terminal.
// @Tags        operation
// @Produce     json
// @Security    BearerAuth
// @Param       projectId   path     string true "Project UUID"
// @Param       operationId path     string true "Operation UUID"
// @Success     202         {object} map[string]interface{} "object with the re-queued operation"
// @Failure     401         {object} map[string]string
// @Failure     403         {object} map[string]string
// @Failure     404         {object} map[string]string
// @Failure     409         {object} map[string]string
// @Router      /projects/{projectId}/operations/{operationId}/retry [post]
func (h *Handler) RetryOperation(c *gin.Context) {
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
	operationID, err := uuid.Parse(c.Param("operationId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	// Filled once the operation row is read: a retry belongs to the environment
	// the operation targeted, and without it the row cannot be lined up with the
	// deploy it was retrying.
	var envID uuid.UUID
	audit := func(outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			OperationID:   operationID,
			Action:        "RetryOperation",
			ResourceKind:  "Operation",
			ResourceName:  operationID.String(),
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	reject := func(status int, reason string) {
		audit(auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
	}

	// Verify project membership
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		reject(http.StatusNotFound, "not_a_member")
		respondNotFound(c)
		return
	}
	if err != nil {
		reject(http.StatusInternalServerError, "membership_check_failed")
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		reject(http.StatusForbidden, "read_only_role")
		respondForbidden(c)
		return
	}

	// Fetch current status
	var currentStatus models.OperationStatus
	var opEnvID *uuid.UUID
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT status, environment_id FROM operations WHERE id = $1 AND project_id = $2`,
		operationID, projectID,
	).Scan(&currentStatus, &opEnvID)
	if opEnvID != nil {
		envID = *opEnvID
	}
	if err == pgx.ErrNoRows {
		reject(http.StatusNotFound, "not_found")
		respondNotFound(c)
		return
	}
	if err != nil {
		reject(http.StatusInternalServerError, "lookup_failed")
		respondError(c, http.StatusInternalServerError, "failed to fetch operation")
		return
	}

	if currentStatus != models.OperationStatusFailed {
		audit(auditOutcomeFailure, map[string]any{
			"reason": "not_failed", "status": http.StatusConflict, "current_status": string(currentStatus),
		})
		respondError(c, http.StatusConflict, "only failed operations can be retried")
		return
	}

	// Reset to Created — the status both agents claim (gitops + portainer).
	// 'Queued' was a dead status nothing claimed, so retries never re-ran.
	var op models.Operation
	retryRow := h.pool.QueryRow(c.Request.Context(),
		`UPDATE operations
		 SET status = 'Created', error_code = NULL, error_message = NULL, updated_at = NOW()
		 WHERE id = $1 AND project_id = $2
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		operationID, projectID,
	)
	if err = scanOperation(retryRow, &op); err != nil {
		reject(http.StatusInternalServerError, "retry_update_failed")
		respondError(c, http.StatusInternalServerError, "failed to retry operation")
		return
	}

	audit(auditOutcomeSuccess, map[string]any{
		"retried_action": op.Action,
		"resource_kind":  op.ResourceKind,
		"resource_name":  op.ResourceName,
	})

	c.JSON(http.StatusAccepted, gin.H{"operation": op})
}
