package api

import (
	"encoding/json"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListAppServers returns all AppServers for a project (excluding Deleted).
//
// @ID          listAppServers
// @Summary     List app servers (VMs) in a project
// @Description Returns all app servers (provisioned or connected VMs) in a project, excluding deleted ones, newest first. Read-only.
// @Tags        appserver
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "object with an app_servers array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/app-servers [get]
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
//
// @ID          getAppServer
// @Summary     Get an app server (VM) by name
// @Description Returns one app server's record (status, VM IP, provider, Portainer endpoint) by name. Read-only.
// @Tags        appserver
// @Produce     json
// @Security    BearerAuth
// @Param       projectId  path     string true "Project UUID"
// @Param       serverName path     string true "App server name"
// @Success     200        {object} map[string]interface{} "object with the app_server"
// @Failure     401        {object} map[string]string
// @Failure     404        {object} map[string]string
// @Router      /projects/{projectId}/app-servers/{serverName} [get]
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

	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
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
//
// @ID          createAppServer
// @Summary     Provision or connect an app server (VM)
// @Description Creates an app server either by provisioning a new VM via Terraform (mode=terraform, the default) or by connecting an existing VM over SSH (mode=manual, requires vm_ip and ssh_private_key). Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        appserver
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                 true "Project UUID"
// @Param       body      body     createAppServerRequest true "App server specification"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/app-servers [post]
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

	nameAudit := ""
	reject := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			Action:       "CreateAppServer",
			ResourceKind: "AppServer",
			ResourceName: nameAudit,
			Outcome:      auditOutcomeFailure,
			Metadata:     map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
	}

	var req createAppServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		reject(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	nameAudit = req.Name
	if req.Name == "" {
		reject(http.StatusBadRequest, "missing_name", "name is required")
		return
	}
	if err := validateKubeName(req.Name); err != nil {
		reject(http.StatusBadRequest, "invalid_name", err.Error())
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = "terraform"
	}
	switch mode {
	case "terraform":
		if req.Region != "" && !isValidAppServerRegion(req.Region) {
			reject(http.StatusBadRequest, "invalid_region", "region must be one of: ru1, ru2, kz1, eu1")
			return
		}
	case "manual":
		if req.VMIP == "" {
			reject(http.StatusBadRequest, "missing_vm_ip", "vm_ip is required for manual mode")
			return
		}
		if req.SSHPrivateKey == "" {
			reject(http.StatusBadRequest, "missing_ssh_key", "ssh_private_key is required for manual mode")
			return
		}
	default:
		reject(http.StatusBadRequest, "invalid_mode", "mode must be one of: terraform, manual")
		return
	}

	// Check name uniqueness
	var existing int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM app_servers WHERE project_id = $1 AND name = $2 AND status != 'Deleted'`,
		projectID, req.Name,
	).Scan(&existing); err != nil {
		reject(http.StatusInternalServerError, "uniqueness_check_failed", "failed to check name uniqueness")
		return
	}
	if existing > 0 {
		reject(http.StatusConflict, "name_taken", "an app server with that name already exists in this project")
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
		reject(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:    projectID,
		OperationID:  op.ID,
		Action:       "CreateAppServer",
		ResourceKind: "AppServer",
		ResourceName: req.Name,
		Outcome:      auditOutcomeSuccess,
		Metadata: map[string]any{
			"mode":     auditPayload.Mode,
			"flavor":   auditPayload.Flavor,
			"os_image": auditPayload.OSImage,
			"region":   auditPayload.Region,
			"ssh_user": auditPayload.SSHUser,
			"ssh_port": auditPayload.SSHPort,
		},
	})

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "AppServer creation queued"})
}

// DeleteAppServer enqueues a DeleteAppServer operation.
//
// @ID          deleteAppServer
// @Summary     Delete an app server (VM)
// @Description Destructive: tears down the app server. For Terraform-provisioned servers this destroys the underlying VM and is irreversible. Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        appserver
// @Produce     json
// @Security    BearerAuth
// @Param       projectId  path     string true "Project UUID"
// @Param       serverName path     string true "App server name"
// @Success     202        {object} map[string]interface{} "object with the accepted operation"
// @Failure     401        {object} map[string]string
// @Failure     403        {object} map[string]string
// @Failure     404        {object} map[string]string
// @Router      /projects/{projectId}/app-servers/{serverName} [delete]
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

// DiscoverWorkload enqueues a read-only DiscoverWorkload operation that inventories
// the VM's running containers/volumes via the Portainer docker proxy (no SSH). The
// result (containers + a ready-to-paste external-volume compose block) lands on the
// operation's validation_result; poll the operation to read it.
//
// @ID          discoverAppServerWorkload
// @Summary     Discover an app server's running workload (read-only)
// @Description Inventories the containers, images, ports and named volumes running on an enrolled VM through the Portainer docker proxy — no SSH. Produces an external-volume compose block for the GitOps migration. Requires the VM to be enrolled (has a Portainer endpoint). Asynchronous: returns 202 with an operation; poll it and read validation_result.
// @Tags        appserver
// @Produce     json
// @Security    BearerAuth
// @Param       projectId  path     string true "Project UUID"
// @Param       serverName path     string true "App server name"
// @Success     202        {object} map[string]interface{} "object with the accepted operation"
// @Failure     401        {object} map[string]string
// @Failure     403        {object} map[string]string
// @Failure     404        {object} map[string]string
// @Failure     409        {object} map[string]string "VM is not enrolled yet"
// @Router      /projects/{projectId}/app-servers/{serverName}/discover [post]
func (h *Handler) DiscoverWorkload(c *gin.Context) {
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

	// Discovery reads through the Portainer docker proxy, which requires the VM to
	// be enrolled (have a Portainer endpoint). Reject early if it is not.
	var endpointID *int
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT portainer_endpoint_id FROM app_servers
		 WHERE project_id = $1 AND name = $2 AND status != 'Deleted'`,
		projectID, serverName,
	).Scan(&endpointID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to find app server")
		return
	}
	if endpointID == nil {
		respondError(c, http.StatusConflict, "app server is not enrolled yet (no Portainer endpoint); enroll it before discovery")
		return
	}

	payload := models.DiscoverWorkloadPayload{ServerName: serverName, EndpointID: *endpointID}
	payloadBytes, _ := json.Marshal(payload)

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, 'DiscoverWorkload', 'AppServer', $3, 'Created', $4)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, serverName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "workload discovery queued"})
}

type importComposeStackRequest struct {
	AppName         string                     `json:"app_name"`
	Services        []models.ImportServiceSpec `json:"services"`
	Env             map[string]string          `json:"env"`
	AckSecretsInGit bool                       `json:"ack_secrets_in_git"`
}

// ImportComposeStack enqueues an operation that adopts a discovered VM workload
// (see DiscoverWorkload) into a managed compose App: it renders compose.yaml +
// .env from the included services, commits them to git, and deploys via the
// same DeployStack chain a normal compose CreateApp uses — so the imported
// stack shows up as an ordinary app with live state/logs/metrics.
//
// @ID          importComposeStack
// @Summary     Import a discovered workload as a managed app
// @Description Adopts a subset of a VM's discovered containers (see the discover endpoint) into a new managed compose App bound to this app server's environment. Named volumes referenced by included services are pinned external in the rendered compose.yaml so the first deploy attaches existing data instead of creating an empty volume. Asynchronous: returns 202 with an operation; poll it until terminal.
// @Tags        appserver
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId  path     string                     true "Project UUID"
// @Param       serverName path     string                     true "App server name"
// @Param       body       body     importComposeStackRequest  true "Import specification"
// @Success     202        {object} map[string]interface{} "object with the accepted operation"
// @Failure     400        {object} map[string]string
// @Failure     401        {object} map[string]string
// @Failure     403        {object} map[string]string
// @Failure     404        {object} map[string]string
// @Failure     409        {object} map[string]string "VM not enrolled/Ready, no included services, or plaintext-secret consent missing"
// @Router      /projects/{projectId}/app-servers/{serverName}/import [post]
func (h *Handler) ImportComposeStack(c *gin.Context) {
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

	appAudit := ""
	var auditEnvID uuid.UUID
	rejectImport := func(status int, reason string, respond func()) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: auditEnvID,
			Action:        "ImportComposeStack",
			ResourceKind:  "App",
			ResourceName:  appAudit,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status, "server": serverName},
		})
		respond()
	}
	rejectImportErr := func(status int, reason, msg string) {
		rejectImport(status, reason, func() { respondError(c, status, msg) })
	}

	var req importComposeStackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rejectImportErr(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	appAudit = req.AppName
	if req.AppName == "" {
		rejectImportErr(http.StatusBadRequest, "missing_app_name", "app_name is required")
		return
	}
	if err := validateKubeName(req.AppName); err != nil {
		rejectImportErr(http.StatusBadRequest, "invalid_app_name", err.Error())
		return
	}

	var included []models.ImportServiceSpec
	for _, svc := range req.Services {
		if svc.Include {
			included = append(included, svc)
		}
	}
	if len(included) == 0 {
		rejectImportErr(http.StatusBadRequest, "no_service_included", "at least one service must be included")
		return
	}
	if len(req.Env) > 0 && !req.AckSecretsInGit {
		rejectImportErr(http.StatusConflict, "secrets_ack_missing", "env is non-empty; set ack_secrets_in_git=true to confirm plaintext .env may land in git")
		return
	}

	var endpointID *int
	var serverStatus string
	var serverID uuid.UUID
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id, portainer_endpoint_id, status FROM app_servers
		 WHERE project_id = $1 AND name = $2 AND status != 'Deleted'`,
		projectID, serverName,
	).Scan(&serverID, &endpointID, &serverStatus)
	if err == pgx.ErrNoRows {
		rejectImport(http.StatusNotFound, "server_not_found", func() { respondNotFound(c) })
		return
	}
	if err != nil {
		rejectImportErr(http.StatusInternalServerError, "server_lookup_failed", "failed to find app server")
		return
	}
	if endpointID == nil {
		rejectImportErr(http.StatusConflict, "server_not_enrolled", "app server is not enrolled yet (no Portainer endpoint); enroll it before importing")
		return
	}
	if serverStatus != string(models.AppServerStatusReady) {
		rejectImportErr(http.StatusConflict, "server_not_ready", "app server is not Ready yet")
		return
	}

	var envID uuid.UUID
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT e.id FROM environments e
		 JOIN app_servers s ON s.id = e.app_server_id
		 WHERE s.project_id = $1 AND s.name = $2
		 ORDER BY e.created_at LIMIT 1`,
		projectID, serverName,
	).Scan(&envID)
	if err == pgx.ErrNoRows {
		var projectSlug string
		if err := h.pool.QueryRow(c.Request.Context(),
			`SELECT name FROM projects WHERE id = $1`, projectID,
		).Scan(&projectSlug); err != nil {
			rejectImportErr(http.StatusInternalServerError, "project_lookup_failed", "failed to resolve project for environment")
			return
		}
		if err := h.pool.QueryRow(c.Request.Context(),
			`INSERT INTO environments (project_id, name, namespace, type, runtime, app_server_id)
			 VALUES ($1, $2, $3, 'prod', 'vm', $4)
			 ON CONFLICT (project_id, name)
			 DO UPDATE SET runtime = 'vm', app_server_id = EXCLUDED.app_server_id, updated_at = NOW()
			 RETURNING id`,
			projectID, serverName, projectSlug+"-"+serverName, serverID,
		).Scan(&envID); err != nil {
			rejectImportErr(http.StatusInternalServerError, "environment_create_failed", "failed to create app server environment")
			return
		}
	} else if err != nil {
		rejectImportErr(http.StatusInternalServerError, "environment_lookup_failed", "failed to resolve app server environment")
		return
	}
	auditEnvID = envID

	// Each included service becomes its own first-class Application; reject if any
	// target app name already exists in this environment.
	for _, svc := range included {
		var existing int
		if err := h.pool.QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM resource_snapshots
			 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
			projectID, envID, svc.ServiceName,
		).Scan(&existing); err != nil {
			rejectImportErr(http.StatusInternalServerError, "uniqueness_check_failed", "failed to check name uniqueness")
			return
		}
		if existing > 0 {
			rejectImportErr(http.StatusConflict, "service_name_taken", "an app named '"+svc.ServiceName+"' already exists in this environment")
			return
		}
	}

	// Seed the imported env into every created Application's env_vars (encrypted,
	// runtime scope) so a later edit-env/redeploy through the normal app UI keeps
	// them. The import env is workload-wide, so each created app gets the full set.
	for _, svc := range included {
		for key, value := range req.Env {
			encrypted, encErr := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(value))
			if encErr != nil {
				rejectImportErr(http.StatusInternalServerError, "env_encrypt_failed", "failed to encrypt imported env var")
				return
			}
			if _, dbErr := h.pool.Exec(c.Request.Context(),
				`INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope, created_by)
				 VALUES ($1, $2, $3, $4, TRUE, 'runtime', $5)
				 ON CONFLICT (environment_id, app_name, key)
				 DO UPDATE SET value_encrypted = EXCLUDED.value_encrypted,
				               is_secret = EXCLUDED.is_secret,
				               scope = EXCLUDED.scope,
				               updated_at = NOW()`,
				envID, svc.ServiceName, key, encrypted, claims.UserID,
			); dbErr != nil {
				rejectImportErr(http.StatusInternalServerError, "env_persist_failed", "failed to persist imported env var")
				return
			}
		}
	}

	payload := models.ImportComposeStackPayload{
		AppName:         req.AppName,
		ServerName:      serverName,
		Services:        included,
		EnvVars:         req.Env,
		AckSecretsInGit: req.AckSecretsInGit,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		rejectImportErr(http.StatusInternalServerError, "marshal_failed", "failed to marshal payload")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'ImportComposeStack', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, req.AppName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		rejectImportErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	importedServices := make([]string, 0, len(included))
	for _, svc := range included {
		importedServices = append(importedServices, svc.ServiceName)
	}
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "ImportComposeStack",
		ResourceKind:  "App",
		ResourceName:  req.AppName,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"server":        serverName,
			"services":      importedServices,
			"env_var_count": len(req.Env),
		},
	})

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "compose stack import queued"})
}
