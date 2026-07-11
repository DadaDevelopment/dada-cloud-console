package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListApps returns all App resources in a project environment.
//
// @ID          listApps
// @Summary     List apps in an environment
// @Description Returns all App resources (Helm or compose) in a project environment, with their live phase/status. Read-only.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Success     200       {object} map[string]interface{} "object with an apps array of ResourceSnapshot"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps [get]
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

	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
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

	seen := make(map[string]struct{}, len(apps))
	for _, a := range apps {
		seen[a.Name] = struct{}{}
	}
	grows, gerr := h.pool.Query(c.Request.Context(),
		`SELECT id, app_name, repo_full_name,
		        COALESCE(profile, 'small'), COALESCE(replicas, 2), COALESCE(port, 8080),
		        updated_at
		 FROM git_repos
		 WHERE project_id = $1 AND environment_id = $2`,
		projectID, envID,
	)
	if gerr == nil {
		defer grows.Close()
		for grows.Next() {
			var (
				id       uuid.UUID
				name     string
				repo     string
				profile  string
				replicas int
				port     int
				updated  time.Time
			)
			if scanErr := grows.Scan(&id, &name, &repo, &profile, &replicas, &port, &updated); scanErr != nil {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			summary, _ := json.Marshal(map[string]any{
				"image":          repo,
				"profile":        profile,
				"replicas":       replicas,
				"port":           port,
				"repo_full_name": repo,
				"source":         "git",
			})
			envRef := envID
			apps = append(apps, models.ResourceSnapshot{
				ID:            id,
				ProjectID:     projectID,
				EnvironmentID: &envRef,
				Kind:          "App",
				Name:          name,
				Phase:         "NotDeployed",
				SummaryJSON:   summary,
				LastSyncedAt:  updated,
			})
			seen[name] = struct{}{}
		}
	}

	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })

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
//
// @ID          createApp
// @Summary     Deploy a new app
// @Description Provisions a new app in an environment. For Kubernetes (Helm) environments image is required and port/replicas/profile apply; for VM (compose) environments the app deploys as a Docker Compose stack onto the environment's app server, which must already be Ready. Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string           true "Project UUID"
// @Param       envId     path     string           true "Environment UUID"
// @Param       body      body     createAppRequest true "App specification"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps [post]
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

	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		if qErr := h.checkQuota(c.Request.Context(), orgID, "apps"); qErr != nil {
			if qe, ok := qErr.(*quotaExceededError); ok {
				respondQuotaExceeded(c, qe.Resource, qe.Limit)
				return
			}
		}
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
	isCompose := runtime == models.EnvironmentRuntimeVM

	// For VM environments, the app deploys as a Docker Compose stack onto the
	// environment's AppServer. That server must exist and be Ready.
	var appServerName string
	if isCompose {
		var status string
		err = h.pool.QueryRow(c.Request.Context(),
			`SELECT s.name, s.status
			 FROM environments e JOIN app_servers s ON s.id = e.app_server_id
			 WHERE e.id = $1 AND e.project_id = $2`,
			envID, projectID,
		).Scan(&appServerName, &status)
		if err == pgx.ErrNoRows {
			respondError(c, http.StatusConflict, "this VM environment has no AppServer attached; create or attach one first")
			return
		}
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to load environment AppServer")
			return
		}
		if status != string(models.AppServerStatusReady) {
			respondError(c, http.StatusConflict, "the environment's AppServer is not Ready yet")
			return
		}
	}

	var req createAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	// Validate name (common to both runtimes).
	if req.Name == "" {
		respondError(c, http.StatusBadRequest, "name is required")
		return
	}
	if err := validateKubeName(req.Name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if !isCompose {
		// Helm app validation + defaults.
		if req.Port == 0 {
			req.Port = 8080
		}
		if req.Replicas == 0 {
			req.Replicas = 2
		}
		if req.Profile == "" {
			req.Profile = "small"
		}
		if req.Image == "" {
			respondError(c, http.StatusBadRequest, "image is required")
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
		Name:          req.Name,
		Image:         req.Image,
		Port:          req.Port,
		Replicas:      req.Replicas,
		Profile:       req.Profile,
		AppServerName: appServerName, // empty for Helm apps
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
//
// @ID          updateAppImage
// @Summary     Deploy a new image version for an app
// @Description Rolls an existing app to a new container image. Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                true "Project UUID"
// @Param       envId     path     string                true "Environment UUID"
// @Param       appName   path     string                true "App name"
// @Param       body      body     updateAppImageRequest true "New image reference"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/image [patch]
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

// RollbackApp enqueues a RollbackStack operation that reverts a compose app's
// compose.yaml to its previous committed version and redeploys — the VM-runtime
// "Rollback" action (ADR-013 §8.3). Git-native + data-safe (the external PG
// volume pin is in every version). No body.
//
// @ID          rollbackApp
// @Summary     Roll a compose app back to its previous version
// @Description Reverts the app's compose.yaml to the previous committed version and redeploys the stack. Compose (VM) apps only. Asynchronous: returns 202 with an operation; poll until terminal.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/rollback [post]
func (h *Handler) RollbackApp(c *gin.Context) {
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

	var count int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}

	payloadBytes, _ := json.Marshal(map[string]string{"app_name": appName})

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'RollbackStack', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	auditMeta, _ := json.Marshal(map[string]string{"app_name": appName})
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'RollbackStack', 'App', $4, $5)`,
		claims.UserID, projectID, op.ID, appName, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "rollback queued"})
}

// AdoptApp enqueues an AdoptComposeStack operation that splits an existing
// single compose App into N first-class per-service Applications, preserving the
// live stack byte-faithfully (verbatim service blocks + the external-volume name
// mapping). Reusable "adopt an existing compose" action; the postgres external
// volume survives the stack swap so prod data is preserved (brief cutover
// outage). Compose (VM) apps only. No body — the path app IS the source.
//
// @ID          adoptApp
// @Summary     Adopt a compose app into per-service Applications
// @Description Splits an existing single compose App into one first-class Application per service, reproducing the live stack (verbatim blocks + preserved external volumes) and redeploying it. Compose (VM) apps only. Asynchronous: returns 202 with an operation; poll until terminal.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "Source app name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/adopt [post]
func (h *Handler) AdoptApp(c *gin.Context) {
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

	var count int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}

	payloadBytes, _ := json.Marshal(map[string]string{"source_app": appName})

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'AdoptComposeStack', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	auditMeta, _ := json.Marshal(map[string]string{"source_app": appName})
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'AdoptComposeStack', 'App', $4, $5)`,
		claims.UserID, projectID, op.ID, appName, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "adopt queued"})
}

// RestartApp enqueues a RestartStack operation that recreates a compose app's
// containers from the current git compose (no image pull) — the VM-runtime
// "Restart" action (ADR-013 §8.3). No body.
//
// @ID          restartApp
// @Summary     Restart a compose app
// @Description Recreates the compose app's containers from the current compose.yaml without pulling new images or touching volumes. Compose (VM) apps only. Asynchronous: returns 202 with an operation.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/restart [post]
func (h *Handler) RestartApp(c *gin.Context) {
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

	var count int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}

	payloadBytes, _ := json.Marshal(map[string]string{"app_name": appName})

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'RestartStack', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	auditMeta, _ := json.Marshal(map[string]string{"app_name": appName})
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'RestartStack', 'App', $4, $5)`,
		claims.UserID, projectID, op.ID, appName, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "restart queued"})
}
