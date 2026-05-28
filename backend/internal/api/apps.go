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

func canCreateAppsInRuntime(runtime models.EnvironmentRuntime) bool {
	return runtime == "" || runtime == models.EnvironmentRuntimeK8s
}

// ListApps returns all App resources in a project environment.
func (h *Handler) ListApps(c *gin.Context) {
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

	_, err = h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, environment_id, kind, name, phase, summary_json, last_synced_at
		 FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App'
		 ORDER BY name`,
		projectID, envID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query apps")
		return
	}
	defer rows.Close()

	var apps []models.ResourceSnapshot
	for rows.Next() {
		var rs models.ResourceSnapshot
		if err := rows.Scan(
			&rs.ID, &rs.ProjectID, &rs.EnvironmentID, &rs.Kind, &rs.Name,
			&rs.Phase, &rs.SummaryJSON, &rs.LastSyncedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan app")
			return
		}
		apps = append(apps, rs)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading apps")
		return
	}
	if apps == nil {
		apps = []models.ResourceSnapshot{}
	}

	c.JSON(http.StatusOK, gin.H{"apps": apps})
}

type createAppRequest struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	Port     int    `json:"port"`
	Replicas int    `json:"replicas"`
	Profile  string `json:"profile"`
}

// CreateApp enqueues an operation to provision a new App CRD.
func (h *Handler) CreateApp(c *gin.Context) {
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

	role, err := h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID)
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

	var runtime models.EnvironmentRuntime
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT runtime FROM environments WHERE id = $1 AND project_id = $2`,
		envID, projectID,
	).Scan(&runtime); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load environment runtime")
		return
	}
	if !canCreateAppsInRuntime(runtime) {
		respondError(c, http.StatusConflict, "VM application deployments are not wired in the console yet; create the AppServer first and deploy through the VM track")
		return
	}

	var req createAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	// Apply defaults
	if req.Port == 0 {
		req.Port = 8080
	}
	if req.Replicas == 0 {
		req.Replicas = 2
	}
	if req.Profile == "" {
		req.Profile = "small"
	}

	// Validate
	if req.Name == "" {
		respondError(c, http.StatusBadRequest, "name is required")
		return
	}
	if req.Image == "" {
		respondError(c, http.StatusBadRequest, "image is required")
		return
	}
	if err := validateKubeName(req.Name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateImage(req.Image); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		respondError(c, http.StatusBadRequest, "port must be between 1 and 65535")
		return
	}
	if req.Replicas < 1 || req.Replicas > 10 {
		respondError(c, http.StatusBadRequest, "replicas must be between 1 and 10")
		return
	}
	validProfiles := map[string]bool{"small": true, "medium": true, "large": true}
	if !validProfiles[req.Profile] {
		respondError(c, http.StatusBadRequest, "profile must be one of: small, medium, large")
		return
	}

	// Check name uniqueness
	var existing int
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, req.Name,
	).Scan(&existing)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check name uniqueness")
		return
	}
	if existing > 0 {
		respondError(c, http.StatusConflict, "an app with that name already exists in this environment")
		return
	}

	// Marshal payload
	payload := models.CreateAppPayload{
		Name:     req.Name,
		Image:    req.Image,
		Port:     req.Port,
		Replicas: req.Replicas,
		Profile:  req.Profile,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	// Insert Operation
	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreateApp', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, req.Name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	// Insert AuditEvent (best-effort)
	auditMeta, _ := json.Marshal(payload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'CreateApp', 'App', $4, $5)`,
		claims.UserID, projectID, op.ID, req.Name, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "App creation queued",
	})
}

type updateAppImageRequest struct {
	Image string `json:"image"`
}

// UpdateAppImage enqueues an operation to deploy a new image version for an App.
func (h *Handler) UpdateAppImage(c *gin.Context) {
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

	role, err := h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID)
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

	var req updateAppImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if req.Image == "" {
		respondError(c, http.StatusBadRequest, "image is required")
		return
	}
	if err := ValidateImage(req.Image); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	// Verify app exists
	var count int
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}

	// Marshal payload
	payload := models.DeployImageVersionPayload{
		AppName: appName,
		Image:   req.Image,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	// Insert Operation
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

	// Insert AuditEvent (best-effort)
	auditMeta, _ := json.Marshal(payload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'DeployImageVersion', 'App', $4, $5)`,
		claims.UserID, projectID, op.ID, appName, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "Image update queued",
	})
}
