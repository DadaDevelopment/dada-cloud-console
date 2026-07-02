package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// SearchLogs runs an aggregated Elasticsearch log search scoped to the caller's
// project. At least one of ?vm / ?app is required and must belong to the
// project — this prevents reading another tenant's logs by guessing labels.
// GET /projects/:projectId/logs?vm=<server>&app=<app>&q=<text>&since=1h&size=200
//
// @ID          searchLogs
// @Summary     Search aggregated logs in a project
// @Description Runs an aggregated Elasticsearch log search scoped to the project. Read-only. At least one of vm or app is required and must belong to the project (prevents reading another tenant's logs). q is a free-text filter; since selects the window (15m, 1h, 6h, 24h, 7d; default 1h); size caps results (1-1000, default 200).
// @Tags        observability
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       vm        query    string false "App server (VM) name to scope logs to"
// @Param       app       query    string false "App name to scope logs to"
// @Param       q         query    string false "Free-text search query"
// @Param       since     query    string false "Time window: 15m, 1h, 6h, 24h or 7d (default 1h)"
// @Param       size      query    int    false "Max log entries to return (1-1000, default 200)"
// @Success     200       {object} map[string]interface{} "object with a log entries array"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/logs [get]
func (h *Handler) SearchLogs(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	if !h.requireProjectMember(c, projectID) {
		return
	}

	if h.logsearch == nil {
		respondError(c, http.StatusServiceUnavailable, "log search not configured")
		return
	}

	vm := c.Query("vm")
	app := c.Query("app")
	if vm == "" && app == "" {
		respondError(c, http.StatusBadRequest, "at least one of vm or app query param is required")
		return
	}

	// Authorization scoping: every requested label must belong to this project.
	if vm != "" {
		var ok bool
		if err := h.pool.QueryRow(c.Request.Context(),
			`SELECT EXISTS(SELECT 1 FROM app_servers WHERE project_id = $1 AND name = $2)`,
			projectID, vm,
		).Scan(&ok); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to verify app server")
			return
		}
		if !ok {
			respondError(c, http.StatusForbidden, "app server not in project")
			return
		}
	}
	if app != "" {
		var ok bool
		if err := h.pool.QueryRow(c.Request.Context(),
			`SELECT EXISTS(SELECT 1 FROM resource_snapshots
			 WHERE project_id = $1 AND kind = 'App' AND name = $2)`,
			projectID, app,
		).Scan(&ok); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to verify app")
			return
		}
		if !ok {
			respondError(c, http.StatusForbidden, "app not in project")
			return
		}
	}

	since := time.Hour
	switch c.Query("since") {
	case "15m":
		since = 15 * time.Minute
	case "1h", "":
		since = time.Hour
	case "6h":
		since = 6 * time.Hour
	case "24h":
		since = 24 * time.Hour
	case "7d":
		since = 7 * 24 * time.Hour
	}

	size := 200
	if s := c.Query("size"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 1000 {
			size = n
		}
	}

	result, err := h.logsearch.Search(c.Request.Context(), logsearch.SearchOpts{
		VMName: vm,
		App:    app,
		Query:  c.Query("q"),
		Since:  time.Now().Add(-since),
		Size:   size,
	})
	if err != nil {
		respondError(c, http.StatusBadGateway, "log search failed: "+err.Error())
		return
	}

	// Native (k8s) apps: their pod stdout never reaches the user stream above —
	// it lands in the infra stream (in-cluster filebeat). Query it as a second
	// source, scoped hard by the environments' namespaces resolved from the DB
	// (never from user input) so a same-named app in another tenant can't leak.
	// Infra-stream failures are non-fatal: user-stream results still return.
	if app != "" && h.infraLogsearch != nil {
		namespaces, nsErr := h.k8sAppNamespaces(c.Request.Context(), projectID, app)
		if nsErr != nil {
			log.Warn().Err(nsErr).Str("app", app).Msg("logs: resolving k8s namespaces")
		} else if len(namespaces) > 0 {
			infra, infraErr := h.infraLogsearch.Search(c.Request.Context(), logsearch.SearchOpts{
				KubeApp:        app,
				KubeNamespaces: namespaces,
				Query:          c.Query("q"),
				Since:          time.Now().Add(-since),
				Size:           size,
			})
			if infraErr != nil {
				log.Warn().Err(infraErr).Str("app", app).Msg("logs: infra stream search")
			} else {
				result = mergeLogResults(result, infra, size)
			}
		}
	}

	if result.Entries == nil {
		result.Entries = []logsearch.LogEntry{}
	}
	c.JSON(http.StatusOK, result)
}

// k8sAppNamespaces returns the namespaces of the k8s environments where an App
// snapshot with this name exists in the project. Empty for VM-only apps.
func (h *Handler) k8sAppNamespaces(ctx context.Context, projectID uuid.UUID, app string) ([]string, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT DISTINCT e.namespace FROM resource_snapshots rs
		 JOIN environments e ON e.id = rs.environment_id
		 WHERE rs.project_id = $1 AND rs.kind = 'App' AND rs.name = $2
		   AND e.runtime = 'k8s' AND e.namespace <> ''`,
		projectID, app,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var namespaces []string
	for rows.Next() {
		var ns string
		if err := rows.Scan(&ns); err != nil {
			return nil, err
		}
		namespaces = append(namespaces, ns)
	}
	return namespaces, rows.Err()
}

// mergeLogResults combines the user-stream and infra-stream responses into one
// newest-first list capped at size. Timestamps are ES @timestamp strings
// (RFC3339, zero-padded UTC) so lexicographic order is chronological.
func mergeLogResults(a, b *logsearch.SearchResult, size int) *logsearch.SearchResult {
	out := &logsearch.SearchResult{
		Total:   a.Total + b.Total,
		Entries: append(append([]logsearch.LogEntry{}, a.Entries...), b.Entries...),
	}
	sort.SliceStable(out.Entries, func(i, j int) bool {
		return out.Entries[i].Timestamp > out.Entries[j].Timestamp
	})
	if len(out.Entries) > size {
		out.Entries = out.Entries[:size]
	}
	return out
}
