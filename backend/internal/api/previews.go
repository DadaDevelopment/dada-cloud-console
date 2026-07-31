package api

import (
	"encoding/json"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeletePreviewEnvironment enqueues a DeletePreviewEnv operation for an
// ephemeral (PR) environment. Only ephemeral environments can be torn down
// through this route; non-ephemeral environments 404 to avoid an
// accidental/adversarial delete of a durable env via the preview path.
//
// @ID          deletePreviewEnvironment
// @Summary     Delete a preview environment
// @Description Tears down an ephemeral (PR) environment: queues a DeletePreviewEnv operation that removes the namespace and its git-rendered manifests. Returns 404 if the environment is not ephemeral. Requires write access.
// @Tags        environment
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/preview [delete]
func (h *Handler) DeletePreviewEnvironment(c *gin.Context) {
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
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
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

	nsAudit := ""
	reject := func(status int, reason string, respond func()) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "DeletePreviewEnv",
			ResourceKind:  "Environment",
			ResourceName:  nsAudit,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
		respond()
	}

	var namespace string
	var isEphemeral bool
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT namespace, is_ephemeral FROM environments WHERE id = $1 AND project_id = $2`,
		envID, projectID,
	).Scan(&namespace, &isEphemeral)
	if err == pgx.ErrNoRows {
		reject(http.StatusNotFound, "environment_not_found", func() { respondNotFound(c) })
		return
	}
	if err != nil {
		reject(http.StatusInternalServerError, "environment_load_failed", func() {
			respondError(c, http.StatusInternalServerError, "failed to load environment")
		})
		return
	}
	nsAudit = namespace
	if !isEphemeral {
		reject(http.StatusNotFound, "not_ephemeral", func() { respondNotFound(c) })
		return
	}

	payload := models.DeletePreviewEnvPayload{
		EnvironmentID: envID.String(),
		Namespace:     namespace,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		reject(http.StatusInternalServerError, "marshal_failed", func() {
			respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		})
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeletePreviewEnv', 'Environment', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, namespace, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		reject(http.StatusInternalServerError, "operation_insert_failed", func() {
			respondError(c, http.StatusInternalServerError, "failed to create operation")
		})
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "DeletePreviewEnv",
		ResourceKind:  "Environment",
		ResourceName:  namespace,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"namespace": namespace},
	})
	h.notifyAuditEvent(claims, projectID, "DeletePreviewEnv", namespace)

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "preview environment teardown queued"})
}
