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
	_, err := h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID)
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
		resp["live_error"] = err.Error()
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
		respondError(c, http.StatusConflict, "this environment has no AppServer attached")
		return 0, false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve environment endpoint")
		return 0, false
	}
	if endpoint == nil {
		respondError(c, http.StatusConflict, "the environment's AppServer has no Portainer endpoint yet")
		return 0, false
	}
	return *endpoint, true
}

// GetAppState returns live compose-stack + container state for a compose app.
// GET /projects/:projectId/environments/:envId/apps/:appName/state
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

	if stacks, err := h.portainer.ListStacks(c.Request.Context(), endpoint); err == nil {
		for i := range stacks {
			if stacks[i].Name == appName {
				resp["stack"] = stacks[i]
				resp["online"] = stacks[i].Status == 1
				break
			}
		}
	}

	containers, err := h.portainer.ListContainers(c.Request.Context(), endpoint, "com.docker.compose.project="+appName)
	if err != nil {
		resp["live_error"] = err.Error()
	} else {
		resp["containers"] = containers
	}
	c.JSON(http.StatusOK, resp)
}

// GetAppLogs proxies the last N lines of a container's logs (read-only).
// GET /projects/:projectId/environments/:envId/apps/:appName/logs?container=<id>&tail=N
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
