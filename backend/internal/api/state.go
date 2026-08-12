package api

import (
	"net/http"
	"strconv"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// requireProjectMember checks the caller is a member of the project and returns
// false (after writing the response) otherwise.
func (h *Handler) requireProjectMember(c *gin.Context, projectID uuid.UUID) bool {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return false
	}
	_, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return false
	}
	return true
}

// GetAppServerState returns live VM state from Portainer (endpoint heartbeat +
// containers), falling back to the DB status when no endpoint/Portainer exists.
// GET /projects/:projectId/app-servers/:serverName/state
//
// @ID          getAppServerState
// @Summary     Get live state of an app server (VM)
// @Description Returns the live state of an app server: online/heartbeat from Portainer plus the running containers, falling back to the stored DB status when no live endpoint exists. Read-only.
// @Tags        appserver
// @Produce     json
// @Security    BearerAuth
// @Param       projectId  path     string true "Project UUID"
// @Param       serverName path     string true "App server name"
// @Success     200        {object} map[string]interface{} "object with status, online flag and containers"
// @Failure     401        {object} map[string]string
// @Failure     404        {object} map[string]string
// @Router      /projects/{projectId}/app-servers/{serverName}/state [get]
func (h *Handler) GetAppServerState(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	if !h.requireProjectMember(c, projectID) {
		return
	}
	serverName := c.Param("serverName")

	var (
		status   string
		source   string
		endpoint *int
	)
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT status, source, portainer_endpoint_id
		 FROM app_servers WHERE project_id = $1 AND name = $2`,
		projectID, serverName,
	).Scan(&status, &source, &endpoint)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load app server")
		return
	}

	resp := gin.H{"status": status, "source": source, "online": false}

	if h.portainer == nil || endpoint == nil {
		c.JSON(http.StatusOK, resp)
		return
	}

	ep, err := h.portainer.GetEndpoint(c.Request.Context(), *endpoint)
	if err != nil {
		// Live lookup failed — return DB-derived state, flag the proxy error.
		setLiveError(resp, err.Error())
		c.JSON(http.StatusOK, resp)
		return
	}
	resp["online"] = ep.Heartbeat || ep.Status == 1
	resp["last_checkin"] = ep.LastCheckInDate

	if containers, err := h.portainer.ListContainers(c.Request.Context(), *endpoint, ""); err == nil {
		resp["containers"] = containers
	}
	c.JSON(http.StatusOK, resp)
}

// resolveEnvEndpoint returns the Portainer endpoint id for a VM environment's
// AppServer, or an error response already written.
func (h *Handler) resolveEnvEndpoint(c *gin.Context, projectID, envID uuid.UUID) (int, bool) {
	var endpoint *int
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT s.portainer_endpoint_id
		 FROM environments e JOIN app_servers s ON s.id = e.app_server_id
		 WHERE e.id = $1 AND e.project_id = $2`,
		envID, projectID,
	).Scan(&endpoint)
	if err == pgx.ErrNoRows {
		respondError(c, http.StatusConflict, "not applicable to this environment: this endpoint serves VM (Docker Compose) environments only, and this environment has no AppServer. Kubernetes apps are served by the Kubernetes endpoints instead; this says nothing about the health of any application")
		return 0, false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve environment endpoint")
		return 0, false
	}
	if endpoint == nil {
		respondError(c, http.StatusConflict, "not applicable yet: this VM environment's AppServer is still being provisioned and has no Portainer endpoint. This says nothing about the health of any application")
		return 0, false
	}
	return *endpoint, true
}

// GetAppState returns live compose-stack + container state for a compose app.
// GET /projects/:projectId/environments/:envId/apps/:appName/state
//
// @ID          getAppState
// @Summary     Get live state of a compose app
// @Description Returns the live Docker Compose stack + container state for a VM (compose) app, queried from Portainer. Read-only. Returns 503 when live state is not configured.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with stack, online flag and containers"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/state [get]
func (h *Handler) GetAppState(c *gin.Context) {
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
	if !h.requireProjectMember(c, projectID) {
		return
	}
	appName := c.Param("appName")

	if h.portainer == nil {
		respondError(c, http.StatusServiceUnavailable, "live state not configured")
		return
	}
	endpoint, ok := h.resolveEnvEndpoint(c, projectID, envID)
	if !ok {
		return
	}

	resp := gin.H{"online": false}

	// One first-class Application is one compose service in the shared per-VM
	// stack, so scope live containers to this app by the service label (== app
	// name), not the whole stack's compose project.
	containers, err := h.portainer.ListContainers(c.Request.Context(), endpoint, "com.docker.compose.service="+appName)
	if err != nil {
		setLiveError(resp, err.Error())
	} else {
		resp["containers"] = containers
		resp["online"] = len(containers) > 0
	}
	c.JSON(http.StatusOK, resp)
}

// GetAppLogs proxies the last N lines of a container's logs (read-only).
// GET /projects/:projectId/environments/:envId/apps/:appName/logs?container=<id>&tail=N
//
// @ID          getAppLogs
// @Summary     Get recent logs of a compose app container
// @Description Returns the last N lines of a single container's logs for a VM (compose) app, proxied from Portainer. Read-only. The container query param (container id) is required; tail defaults to 200 (max 5000).
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       envId     path     string true  "Environment UUID"
// @Param       appName   path     string true  "App name"
// @Param       container query    string true  "Container id to read logs from"
// @Param       tail      query    int    false "Number of log lines to return (1-5000, default 200)"
// @Success     200       {object} map[string]interface{} "object with a logs string"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/logs [get]
func (h *Handler) GetAppLogs(c *gin.Context) {
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
	if !h.requireProjectMember(c, projectID) {
		return
	}

	containerID := c.Query("container")
	if containerID == "" {
		respondError(c, http.StatusBadRequest, "container query param is required")
		return
	}
	tail := 200
	if t := c.Query("tail"); t != "" {
		if n, err := strconv.Atoi(t); err == nil && n > 0 && n <= 5000 {
			tail = n
		}
	}

	if h.portainer == nil {
		respondError(c, http.StatusServiceUnavailable, "live state not configured")
		return
	}
	endpoint, ok := h.resolveEnvEndpoint(c, projectID, envID)
	if !ok {
		return
	}

	logs, err := h.portainer.GetContainerLogs(c.Request.Context(), endpoint, containerID, tail)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to fetch logs: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}
