package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FillRepoFullName injects repo_full_name (from git_repos, keyed by app name)
// into each app's summary when the snapshot summary does not already carry it.
// A deployed app's resource-snapshot summary omits the linked repo, so without
// this the UI shows "not linked" even though the repo is connected in git_repos.
func FillRepoFullName(apps []models.ResourceSnapshot, repoByName map[string]string) {
	for i := range apps {
		repo := repoByName[apps[i].Name]
		if repo == "" {
			continue
		}
		var m map[string]any
		if len(apps[i].SummaryJSON) > 0 {
			_ = json.Unmarshal(apps[i].SummaryJSON, &m)
		}
		if m == nil {
			m = map[string]any{}
		}
		if cur, _ := m["repo_full_name"].(string); cur == "" {
			m["repo_full_name"] = repo
			if b, err := json.Marshal(m); err == nil {
				apps[i].SummaryJSON = b
			}
		}
	}
}

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
		        COALESCE(profile, 'small'), COALESCE(replicas, 1), COALESCE(port, 8080),
		        updated_at
		 FROM git_repos
		 WHERE project_id = $1 AND environment_id = $2`,
		projectID, envID,
	)
	repoByName := make(map[string]string)
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
			if repo != "" {
				repoByName[name] = repo
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

	FillRepoFullName(apps, repoByName)

	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })

	c.JSON(http.StatusOK, gin.H{"apps": apps})
}

// jsFrameworks are the detected framework labels whose serve/dev process listens
// on :5173 by default (mirrors the helm common chart's javascript stack and
// renderer.ChartFor). Kept local so the backend module does not depend on the
// gitops-agent renderer package.
var jsFrameworks = map[string]bool{
	"javascript": true, "web": true, "nextjs": true, "nuxt": true,
	"sveltekit": true, "react": true, "nestjs": true, "express": true,
	"fastify": true, "remix": true, "vite": true, "node": true,
}

// defaultPortForFramework returns the servicePort to assume when a create request
// omits one: 5173 for javascript apps, 8080 otherwise.
func defaultPortForFramework(framework string) int {
	if jsFrameworks[framework] {
		return 5173
	}
	return 8080
}

type createAppRequest struct {
	Name      string        `json:"name"`
	Image     string        `json:"image"`
	Framework string        `json:"framework"`
	Port      int           `json:"port"`
	Replicas  int           `json:"replicas"`
	Profile   string        `json:"profile"`
	Volume    *appVolumeReq `json:"volume,omitempty"`
}

// appVolumeReq is the wire form of a persistent data directory request. Empty
// StorageClass defaults to defaultVolumeStorageClass (a Retain longhorn class).
type appVolumeReq struct {
	Path         string `json:"path"`
	Size         string `json:"size"`
	StorageClass string `json:"storage_class"`
}

// defaultVolumeStorageClass is a ReadWriteMany-capable Longhorn class with
// reclaimPolicy=Retain, so deleting the PVC does not immediately destroy data.
//
// It intentionally defaults to a 2-replica class. beget-prod runs three Longhorn
// nodes with strict replica anti-affinity while chronic disk pressure keeps one
// node below the schedulable floor at any given moment, so a 3-replica volume can
// never reliably place its third replica: the Longhorn Volume reports
// Scheduled=False (ReplicaSchedulingFailure) and refuses to attach, wedging the
// pod in ContainerCreating for the lifetime of the pressure. A 2-replica volume
// fits the two schedulable nodes and attaches cleanly. The 3-replica classes stay
// allowed below so existing volumes can still be resized.
const defaultVolumeStorageClass = "longhorn-dev"

var volumeSizeRe = regexp.MustCompile(`^[1-9][0-9]*(Mi|Gi|Ti)$`)

// allowedVolumeStorageClasses guards against pointing app data at ephemeral
// (reclaimPolicy=Delete) classes. All entries are Retain on beget-prod.
// longhorn-dev is the 2-replica default (see defaultVolumeStorageClass); the
// 3-replica classes remain selectable for back-compat with existing volumes.
var allowedVolumeStorageClasses = map[string]bool{
	"longhorn-dev":           true,
	"longhorn-prod":          true,
	"longhorn-stateful-prod": true,
}

// validateAppVolume normalises and validates a persistent-directory request,
// returning the typed model payload. A nil request yields a nil volume (no PVC).
func validateAppVolume(v *appVolumeReq) (*models.AppVolume, error) {
	if v == nil {
		return nil, nil
	}
	if !strings.HasPrefix(v.Path, "/") || strings.Contains(v.Path, "..") || len(v.Path) < 2 {
		return nil, fmt.Errorf("volume path must be an absolute path without '..'")
	}
	if !volumeSizeRe.MatchString(v.Size) {
		return nil, fmt.Errorf("volume size must be a quantity like 1Gi, 512Mi or 2Ti")
	}
	sc := v.StorageClass
	if sc == "" {
		sc = defaultVolumeStorageClass
	}
	if !allowedVolumeStorageClasses[sc] {
		return nil, fmt.Errorf("storage_class must be one of: longhorn-dev, longhorn-prod, longhorn-stateful-prod")
	}
	return &models.AppVolume{Path: v.Path, Size: v.Size, StorageClass: sc}, nil
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
			req.Port = defaultPortForFramework(req.Framework)
		}
		if req.Replicas == 0 && !claims.IsPlatformAdmin() {
			req.Replicas = 1
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
		minReplicas := 1
		if claims.IsPlatformAdmin() {
			minReplicas = 0
		}
		if req.Replicas < minReplicas || req.Replicas > 10 {
			respondError(c, http.StatusBadRequest, fmt.Sprintf("replicas must be between %d and 10", minReplicas))
			return
		}
		validProfiles := map[string]bool{"small": true, "medium": true, "large": true}
		if !validProfiles[req.Profile] {
			respondError(c, http.StatusBadRequest, "profile must be one of: small, medium, large")
			return
		}
	}

	appVolume, err := validateAppVolume(req.Volume)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if appVolume != nil && isCompose {
		respondError(c, http.StatusBadRequest, "persistent storage is only supported for Kubernetes apps")
		return
	}

	// Uniqueness is scoped to THIS project+environment, not the whole Argo instance.
	// New apps get a project-scoped ArgoCD Application name (App CR spec.argoName =
	// "<app>-<env>-<projhash>", consumed by the tenant-apps ApplicationSet), so the
	// same app name in the same env under two different projects no longer collides
	// into one Application. k8s namespaces are already per project+env, so nothing
	// else clashes either. Guarding globally would be a needless cross-tenant name
	// grab (the S3-bucket-global-namespace surprise). See project_appset_name_collision.
	// PRECONDITION: the argo-infra tenant-apps ApplicationSet must consume
	// spec.argoName before this relaxation ships, else a cross-project duplicate
	// still wedges Argo on the bare name.
	var exists int
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT 1
		 FROM resource_snapshots rs
		 WHERE rs.kind = 'App' AND rs.name = $1
		   AND rs.project_id = $2 AND rs.environment_id = $3
		 LIMIT 1`,
		req.Name, projectID, envID,
	).Scan(&exists)
	if err == nil {
		respondError(c, http.StatusConflict, fmt.Sprintf(
			"the app name %q is already taken in this project's environment; choose another name",
			req.Name))
		return
	} else if err != pgx.ErrNoRows {
		respondError(c, http.StatusInternalServerError, "failed to check name uniqueness")
		return
	}

	var defaultHostname string
	if !isCompose && h.cfg.DefaultDomainEnabled && h.cfg.DefaultDomainBase != "" {
		if suffix, sErr := randomHostSuffix(); sErr == nil {
			defaultHostname = buildDefaultHostname(h.cfg.DefaultDomainBase, req.Name, suffix)
		}
	}

	// Marshal payload
	payload := models.CreateAppPayload{
		Name:            req.Name,
		Image:           req.Image,
		Framework:       req.Framework,
		Port:            req.Port,
		Replicas:        req.Replicas,
		Profile:         req.Profile,
		Volume:          appVolume,
		AppServerName:   appServerName,
		DefaultHostname: defaultHostname,
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

	if defaultHostname != "" {
		_, dhErr := h.pool.Exec(c.Request.Context(),
			`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, operation_id, managed)
			 VALUES (NULL, $1, $2, $3, 'CNAME', 'pending', 'pending', $4, true)`,
			envID, req.Name, defaultHostname, op.ID,
		)
		if dhErr != nil {
			respondError(c, http.StatusInternalServerError, "failed to record default hostname")
			return
		}
	}

	c.JSON(http.StatusAccepted, gin.H{
		"operation":        op,
		"default_hostname": defaultHostname,
		"message":          "App creation queued",
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

// quantityBytes converts a restricted k8s quantity (Mi/Gi/Ti, validated by
// volumeSizeRe) to a byte count for grow-only comparisons. It returns 0 for any
// value that does not match the expected shape.
func quantityBytes(q string) int64 {
	if !volumeSizeRe.MatchString(q) {
		return 0
	}
	unit := q[len(q)-2:]
	num, err := strconv.ParseInt(q[:len(q)-2], 10, 64)
	if err != nil {
		return 0
	}
	switch unit {
	case "Mi":
		return num * 1024 * 1024
	case "Gi":
		return num * 1024 * 1024 * 1024
	case "Ti":
		return num * 1024 * 1024 * 1024 * 1024
	}
	return 0
}

// UpdateAppStorage attaches or resizes the persistent data directory of a Helm
// app. A PersistentVolumeClaim is immutable once created, so an existing volume
// may only grow and may not change its storage class; the mount path may change.
//
// @ID          updateAppStorage
// @Summary     Attach or resize app persistent storage
// @Description Attaches a ReadWriteMany persistent data directory to a Kubernetes app, or resizes an existing one (grow-only; storage class is fixed once created). Asynchronous: returns 202 with an operation; poll it until terminal.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string       true "Project UUID"
// @Param       envId     path     string       true "Environment UUID"
// @Param       appName   path     string       true "App name"
// @Param       body      body     appVolumeReq true "Persistent storage specification"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/storage [put]
func (h *Handler) UpdateAppStorage(c *gin.Context) {
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

	var req appVolumeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	vol, err := validateAppVolume(&req)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	var summaryRaw []byte
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&summaryRaw)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load app")
		return
	}

	var cur struct {
		Volume *models.AppVolume `json:"volume"`
	}
	_ = json.Unmarshal(summaryRaw, &cur)
	if cur.Volume != nil {
		if cur.Volume.StorageClass != "" && cur.Volume.StorageClass != vol.StorageClass {
			respondError(c, http.StatusBadRequest, "storage class cannot be changed after the volume is created")
			return
		}
		if quantityBytes(vol.Size) < quantityBytes(cur.Volume.Size) {
			respondError(c, http.StatusBadRequest, "storage can only be expanded, not shrunk")
			return
		}
	}

	payload := models.UpdateAppStoragePayload{AppName: appName, Volume: *vol}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'UpdateAppStorage', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	auditMeta, _ := json.Marshal(payload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'UpdateAppStorage', 'App', $4, $5)`,
		claims.UserID, projectID, op.ID, appName, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "Storage update queued",
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
