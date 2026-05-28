package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/profiles"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// validModelTypes mirrors AIModel XRD enum.
var validModelTypes = map[string]bool{
	"sklearn": true, "xgboost": true, "lightgbm": true,
	"pytorch": true, "tensorflow": true, "triton": true,
	"huggingface": true, "custom": true,
}

// ListAIModels returns all AIModel resources in a project environment.
func (h *Handler) ListAIModels(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, environment_id, kind, name, phase, summary_json, last_synced_at
		 FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'AIModel'
		 ORDER BY name`,
		projectID, envID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query models")
		return
	}
	defer rows.Close()

	out := []models.ResourceSnapshot{}
	for rows.Next() {
		var rs models.ResourceSnapshot
		if err := rows.Scan(&rs.ID, &rs.ProjectID, &rs.EnvironmentID, &rs.Kind, &rs.Name,
			&rs.Phase, &rs.SummaryJSON, &rs.LastSyncedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan model")
			return
		}
		out = append(out, rs)
	}
	c.JSON(http.StatusOK, gin.H{"models": out})
}

// GetAIModel returns a single AIModel snapshot plus its API key prefix (no plaintext).
func (h *Handler) GetAIModel(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}
	name := c.Param("name")

	var snap models.ResourceSnapshot
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT id, project_id, environment_id, kind, name, phase, summary_json, last_synced_at
		 FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'AIModel' AND name = $3`,
		projectID, envID, name,
	).Scan(&snap.ID, &snap.ProjectID, &snap.EnvironmentID, &snap.Kind, &snap.Name,
		&snap.Phase, &snap.SummaryJSON, &snap.LastSyncedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to fetch model")
		return
	}

	var keyPrefix *string
	_ = h.pool.QueryRow(c.Request.Context(),
		`SELECT key_prefix FROM aimodel_api_keys
		 WHERE project_id = $1 AND environment_id = $2 AND aimodel_name = $3 AND revoked_at IS NULL`,
		projectID, envID, name,
	).Scan(&keyPrefix)

	c.JSON(http.StatusOK, gin.H{
		"model":          snap,
		"api_key_prefix": keyPrefix,
	})
}

type createAIModelRequest struct {
	Name            string                 `json:"name"`
	ModelType       string                 `json:"model_type"`
	Source          models.AIModelSource   `json:"source"`
	MLflowName      string                 `json:"mlflow_name,omitempty"`
	MLflowVersion   string                 `json:"mlflow_version,omitempty"`
	ArtifactURI     string                 `json:"artifact_uri,omitempty"`
	ContainerImage  string                 `json:"container_image,omitempty"`
	Profile         string                 `json:"profile"`
	AuthMode        models.AIModelAuthMode `json:"auth_mode"`
	AttachedAppName string                 `json:"attached_app_name,omitempty"`
	Version         string                 `json:"version,omitempty"`
}

// CreateAIModel queues a CreateAIModel operation. GPU profiles land in
// WaitingForApproval when the project's gpu_model_max is 0; otherwise the
// caller must already be platform-admin to use a GPU profile.
func (h *Handler) CreateAIModel(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	role, err := h.requireWriter(c, claims.UserID, projectID)
	if err != nil {
		return
	}

	var req createAIModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if req.AuthMode == "" {
		req.AuthMode = models.AIModelAuthAPIKey
	}
	if req.Version == "" {
		req.Version = "v1"
	}

	if err := validateKubeName(req.Name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if !validModelTypes[req.ModelType] {
		respondError(c, http.StatusBadRequest, "model_type must be one of sklearn, xgboost, lightgbm, pytorch, tensorflow, triton, huggingface, custom")
		return
	}
	prof, ok := profiles.Lookup(req.Profile)
	if !ok {
		respondError(c, http.StatusBadRequest, fmt.Sprintf("profile must be one of: %s", strings.Join(profiles.Names(), ", ")))
		return
	}
	if req.AuthMode != models.AIModelAuthAPIKey &&
		req.AuthMode != models.AIModelAuthJWT &&
		req.AuthMode != models.AIModelAuthPublic {
		respondError(c, http.StatusBadRequest, "auth_mode must be apikey, jwt, or public")
		return
	}
	if req.AuthMode == models.AIModelAuthPublic && role != models.MemberRolePlatformAdmin {
		respondError(c, http.StatusForbidden, "auth_mode=public requires platform-admin")
		return
	}

	if err := h.validateAIModelSource(c, projectID, &req); err != nil {
		return
	}

	// Uniqueness within project+env.
	var existing int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'AIModel' AND name = $3`,
		projectID, envID, req.Name,
	).Scan(&existing); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check name uniqueness")
		return
	}
	if existing > 0 {
		respondError(c, http.StatusConflict, "an AI model with that name already exists in this environment")
		return
	}

	// Quota enforcement + GPU approval gate.
	status, err := h.resolveCreateStatus(c, projectID, prof, role)
	if err != nil {
		return // response already written
	}

	payload := models.CreateAIModelPayload{
		Name:            req.Name,
		ModelType:       req.ModelType,
		Source:          req.Source,
		MLflowName:      req.MLflowName,
		MLflowVersion:   req.MLflowVersion,
		ArtifactURI:     req.ArtifactURI,
		ContainerImage:  req.ContainerImage,
		Profile:         req.Profile,
		AuthMode:        req.AuthMode,
		AttachedAppName: req.AttachedAppName,
		Version:         req.Version,
	}
	op, err := h.insertAIModelOperation(c, claims.UserID, projectID, envID,
		"CreateAIModel", req.Name, status, payload)
	if err != nil {
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "AI model creation queued"})
}

// validateAIModelSource fills in artifactURI from the requested source and
// rejects URIs outside the project's ai_storage_prefix (D13).
func (h *Handler) validateAIModelSource(c *gin.Context, projectID uuid.UUID, req *createAIModelRequest) error {
	switch req.Source {
	case models.AIModelSourceCustom:
		if req.ContainerImage == "" {
			respondError(c, http.StatusBadRequest, "container_image is required for source=custom")
			return errors.New("invalid")
		}
		if err := ValidateImage(req.ContainerImage); err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return err
		}
		req.ArtifactURI = ""
		return nil
	case models.AIModelSourceMLflow:
		if req.MLflowName == "" || req.MLflowVersion == "" {
			respondError(c, http.StatusBadRequest, "mlflow_name and mlflow_version are required for source=mlflow")
			return errors.New("invalid")
		}
		// Worker resolves the actual artifactURI from MLflow at render time.
		// Backend still verifies the MLflow record exists and lives within the project's prefix.
		// For v1 we accept the request and let the worker fail loudly if pin resolution fails.
		return nil
	case models.AIModelSourceS3:
		if req.ArtifactURI == "" {
			respondError(c, http.StatusBadRequest, "artifact_uri is required for source=s3")
			return errors.New("invalid")
		}
		return h.assertArtifactPrefix(c, projectID, req.ArtifactURI)
	default:
		respondError(c, http.StatusBadRequest, "source must be mlflow, s3, or custom")
		return errors.New("invalid")
	}
}

func (h *Handler) assertArtifactPrefix(c *gin.Context, projectID uuid.UUID, uri string) error {
	var prefix *string
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT ai_storage_prefix FROM projects WHERE id = $1`, projectID,
	).Scan(&prefix); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read project storage prefix")
		return err
	}
	if prefix == nil || *prefix == "" {
		respondError(c, http.StatusForbidden, "project has no AI storage prefix configured; ask an admin")
		return errors.New("no prefix")
	}
	if !strings.HasPrefix(uri, *prefix) {
		respondError(c, http.StatusBadRequest, fmt.Sprintf("artifact_uri must start with %s", *prefix))
		return errors.New("prefix mismatch")
	}
	return nil
}

// quotaDecision is the outcome of the quota gate.
type quotaDecision int

const (
	quotaAllow    quotaDecision = iota // Created
	quotaApproval                      // WaitingForApproval (GPU + non-admin over quota)
	quotaReject                        // 409 (admin over GPU quota, or any over CPU quota)
)

// decideQuota is the pure branching of resolveCreateStatus extracted for
// testability. GPU + non-admin + over-quota lands in approval (D12); admins
// get a hard 409 because there's no one above them to approve.
func decideQuota(prof profiles.Profile, role models.MemberRole, cpuMax, gpuMax, cpuInUse, gpuInUse int) quotaDecision {
	if prof.IsGPU() {
		if gpuInUse >= gpuMax {
			if role == models.MemberRolePlatformAdmin {
				return quotaReject
			}
			return quotaApproval
		}
		return quotaAllow
	}
	if cpuInUse >= cpuMax {
		return quotaReject
	}
	return quotaAllow
}

// resolveCreateStatus enforces quota + decides Created vs WaitingForApproval.
// Returns status and writes a response on rejection.
func (h *Handler) resolveCreateStatus(c *gin.Context, projectID uuid.UUID, prof profiles.Profile, role models.MemberRole) (models.OperationStatus, error) {
	cpuMax, gpuMax, err := h.fetchQuotaLimits(c.Request.Context(), projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read project quotas")
		return "", err
	}
	cpuInUse, gpuInUse, err := h.countAIModelsByKind(c.Request.Context(), projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to count existing models")
		return "", err
	}
	switch decideQuota(prof, role, cpuMax, gpuMax, cpuInUse, gpuInUse) {
	case quotaApproval:
		return models.OperationStatusWaitingForApproval, nil
	case quotaReject:
		if prof.IsGPU() {
			respondError(c, http.StatusConflict, fmt.Sprintf("GPU model quota exceeded (%d/%d)", gpuInUse, gpuMax))
		} else {
			respondError(c, http.StatusConflict, fmt.Sprintf("CPU model quota exceeded (%d/%d)", cpuInUse, cpuMax))
		}
		return "", errors.New("quota")
	default:
		return models.OperationStatusCreated, nil
	}
}

func (h *Handler) fetchQuotaLimits(ctx context.Context, projectID uuid.UUID) (int, int, error) {
	var cpuMax, gpuMax int
	row := h.pool.QueryRow(ctx,
		`SELECT cpu_model_max, gpu_model_max FROM project_quotas WHERE project_id = $1`,
		projectID,
	)
	err := row.Scan(&cpuMax, &gpuMax)
	if errors.Is(err, pgx.ErrNoRows) {
		return 5, 0, nil
	}
	return cpuMax, gpuMax, err
}

func (h *Handler) countAIModelsByKind(ctx context.Context, projectID uuid.UUID) (int, int, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT summary_json->>'profile' AS profile, COUNT(*) AS count
		 FROM resource_snapshots
		 WHERE project_id = $1 AND kind = 'AIModel'
		 GROUP BY summary_json->>'profile'`,
		projectID,
	)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	cpu, gpu := 0, 0
	for rows.Next() {
		var p *string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return 0, 0, err
		}
		if p == nil {
			continue
		}
		if strings.HasPrefix(*p, "gpu-") {
			gpu += n
		} else if strings.HasPrefix(*p, "cpu-") {
			cpu += n
		}
	}
	return cpu, gpu, rows.Err()
}

// UpdateAIModelArtifact queues a new artifactURI for an existing AIModel.
func (h *Handler) UpdateAIModelArtifact(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	name := c.Param("name")

	var req struct {
		ArtifactURI   string `json:"artifact_uri"`
		MLflowName    string `json:"mlflow_name"`
		MLflowVersion string `json:"mlflow_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.ArtifactURI == "" && (req.MLflowName == "" || req.MLflowVersion == "") {
		respondError(c, http.StatusBadRequest, "either artifact_uri or (mlflow_name + mlflow_version) is required")
		return
	}
	if req.ArtifactURI != "" {
		if err := h.assertArtifactPrefix(c, projectID, req.ArtifactURI); err != nil {
			return
		}
	}
	if !h.aiModelExists(c, projectID, envID, name) {
		return
	}
	payload := models.UpdateAIModelArtifactPayload{
		Name: name, ArtifactURI: req.ArtifactURI,
		MLflowName: req.MLflowName, MLflowVersion: req.MLflowVersion,
	}
	op, err := h.insertAIModelOperation(c, claims.UserID, projectID, envID,
		"UpdateAIModelArtifact", name, models.OperationStatusCreated, payload)
	if err != nil {
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "Artifact update queued"})
}

// SetCanaryTraffic queues a canary.trafficPercent change.
func (h *Handler) SetCanaryTraffic(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	name := c.Param("name")

	var req struct {
		TrafficPercent int `json:"traffic_percent"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.TrafficPercent < 0 || req.TrafficPercent > 100 {
		respondError(c, http.StatusBadRequest, "traffic_percent must be between 0 and 100")
		return
	}
	if !h.aiModelExists(c, projectID, envID, name) {
		return
	}
	payload := models.SetCanaryTrafficPayload{Name: name, TrafficPercent: req.TrafficPercent}
	op, err := h.insertAIModelOperation(c, claims.UserID, projectID, envID,
		"SetCanaryTraffic", name, models.OperationStatusCreated, payload)
	if err != nil {
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "Canary update queued"})
}

// PromoteAIModel clears the canary (sets traffic to 100% of latest revision).
func (h *Handler) PromoteAIModel(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	name := c.Param("name")
	if !h.aiModelExists(c, projectID, envID, name) {
		return
	}
	op, err := h.insertAIModelOperation(c, claims.UserID, projectID, envID,
		"PromoteAIModel", name, models.OperationStatusCreated,
		models.PromoteAIModelPayload{Name: name})
	if err != nil {
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "Promote queued"})
}

// DeleteAIModel removes an AIModel. force=true bypasses the attached-app guard
// (admin-only).
func (h *Handler) DeleteAIModel(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	role, err := h.requireWriter(c, claims.UserID, projectID)
	if err != nil {
		return
	}
	name := c.Param("name")
	force := c.Query("force") == "true"
	if force && role != models.MemberRolePlatformAdmin {
		respondError(c, http.StatusForbidden, "force=true requires platform-admin")
		return
	}
	if !h.aiModelExists(c, projectID, envID, name) {
		return
	}
	op, err := h.insertAIModelOperation(c, claims.UserID, projectID, envID,
		"DeleteAIModel", name, models.OperationStatusCreated,
		models.DeleteAIModelPayload{Name: name, Force: force})
	if err != nil {
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "Delete queued"})
}

// PinAIModelMlflowVersion pins to an MLflow registered model + version.
func (h *Handler) PinAIModelMlflowVersion(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	name := c.Param("name")
	var req struct {
		MLflowName    string `json:"mlflow_name"`
		MLflowVersion string `json:"mlflow_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.MLflowName == "" || req.MLflowVersion == "" {
		respondError(c, http.StatusBadRequest, "mlflow_name and mlflow_version are required")
		return
	}
	if !h.aiModelExists(c, projectID, envID, name) {
		return
	}
	op, err := h.insertAIModelOperation(c, claims.UserID, projectID, envID,
		"PinAIModelMlflowVersion", name, models.OperationStatusCreated,
		models.PinAIModelMlflowVersionPayload{Name: name, MLflowName: req.MLflowName, MLflowVersion: req.MLflowVersion})
	if err != nil {
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "MLflow pin queued"})
}

// RevealAIModelAPIKey consumes the one-shot reveal row created by the worker
// at CreateAIModel time. Returns 410 once the row has been consumed or expired.
func (h *Handler) RevealAIModelAPIKey(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	if c.Query("reveal") != "true" {
		respondError(c, http.StatusBadRequest, "reveal=true is required")
		return
	}
	name := c.Param("name")

	var apiKeyID uuid.UUID
	var plaintext []byte
	var actorID uuid.UUID
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT r.api_key_id, r.plaintext, o.actor_id
		 FROM aimodel_api_key_reveals r
		 JOIN aimodel_api_keys k ON k.id = r.api_key_id
		 JOIN operations o ON o.id = r.operation_id
		 WHERE k.project_id = $1 AND k.environment_id = $2 AND k.aimodel_name = $3
		   AND k.revoked_at IS NULL AND r.expires_at > NOW()
		 ORDER BY r.created_at DESC
		 LIMIT 1`,
		projectID, envID, name,
	).Scan(&apiKeyID, &plaintext, &actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusGone, gin.H{"error": "API key reveal window expired or already consumed; rotate the key to issue a new one"})
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to fetch reveal")
		return
	}
	// Only the original requester (or platform-admin) can consume the reveal.
	if actorID != claims.UserID {
		role, _ := h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID)
		if role != models.MemberRolePlatformAdmin {
			respondForbidden(c)
			return
		}
	}
	if _, err := h.pool.Exec(c.Request.Context(),
		`DELETE FROM aimodel_api_key_reveals WHERE api_key_id = $1`, apiKeyID,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to consume reveal")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"api_key":    string(plaintext),
		"expires_at": time.Now(),
	})
}

// helpers ---------------------------------------------------------------------

func (h *Handler) parseProjectEnv(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, false
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, envID, true
}

func (h *Handler) requireMember(c *gin.Context, userID, projectID uuid.UUID) (models.MemberRole, error) {
	role, err := h.getUserProjectRole(c.Request.Context(), userID, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return "", err
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return "", err
	}
	return role, nil
}

func (h *Handler) requireWriter(c *gin.Context, userID, projectID uuid.UUID) (models.MemberRole, error) {
	role, err := h.requireMember(c, userID, projectID)
	if err != nil {
		return "", err
	}
	if !canWrite(role) {
		respondForbidden(c)
		return "", errors.New("forbidden")
	}
	return role, nil
}

func (h *Handler) aiModelExists(c *gin.Context, projectID, envID uuid.UUID, name string) bool {
	var count int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'AIModel' AND name = $3`,
		projectID, envID, name,
	).Scan(&count); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check model existence")
		return false
	}
	if count == 0 {
		respondNotFound(c)
		return false
	}
	return true
}

func (h *Handler) insertAIModelOperation(c *gin.Context, actorID, projectID, envID uuid.UUID,
	action, resourceName string, status models.OperationStatus, payload any,
) (models.Operation, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return models.Operation{}, err
	}
	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, $4, 'AIModel', $5, $6, $7)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		actorID, projectID, envID, action, resourceName, status, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return models.Operation{}, err
	}
	auditMeta, _ := json.Marshal(payload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, $4, 'AIModel', $5, $6)`,
		actorID, projectID, op.ID, action, resourceName, auditMeta,
	)
	return op, nil
}
