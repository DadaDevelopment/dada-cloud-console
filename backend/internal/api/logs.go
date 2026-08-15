package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/dada-tuda/console/backend/internal/cache"
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
	result, _, _, _, ok := h.runLogSearch(c, 1000, 200)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, result)
}

// DownloadLogs runs the same aggregated search as SearchLogs but returns the
// matched entries as a plain-text .log file attachment instead of JSON, so
// users can save a time range of logs the way ArgoCD's log download does.
// GET /projects/:projectId/logs/download?vm=<server>&app=<app>&q=<text>&since=1h
//
// @ID          downloadLogs
// @Summary     Download aggregated logs in a project as a .log file
// @Description Same scoping/filters as GET /logs, but streams the matched entries as a downloadable text/plain .log file (one line per entry, newest first) instead of JSON.
// @Tags        observability
// @Produce     plain
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       vm        query    string false "App server (VM) name to scope logs to"
// @Param       app       query    string false "App name to scope logs to"
// @Param       q         query    string false "Free-text search query"
// @Param       since     query    string false "Time window: 15m, 1h, 6h, 24h or 7d (default 1h)"
// @Success     200       {file}   file "text/plain .log file"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/logs/download [get]
func (h *Handler) DownloadLogs(c *gin.Context) {
	result, vm, app, sinceParam, ok := h.runLogSearch(c, 10000, 10000)
	if !ok {
		return
	}

	scope := app
	if scope == "" {
		scope = vm
	}
	filename := fmt.Sprintf("%s-%s.log", scope, sinceParam)

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Content-Disposition", contentDispositionAttachment(filename))
	for _, e := range result.Entries {
		stream := e.Stream
		if stream == "" {
			stream = "stdout"
		}
		fmt.Fprintf(c.Writer, "%s %s %s\n", e.Timestamp, stream, e.Message)
	}
}

// runLogSearch validates the vm/app scoping, parses since/size, and runs the
// merged user+infra Elasticsearch search shared by SearchLogs and
// DownloadLogs. maxSize caps the requested ?size param; defaultSize is used
// when ?size is absent. ok is false if a response has already been written.
func (h *Handler) runLogSearch(c *gin.Context, maxSize, defaultSize int) (result *logsearch.SearchResult, vm, app, sinceParam string, ok bool) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return nil, "", "", "", false
	}
	if !h.requireProjectMember(c, projectID) {
		return nil, "", "", "", false
	}

	if h.logsearch == nil {
		respondError(c, http.StatusServiceUnavailable, "log search not configured")
		return nil, "", "", "", false
	}

	vm = c.Query("vm")
	app = c.Query("app")
	if vm == "" && app == "" {
		respondError(c, http.StatusBadRequest, "at least one of vm or app query param is required")
		return nil, "", "", "", false
	}

	// Authorization scoping: every requested label must belong to this project.
	if vm != "" {
		var exists bool
		if err := h.pool.QueryRow(c.Request.Context(),
			`SELECT EXISTS(SELECT 1 FROM app_servers WHERE project_id = $1 AND name = $2)`,
			projectID, vm,
		).Scan(&exists); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to verify app server")
			return nil, "", "", "", false
		}
		if !exists {
			respondError(c, http.StatusForbidden, "app server not in project")
			return nil, "", "", "", false
		}
	}
	if app != "" {
		var exists bool
		if err := h.pool.QueryRow(c.Request.Context(),
			`SELECT EXISTS(SELECT 1 FROM resource_snapshots
			 WHERE project_id = $1 AND kind = 'App' AND name = $2)`,
			projectID, app,
		).Scan(&exists); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to verify app")
			return nil, "", "", "", false
		}
		if !exists {
			respondError(c, http.StatusForbidden, "app not in project")
			return nil, "", "", "", false
		}
	}

	sinceParam = c.Query("since")
	since := time.Hour
	switch sinceParam {
	case "15m":
		since = 15 * time.Minute
	case "1h", "":
		sinceParam = "1h"
		since = time.Hour
	case "6h":
		since = 6 * time.Hour
	case "24h":
		since = 24 * time.Hour
	case "7d":
		since = 7 * 24 * time.Hour
	}

	size := defaultSize
	if s := c.Query("size"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= maxSize {
			size = n
		}
	}

	ctx := c.Request.Context()
	q := c.Query("q")
	sinceTime := time.Now().Add(-since)

	key := "logs:search:" + projectID.String() + ":" + c.Request.URL.RawQuery
	res, err := cache.Fetch(ctx, h.cache, key, h.cfg.CacheLogsTTL,
		func() (*logsearch.SearchResult, error) {
			var (
				wg       sync.WaitGroup
				userRes  *logsearch.SearchResult
				userErr  error
				infraRes *logsearch.SearchResult
			)

			wg.Add(1)
			go func() {
				defer wg.Done()
				userRes, userErr = h.logsearch.Search(ctx, logsearch.SearchOpts{
					VMName: vm,
					App:    app,
					Query:  q,
					Since:  sinceTime,
					Size:   size,
				})
			}()

			if app != "" && h.infraLogsearch != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					namespaces, nsErr := h.k8sAppNamespaces(ctx, projectID, app)
					if nsErr != nil {
						log.Warn().Err(nsErr).Str("app", app).Msg("logs: resolving k8s namespaces")
						return
					}
					if len(namespaces) == 0 {
						return
					}
					infra, infraErr := h.infraLogsearch.Search(ctx, logsearch.SearchOpts{
						KubeApp:        app,
						KubeNamespaces: namespaces,
						Query:          q,
						Since:          sinceTime,
						Size:           size,
					})
					if infraErr != nil {
						log.Warn().Err(infraErr).Str("app", app).Msg("logs: infra stream search")
						return
					}
					infraRes = infra
				}()
			}

			wg.Wait()

			if userErr != nil {
				return nil, userErr
			}
			res := userRes
			if infraRes != nil {
				res = mergeLogResults(res, infraRes, size)
			}
			return res, nil
		})
	if err != nil {
		respondError(c, http.StatusBadGateway, "log search failed: "+err.Error())
		return nil, "", "", "", false
	}

	if res.Entries == nil {
		res.Entries = []logsearch.LogEntry{}
	}
	return res, vm, app, sinceParam, true
}

// k8sAppNamespaces returns the namespaces to search infra logs in for an App:
// the namespaces of the k8s environments holding a snapshot of that name, plus
// the namespaces the status reconciler actually observed the workloads running
// in. The two differ for adopted ArgoCD apps (ADR-013), which are filed under a
// project environment while their pods live elsewhere — searching only the
// environment namespace returns zero hits for an app that logs constantly.
// Empty for VM-only apps.
func (h *Handler) k8sAppNamespaces(ctx context.Context, projectID uuid.UUID, app string) ([]string, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT DISTINCT ns FROM (
		   SELECT e.namespace AS ns
		     FROM resource_snapshots rs
		     JOIN environments e ON e.id = rs.environment_id
		    WHERE rs.project_id = $1 AND rs.kind = 'App' AND rs.name = $2
		      AND e.runtime = 'k8s' AND e.namespace <> ''
		   UNION
		   SELECT jsonb_array_elements_text(rs.summary_json->'namespaces') AS ns
		     FROM resource_snapshots rs
		    WHERE rs.project_id = $1 AND rs.kind = 'App' AND rs.name = $2
		      AND jsonb_typeof(rs.summary_json->'namespaces') = 'array'
		 ) u WHERE ns <> ''`,
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
