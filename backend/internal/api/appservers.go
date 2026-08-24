package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

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

// appServerRegions lists the regions the VM provider actually serves. It is an
// allowlist, not a wish list: "ru2", "kz1" and "eu1" were offered here and in
// the console dropdown while Beget only ever had ru1, so every pick but the
// first died inside `terraform apply` with
// `Region 'eu1' does not exist. Available regions: ru1` — after the console had
// already accepted the order and created the row.
var appServerRegions = []string{"ru1"}

func isValidAppServerRegion(region string) bool {
	for _, r := range appServerRegions {
		if r == region {
			return true
		}
	}
	return false
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

	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		if qErr := h.checkQuota(c.Request.Context(), orgID, "app_servers"); qErr != nil {
			if meta, blocked := billingBlockAudit(qErr); blocked {
				h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
					ProjectID:    projectID,
					Action:       "CreateAppServer",
					ResourceKind: "AppServer",
					Outcome:      auditOutcomeFailure,
					Metadata:     meta,
				})
				h.respondBillingBlocked(c, orgID, qErr)
				return
			}
		}
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
			reject(http.StatusBadRequest, "invalid_region", "region must be one of: "+strings.Join(appServerRegions, ", "))
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
// @Description Destructive: tears down the app server. For Terraform-provisioned servers this destroys the underlying VM and is irreversible. Asynchronous: returns 202 with an operation; poll the operation until terminal. A server left in Deleting status by a failed deletion can be deleted again; a deletion that is still running returns 409.
// @Tags        appserver
// @Produce     json
// @Security    BearerAuth
// @Param       projectId  path     string true "Project UUID"
// @Param       serverName path     string true "App server name"
// @Success     202        {object} map[string]interface{} "object with the accepted operation"
// @Failure     401        {object} map[string]string
// @Failure     403        {object} map[string]string
// @Failure     404        {object} map[string]string
// @Failure     409        {object} map[string]string
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

	audit := func(opID uuid.UUID, outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			OperationID:  opID,
			Action:       "DeleteAppServer",
			ResourceKind: "AppServer",
			ResourceName: serverName,
			Outcome:      outcome,
			Metadata:     meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		respondError(c, status, msg)
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_a_member", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "membership_check_failed", "failed to check project membership")
		return
	}
	if !canWrite(role) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "read_only_role", "status": http.StatusForbidden})
		respondForbidden(c)
		return
	}

	// Verify the server exists and has not already been deleted. Status
	// 'Deleting' is deliberately NOT a rejection: a delete whose worker failed
	// leaves the row parked in 'Deleting' forever, and rejecting on the status
	// alone made that row permanently undeletable (404 on every retry) instead
	// of merely stuck. What must not be duplicated is a delete that is still
	// running, so the in-flight check below looks at the operation, not the row.
	var serverID uuid.UUID
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id FROM app_servers WHERE project_id = $1 AND name = $2 AND status <> 'Deleted'`,
		projectID, serverName,
	).Scan(&serverID)
	if err == pgx.ErrNoRows {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "server_not_found_or_already_deleted", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "server_lookup_failed", "failed to find app server")
		return
	}

	var inFlight int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM operations
		 WHERE project_id = $1 AND action = 'DeleteAppServer' AND resource_name = $2
		   AND status NOT IN ('Ready','Failed')`,
		projectID, serverName,
	).Scan(&inFlight); err != nil {
		rejectErr(http.StatusInternalServerError, "delete_inflight_check_failed", "failed to check running deletions")
		return
	}
	if inFlight > 0 {
		rejectErr(http.StatusConflict, "delete_already_running", "deletion of this app server is already running")
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
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	audit(op.ID, auditOutcomeSuccess, map[string]any{"app_server_id": serverID})

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

	envID, err := h.ensureVMEnvironment(c.Request.Context(), projectID, serverName, serverID)
	if err != nil {
		rejectImportErr(http.StatusInternalServerError, "environment_create_failed", "failed to resolve app server environment")
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

// ensureVMEnvironment resolves the single vm-runtime environment bound to an app
// server, creating it on first use. A VM gets no environments row at provisioning
// time (doCreateAppServer never inserts one), yet every environment-scoped lever
// the console has — ingress, hostnames, env vars, app snapshots — is keyed by
// environment id, so without this the VM is reachable by none of them until an
// import happens to run. Name/namespace are derived deterministically from the
// server name so import and hostname attachment converge on the same row.
func (h *Handler) ensureVMEnvironment(ctx context.Context, projectID uuid.UUID, serverName string, serverID uuid.UUID) (uuid.UUID, error) {
	var envID uuid.UUID
	err := h.pool.QueryRow(ctx,
		`SELECT e.id FROM environments e
		 JOIN app_servers s ON s.id = e.app_server_id
		 WHERE s.project_id = $1 AND s.name = $2
		 ORDER BY e.created_at LIMIT 1`,
		projectID, serverName,
	).Scan(&envID)
	if err == nil {
		return envID, nil
	}
	if err != pgx.ErrNoRows {
		return uuid.Nil, err
	}

	var projectSlug string
	if err := h.pool.QueryRow(ctx,
		`SELECT name FROM projects WHERE id = $1`, projectID,
	).Scan(&projectSlug); err != nil {
		return uuid.Nil, err
	}
	if err := h.pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime, app_server_id)
		 VALUES ($1, $2, $3, 'prod', 'vm', $4)
		 ON CONFLICT (project_id, name)
		 DO UPDATE SET runtime = 'vm', app_server_id = EXCLUDED.app_server_id, updated_at = NOW()
		 RETURNING id`,
		projectID, serverName, projectSlug+"-"+serverName, serverID,
	).Scan(&envID); err != nil {
		return uuid.Nil, err
	}
	return envID, nil
}

type attachAppServerHostnameRequest struct {
	AppName      string `json:"app_name"`
	Hostname     string `json:"hostname"`
	TargetPort   int    `json:"target_port"`
	HostLoopback bool   `json:"host_loopback"`
}

// AttachAppServerHostname publishes a VM on a platform subdomain in one call.
//
// This is the VM counterpart of the default domain a k8s app gets at CreateApp:
// it mints a managed hostname under the platform's own base domain with an A
// record pointing at the app server's public IP, and enqueues the same
// AttachDefaultDomain operation gitops-agent renders — which on a vm-runtime
// environment installs/extends the platform-managed nginx + ACME stack on the VM
// and writes the DNS carrier in the same commit.
//
// It deliberately does NOT require a compose import first: ensureVMEnvironment
// gives the server an environment on the spot, so a freshly provisioned VM is one
// call away from a working https URL. host_loopback covers the common case of a
// workload the platform did not deploy, listening on 127.0.0.1 — nginx then
// proxies to the host gateway instead of a compose service.
//
// @ID          attachAppServerHostname
// @Summary     Publish an app server on a platform subdomain
// @Description Mints a managed <name>.<platform-domain> hostname whose A record points at this app server's public IP, and installs/extends the platform-managed nginx + Let's Encrypt stack on the VM to serve it. Works on a bare VM with no imported workload. Set host_loopback=true with target_port to proxy a service bound to 127.0.0.1 on the host; otherwise target_port names the port of the managed compose app given by app_name. Asynchronous: returns 202 with an operation; poll it until terminal.
// @Tags        appserver
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId  path     string                          true "Project UUID"
// @Param       serverName path     string                          true "App server name"
// @Param       body       body     attachAppServerHostnameRequest  false "Hostname specification"
// @Success     202        {object} map[string]interface{} "object with the accepted operation and the minted hostname"
// @Success     200        {object} map[string]interface{} "hostname already attached; nothing enqueued"
// @Failure     400        {object} map[string]string
// @Failure     401        {object} map[string]string
// @Failure     403        {object} map[string]string
// @Failure     404        {object} map[string]string
// @Failure     409        {object} map[string]string "VM not enrolled/Ready, no public IP, or hostname taken elsewhere"
// @Router      /projects/{projectId}/app-servers/{serverName}/hostname [post]
func (h *Handler) AttachAppServerHostname(c *gin.Context) {
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

	var req attachAppServerHostnameRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}
	}

	reject := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			Action:       "AttachAppServerHostname",
			ResourceKind: "App",
			ResourceName: req.AppName,
			Outcome:      auditOutcomeFailure,
			Metadata:     map[string]any{"reason": reason, "status": status, "server": serverName},
		})
		respondError(c, status, msg)
	}

	if !h.cfg.DefaultDomainEnabled || h.cfg.DefaultDomainBase == "" {
		reject(http.StatusConflict, "default_domain_disabled", "platform domains are not enabled on this installation")
		return
	}

	appName := req.AppName
	if appName == "" {
		appName = serverName
	}
	if err := validateKubeName(appName); err != nil {
		reject(http.StatusBadRequest, "invalid_app_name", err.Error())
		return
	}
	if req.HostLoopback && req.TargetPort <= 0 {
		reject(http.StatusBadRequest, "missing_target_port", "host_loopback requires target_port: the platform cannot guess which loopback port to proxy to")
		return
	}
	if req.TargetPort < 0 || req.TargetPort > 65535 {
		reject(http.StatusBadRequest, "invalid_target_port", "target_port must be between 1 and 65535")
		return
	}

	var endpointID *int
	var serverStatus string
	var serverID uuid.UUID
	var vmIP *string
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id, portainer_endpoint_id, status, vm_ip FROM app_servers
		 WHERE project_id = $1 AND name = $2 AND status != 'Deleted'`,
		projectID, serverName,
	).Scan(&serverID, &endpointID, &serverStatus, &vmIP)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		reject(http.StatusInternalServerError, "server_lookup_failed", "failed to find app server")
		return
	}
	if endpointID == nil {
		reject(http.StatusConflict, "server_not_enrolled", "app server is not enrolled yet (no Portainer endpoint); enroll it before publishing a hostname")
		return
	}
	if serverStatus != string(models.AppServerStatusReady) {
		reject(http.StatusConflict, "server_not_ready", "app server is not Ready yet")
		return
	}
	if vmIP == nil || *vmIP == "" {
		reject(http.StatusConflict, "server_has_no_ip", "app server has no public IP recorded yet; a hostname would have nothing to point at")
		return
	}

	hostname := normalizeDomain(req.Hostname)
	if hostname == "" {
		suffix, sErr := randomHostSuffix()
		if sErr != nil {
			reject(http.StatusInternalServerError, "hostname_mint_failed", "failed to mint a hostname")
			return
		}
		hostname = buildDefaultHostname(h.cfg.DefaultDomainBase, appName, suffix)
	} else if !strings.HasSuffix(hostname, "."+h.cfg.DefaultDomainBase) {
		reject(http.StatusBadRequest, "hostname_not_platform_domain",
			"this endpoint only mints hostnames under "+h.cfg.DefaultDomainBase+"; for your own domain verify its apex and use the custom hostname endpoint")
		return
	} else if !isValidDomain(hostname) {
		reject(http.StatusBadRequest, "invalid_hostname", "hostname is not a valid domain name")
		return
	}

	envID, err := h.ensureVMEnvironment(c.Request.Context(), projectID, serverName, serverID)
	if err != nil {
		reject(http.StatusInternalServerError, "environment_create_failed", "failed to resolve app server environment")
		return
	}

	var existingEnv uuid.UUID
	var existingApp string
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT environment_id, app_name FROM domain_hostnames WHERE hostname = $1`, hostname,
	).Scan(&existingEnv, &existingApp)
	if err == nil {
		if existingEnv != envID {
			reject(http.StatusConflict, "hostname_taken", "that hostname is already attached elsewhere")
			return
		}
		c.JSON(http.StatusOK, gin.H{"hostname": hostname, "app_name": existingApp, "message": "hostname already attached"})
		return
	}
	if err != pgx.ErrNoRows {
		reject(http.StatusInternalServerError, "hostname_lookup_failed", "failed to check hostname")
		return
	}

	payloadBytes, err := json.Marshal(models.AttachCustomHostnamePayload{
		AppName:      appName,
		Hostname:     hostname,
		Port:         req.TargetPort,
		HostLoopback: req.HostLoopback,
	})
	if err != nil {
		reject(http.StatusInternalServerError, "payload_marshal_failed", "failed to marshal payload")
		return
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		reject(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var op models.Operation
	row := tx.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'AttachDefaultDomain', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		reject(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}
	if _, err := tx.Exec(c.Request.Context(),
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, operation_id, managed)
		 VALUES (NULL, $1, $2, $3, 'A', 'pending', 'pending', $4, true)`,
		envID, appName, hostname, op.ID,
	); err != nil {
		reject(http.StatusInternalServerError, "hostname_insert_failed", "failed to record hostname")
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		reject(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "AttachAppServerHostname",
		ResourceKind:  "App",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"server":        serverName,
			"hostname":      hostname,
			"target_port":   req.TargetPort,
			"host_loopback": req.HostLoopback,
		},
	})

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"hostname":  hostname,
		"app_name":  appName,
		"message":   "hostname attachment queued",
	})
}
