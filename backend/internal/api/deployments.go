package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// deployment mirrors the frontend Deployment shape.
type deployment struct {
	ID            uuid.UUID  `json:"id"`
	EnvironmentID uuid.UUID  `json:"environment_id"`
	AppName       string     `json:"app_name"`
	BuildID       *uuid.UUID `json:"build_id,omitempty"`
	OperationID   *uuid.UUID `json:"operation_id,omitempty"`
	ImageURI      string     `json:"image_uri"`
	Trigger       string     `json:"trigger"`
	IsCurrent     bool       `json:"is_current"`
	CreatedAt     time.Time  `json:"created_at"`
}

const deploymentSelectCols = `id, environment_id, app_name, build_id, operation_id,
		image_uri, trigger, is_current, created_at`

func scanDeployment(s interface {
	Scan(dest ...any) error
}, d *deployment) error {
	return s.Scan(&d.ID, &d.EnvironmentID, &d.AppName, &d.BuildID, &d.OperationID,
		&d.ImageURI, &d.Trigger, &d.IsCurrent, &d.CreatedAt)
}

// ListDeployments returns the deployment history for an app in an environment.
//
// @ID          listDeployments
// @Summary     List deployments for an app
// @Description Returns the deployment history for an app in an environment, most recent first. Read-only.
// @Tags        deployment
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with a deployments array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/deployments [get]
func (h *Handler) ListDeployments(c *gin.Context) {
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
	appName := c.Param("appName")

	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT `+deploymentSelectCols+`
		 FROM deployments
		 WHERE environment_id = $1 AND app_name = $2
		 ORDER BY created_at DESC`,
		envID, appName,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query deployments")
		return
	}
	defer rows.Close()

	deployments := []deployment{}
	for rows.Next() {
		var d deployment
		if err := scanDeployment(rows, &d); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan deployment")
			return
		}
		deployments = append(deployments, d)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading deployments")
		return
	}

	c.JSON(http.StatusOK, gin.H{"deployments": deployments})
}

// RollbackDeployment re-deploys the image of a prior deployment.
//
// @ID          rollbackDeployment
// @Summary     Roll back to a prior deployment
// @Description Re-deploys the immutable image of an earlier deployment. No rebuild — enqueues a DeployImageVersion operation. Asynchronous: returns 202 with the operation. Requires write access.
// @Tags        deployment
// @Produce     json
// @Security    BearerAuth
// @Param       projectId    path     string true "Project UUID"
// @Param       deploymentId path     string true "Deployment UUID"
// @Success     202          {object} map[string]interface{} "object with the accepted operation"
// @Failure     403          {object} map[string]string
// @Failure     404          {object} map[string]string
// @Router      /projects/{projectId}/deployments/{deploymentId}/rollback [post]
func (h *Handler) RollbackDeployment(c *gin.Context) {
	h.redeployFrom(c, "rollback", "Rollback queued")
}

// PromoteDeployment re-deploys a deployment's image (e.g. preview → production).
//
// @ID          promoteDeployment
// @Summary     Promote a deployment
// @Description Re-deploys the immutable image of a deployment (e.g. promoting a preview build). No rebuild — enqueues a DeployImageVersion operation. Asynchronous: returns 202 with the operation. Requires write access.
// @Tags        deployment
// @Produce     json
// @Security    BearerAuth
// @Param       projectId    path     string true "Project UUID"
// @Param       deploymentId path     string true "Deployment UUID"
// @Success     202          {object} map[string]interface{} "object with the accepted operation"
// @Failure     403          {object} map[string]string
// @Failure     404          {object} map[string]string
// @Router      /projects/{projectId}/deployments/{deploymentId}/promote [post]
func (h *Handler) PromoteDeployment(c *gin.Context) {
	h.redeployFrom(c, "promote", "Promotion queued")
}

// redeployFrom is the shared rollback/promote path. It reads the prior deployment's
// immutable image_uri, inserts a new deployments row, enqueues a DeployImageVersion
// operation (copying UpdateAppImage), and links the op id back onto the new row.
func (h *Handler) redeployFrom(c *gin.Context, trigger, message string) {
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
	deploymentID, err := uuid.Parse(c.Param("deploymentId"))
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

	// Read the prior deployment (immutable image_uri), scoped to the project via
	// its environment so existence isn't leaked across tenants.
	var envID uuid.UUID
	var appName, imageURI string
	var buildID *uuid.UUID
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT d.environment_id, d.app_name, d.image_uri, d.build_id
		 FROM deployments d
		 JOIN environments e ON e.id = d.environment_id
		 WHERE d.id = $1 AND e.project_id = $2`,
		deploymentID, projectID,
	).Scan(&envID, &appName, &imageURI, &buildID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load deployment")
		return
	}

	// Insert the new deployment row (not yet current — the op-Ready watcher flips it).
	var newDeployID uuid.UUID
	err = h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO deployments (environment_id, app_name, build_id, image_uri, trigger, deployed_by)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		envID, appName, buildID, imageURI, trigger, claims.UserID,
	).Scan(&newDeployID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record deployment")
		return
	}

	// Enqueue the deploy op (identical to UpdateAppImage).
	payload := models.DeployImageVersionPayload{
		AppName: appName,
		Image:   imageURI,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeployImageVersion', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	// Link the op id back onto the new deployment row.
	_, err = h.pool.Exec(c.Request.Context(),
		`UPDATE deployments SET operation_id = $1 WHERE id = $2`,
		op.ID, newDeployID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to link operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "DeployImageVersion",
		ResourceKind:  "App",
		ResourceName:  appName,
		Metadata:      payload,
	})

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   message,
	})
}
