package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListEndpoints returns all PublicApi resources for an app in a project environment.
//
// @ID          listEndpoints
// @Summary     List public endpoints of an app
// @Description Returns all public API endpoints (PublicApi resources / domains) registered for an app in an environment, with their live phase/status. Read-only.
// @Tags        endpoint
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with an endpoints array of ResourceSnapshot"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/endpoints [get]
func (h *Handler) ListEndpoints(c *gin.Context) {
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

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, environment_id, kind, name, phase, summary_json, last_synced_at
		 FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'PublicApi'
		   AND summary_json->>'app_name' = $3
		 ORDER BY name`,
		projectID, envID, appName,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query endpoints")
		return
	}
	defer rows.Close()

	var endpoints []models.ResourceSnapshot
	for rows.Next() {
		var rs models.ResourceSnapshot
		if err := rows.Scan(
			&rs.ID, &rs.ProjectID, &rs.EnvironmentID, &rs.Kind, &rs.Name,
			&rs.Phase, &rs.SummaryJSON, &rs.LastSyncedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan endpoint")
			return
		}
		endpoints = append(endpoints, rs)
	}
	if endpoints == nil {
		endpoints = []models.ResourceSnapshot{}
	}
	c.JSON(http.StatusOK, gin.H{"endpoints": endpoints})
}

type createEndpointRequest struct {
	FQDN           string   `json:"fqdn"`
	AuthEnabled    bool     `json:"auth_enabled"`
	AuthScheme     string   `json:"auth_scheme"`
	AuthScopes     []string `json:"auth_scopes"`
	SwaggerEnabled bool     `json:"swagger_enabled"`
	SwaggerPath    string   `json:"swagger_path"`
	SwaggerTitle   string   `json:"swagger_title"`
}

// CreateEndpoint enqueues a CreatePublicApi operation.
//
// @ID          createEndpoint
// @Summary     Register a public endpoint (domain) for an app
// @Description Registers a public API endpoint (PublicApi resource) exposing an app at a given FQDN, with optional auth (none, platform-jwt, internal) and Swagger publishing. Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        endpoint
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                true "Project UUID"
// @Param       envId     path     string                true "Environment UUID"
// @Param       appName   path     string                true "App name"
// @Param       body      body     createEndpointRequest true "Endpoint specification (FQDN, auth, swagger)"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/endpoints [post]
func (h *Handler) CreateEndpoint(c *gin.Context) {
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

	var req createEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	req.FQDN = normalizeDomain(req.FQDN)
	if req.FQDN == "" {
		respondError(c, http.StatusBadRequest, "fqdn is required")
		return
	}
	if !isValidDomain(req.FQDN) {
		respondError(c, http.StatusBadRequest, "fqdn must be a valid domain name")
		return
	}

	if req.AuthScheme == "" {
		req.AuthScheme = "none"
		req.AuthEnabled = false
	}
	if req.SwaggerPath == "" {
		req.SwaggerPath = "/v3/api-docs"
	}
	if req.SwaggerTitle == "" {
		req.SwaggerTitle = appName
	}

	validSchemes := map[string]bool{"none": true, "platform-jwt": true, "internal": true}
	if !validSchemes[req.AuthScheme] {
		respondError(c, http.StatusBadRequest, "auth_scheme must be none, platform-jwt, or internal")
		return
	}

	var appCount int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&appCount); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify app")
		return
	}
	if appCount == 0 {
		respondError(c, http.StatusNotFound, "app not found")
		return
	}

	publicApiName := strings.ReplaceAll(req.FQDN, ".", "-")

	var existing int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'PublicApi' AND name = $3`,
		projectID, envID, publicApiName,
	).Scan(&existing); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check uniqueness")
		return
	}
	if existing > 0 {
		respondError(c, http.StatusConflict, "a domain with that FQDN already exists in this environment")
		return
	}

	payload := models.CreatePublicApiPayload{
		AppName:        appName,
		PublicApiName:  publicApiName,
		FQDN:           req.FQDN,
		AuthEnabled:    req.AuthEnabled,
		AuthScheme:     req.AuthScheme,
		AuthScopes:     req.AuthScopes,
		SwaggerEnabled: req.SwaggerEnabled,
		SwaggerPath:    req.SwaggerPath,
		SwaggerTitle:   req.SwaggerTitle,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var op models.Operation
	err = scanOperation(tx.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreatePublicApi', 'PublicApi', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, publicApiName, payloadBytes,
	), &op)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	if err = seedOptimisticSnapshot(c.Request.Context(), tx, projectID, envID, "PublicApi", publicApiName, nil); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	if err = tx.Commit(c.Request.Context()); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	auditMeta, _ := json.Marshal(payload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'CreatePublicApi', 'PublicApi', $4, $5)`,
		claims.UserID, projectID, op.ID, publicApiName, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "Domain registration queued",
	})
}
