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

// ListAppServers returns all AppServers for a project (excluding Deleted).
func (h *Handler) ListAppServers(c *gin.Context) {
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
		`SELECT id, project_id, name, source, vm_ip, vm_provider_id, terraform_workspace,
		        portainer_endpoint_id, status, error_message, created_at, updated_at
		 FROM app_servers
		 WHERE project_id = $1 AND status != 'Deleted'
		 ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query app servers")
		return
	}
	defer rows.Close()

	var servers []models.AppServer
	for rows.Next() {
		var s models.AppServer
		if err := rows.Scan(
			&s.ID, &s.ProjectID, &s.Name, &s.Source, &s.VMIP, &s.VMProviderID,
			&s.TerraformWorkspace, &s.PortainerEndpointID,
			&s.Status, &s.ErrorMessage, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan app server")
			return
		}
		servers = append(servers, s)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading app servers")
		return
	}
	if servers == nil {
		servers = []models.AppServer{}
	}
	c.JSON(http.StatusOK, gin.H{"app_servers": servers})
}

// GetAppServer returns a single AppServer by name.
func (h *Handler) GetAppServer(c *gin.Context) {
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
	serverName := c.Param("serverName")

	_, err = h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	var s models.AppServer
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id, project_id, name, source, vm_ip, vm_provider_id, terraform_workspace,
		        portainer_endpoint_id, status, error_message, created_at, updated_at
		 FROM app_servers
		 WHERE project_id = $1 AND name = $2`,
		projectID, serverName,
	).Scan(
		&s.ID, &s.ProjectID, &s.Name, &s.Source, &s.VMIP, &s.VMProviderID,
		&s.TerraformWorkspace, &s.PortainerEndpointID,
		&s.Status, &s.ErrorMessage, &s.CreatedAt, &s.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to get app server")
		return
	}
	c.JSON(http.StatusOK, gin.H{"app_server": s})
}

type createAppServerRequest struct {
	Name       string `json:"name"`
	Mode       string `json:"mode"` // "terraform" (default) | "manual"
	Flavor     string `json:"flavor"`
	OSImage    string `json:"os_image"`
	Region     string `json:"region"`
	SSHKeyName string `json:"ssh_key_name"`

	// Manual-mode fields (connecting a pre-existing VM over SSH).
	VMIP          string `json:"vm_ip"`
	SSHUser       string `json:"ssh_user"`
	SSHPort       int    `json:"ssh_port"`
	SSHPrivateKey string `json:"ssh_private_key"`
}

func isValidAppServerRegion(region string) bool {
	switch region {
	case "ru1", "ru2", "kz1", "eu1":
		return true
	default:
		return false
	}
}

// CreateAppServer enqueues a CreateAppServer operation.
func (h *Handler) CreateAppServer(c *gin.Context) {
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

	var req createAppServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		respondError(c, http.StatusBadRequest, "name is required")
		return
	}
	if err := validateKubeName(req.Name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = "terraform"
	}
	switch mode {
	case "terraform":
		if req.Region != "" && !isValidAppServerRegion(req.Region) {
			respondError(c, http.StatusBadRequest, "region must be one of: ru1, ru2, kz1, eu1")
			return
		}
	case "manual":
		if req.VMIP == "" {
			respondError(c, http.StatusBadRequest, "vm_ip is required for manual mode")
			return
		}
		if req.SSHPrivateKey == "" {
			respondError(c, http.StatusBadRequest, "ssh_private_key is required for manual mode")
			return
		}
	default:
		respondError(c, http.StatusBadRequest, "mode must be one of: terraform, manual")
		return
	}

	// Check name uniqueness
	var existing int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM app_servers WHERE project_id = $1 AND name = $2 AND status != 'Deleted'`,
		projectID, req.Name,
	).Scan(&existing); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check name uniqueness")
		return
	}
	if existing > 0 {
		respondError(c, http.StatusConflict, "an app server with that name already exists in this project")
		return
	}

	payload := models.CreateAppServerPayload{
		Name:          req.Name,
		Mode:          mode,
		Flavor:        req.Flavor,
		OSImage:       req.OSImage,
		Region:        req.Region,
		SSHKeyName:    req.SSHKeyName,
		VMIP:          req.VMIP,
		SSHUser:       req.SSHUser,
		SSHPort:       req.SSHPort,
		SSHPrivateKey: req.SSHPrivateKey,
	}
	payloadBytes, _ := json.Marshal(payload)

	// Audit metadata must never carry the one-shot SSH private key.
	auditPayload := payload
	auditPayload.SSHPrivateKey = ""

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, 'CreateAppServer', 'AppServer', $3, 'Created', $4)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, req.Name, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	auditMeta, _ := json.Marshal(auditPayload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'CreateAppServer', 'AppServer', $4, $5)`,
		claims.UserID, projectID, op.ID, req.Name, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "AppServer creation queued"})
}

// DeleteAppServer enqueues a DeleteAppServer operation.
func (h *Handler) DeleteAppServer(c *gin.Context) {
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
	serverName := c.Param("serverName")

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

	// Verify server exists and is not already being deleted
	var serverID uuid.UUID
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id FROM app_servers WHERE project_id = $1 AND name = $2 AND status NOT IN ('Deleting','Deleted')`,
		projectID, serverName,
	).Scan(&serverID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to find app server")
		return
	}

	payload := models.DeleteAppServerPayload{AppServerName: serverName}
	payloadBytes, _ := json.Marshal(payload)

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, 'DeleteAppServer', 'AppServer', $3, 'Created', $4)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, serverName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "AppServer deletion queued"})
}
