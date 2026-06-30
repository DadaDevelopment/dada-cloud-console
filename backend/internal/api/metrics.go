package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// metricSpec describes one chart's PromQL. %s is replaced by the escaped label
// value (vm_name for VM metrics, the dada_io_app label for container metrics).
// Keeping every query in one block makes label/PromQL tuning a one-place change
// (the cAdvisor label name and root-fs assumptions are the main uncertainties).
type metricSpec struct {
	key  string
	unit string
	expr string // contains exactly one %s for the escaped label value
}

var vmMetricSpecs = []metricSpec{
	{"cpu_pct", "%", `100 - (avg by (vm_name) (rate(node_cpu_seconds_total{vm_name="%s",mode="idle"}[5m])) * 100)`},
	{"mem_pct", "%", `(1 - (node_memory_MemAvailable_bytes{vm_name="%s"} / node_memory_MemTotal_bytes{vm_name="%s"})) * 100`},
	{"disk_pct", "%", `(1 - (node_filesystem_avail_bytes{vm_name="%s",mountpoint="/",fstype!~"tmpfs|overlay|squashfs"} / node_filesystem_size_bytes{vm_name="%s",mountpoint="/",fstype!~"tmpfs|overlay|squashfs"})) * 100`},
	{"net_rx", "B/s", `sum by (vm_name) (rate(node_network_receive_bytes_total{vm_name="%s",device!~"lo|veth.*|docker.*|br-.*"}[5m]))`},
	{"net_tx", "B/s", `sum by (vm_name) (rate(node_network_transmit_bytes_total{vm_name="%s",device!~"lo|veth.*|docker.*|br-.*"}[5m]))`},
}

// Container metrics are keyed by the docker-compose project label, which equals
// the app/stack name (same label GetAppState filters containers by:
// com.docker.compose.project). Verified live: the `dada_io_app` label the
// bootstrap relabel expected is empty on real VMs, whereas
// container_label_com_docker_compose_project carries the stack name.
var containerMetricSpecs = []metricSpec{
	{"cpu_cores", "cores", `sum by (container_label_com_docker_compose_project) (rate(container_cpu_usage_seconds_total{container_label_com_docker_compose_project="%s"}[5m]))`},
	{"mem_bytes", "B", `sum by (container_label_com_docker_compose_project) (container_memory_working_set_bytes{container_label_com_docker_compose_project="%s"})`},
}

// k8sContainerMetricSpecs key container metrics by namespace + the exact image
// the status reconciler recorded on the App snapshot. Image is unique per app
// (profi vs profi-backend differ), so it isolates one app's pods without relying
// on pod/container naming conventions or pod-label joins (kube_pod_labels does
// not carry dada.io/app here). Each expr takes (namespace, image).
var k8sContainerMetricSpecs = []metricSpec{
	{"cpu_cores", "cores", `sum(rate(container_cpu_usage_seconds_total{namespace="%s",image="%s",container!=""}[5m]))`},
	{"mem_bytes", "B", `sum(container_memory_working_set_bytes{namespace="%s",image="%s",container!=""})`},
}

// runK8sContainerMetrics runs the namespace+image-scoped container queries and
// assembles the same response shape as runMetricSpecs (partial results on error).
func (h *Handler) runK8sContainerMetrics(ctx context.Context, namespace, image string, start, end time.Time, step time.Duration) gin.H {
	ns := prometheus.EscapeLabelValue(namespace)
	img := prometheus.EscapeLabelValue(image)
	metrics := gin.H{}
	var liveErr string
	for _, s := range k8sContainerMetricSpecs {
		series, err := h.prometheus.QueryRange(ctx, fmt.Sprintf(s.expr, ns, img), start, end, step, "")
		if err != nil {
			if liveErr == "" {
				liveErr = err.Error()
			}
			metrics[s.key] = gin.H{"unit": s.unit, "series": []prometheus.Point{}}
			continue
		}
		points := []prometheus.Point{}
		if len(series) > 0 {
			points = series[0].Points
		}
		metrics[s.key] = gin.H{"unit": s.unit, "series": points}
	}
	resp := gin.H{"range": end.Sub(start).String(), "step": step.String(), "metrics": metrics}
	if liveErr != "" {
		resp["live_error"] = liveErr
	}
	return resp
}

// countPlaceholders returns how many %s the expr expects so we can supply the
// label value the right number of times.
func fillExpr(expr, label string) string {
	n := 0
	for i := 0; i+1 < len(expr); i++ {
		if expr[i] == '%' && expr[i+1] == 's' {
			n++
		}
	}
	args := make([]any, n)
	for i := range args {
		args[i] = label
	}
	return fmt.Sprintf(expr, args...)
}

// parseRange resolves the ?range / ?step query params into a time window.
func parseRange(c *gin.Context) (start, end time.Time, step time.Duration) {
	end = time.Now()
	dur := time.Hour
	switch c.Query("range") {
	case "15m":
		dur = 15 * time.Minute
	case "1h", "":
		dur = time.Hour
	case "6h":
		dur = 6 * time.Hour
	case "24h":
		dur = 24 * time.Hour
	}
	step = 60 * time.Second
	if dur >= 6*time.Hour {
		step = 5 * time.Minute
	}
	if dur >= 24*time.Hour {
		step = 15 * time.Minute
	}
	return end.Add(-dur), end, step
}

// runMetricSpecs executes the given specs for one label value and assembles the
// response payload, recording the first query error in live_error (partial
// results still return, mirroring GetAppState).
func (h *Handler) runMetricSpecs(ctx context.Context, specs []metricSpec, label string, start, end time.Time, step time.Duration) gin.H {
	escaped := prometheus.EscapeLabelValue(label)
	metrics := gin.H{}
	var liveErr string
	for _, s := range specs {
		series, err := h.prometheus.QueryRange(ctx, fillExpr(s.expr, escaped), start, end, step, "")
		if err != nil {
			if liveErr == "" {
				liveErr = err.Error()
			}
			metrics[s.key] = gin.H{"unit": s.unit, "series": []prometheus.Point{}}
			continue
		}
		points := []prometheus.Point{}
		if len(series) > 0 {
			points = series[0].Points // single aggregated series per spec
		}
		metrics[s.key] = gin.H{"unit": s.unit, "series": points}
	}
	resp := gin.H{
		"range":   end.Sub(start).String(),
		"step":    step.String(),
		"metrics": metrics,
	}
	if liveErr != "" {
		resp["live_error"] = liveErr
	}
	return resp
}

// GetAppServerMetrics returns VM resource metrics (CPU/RAM/disk/network) from
// the central Prometheus, keyed by the app server's name (== vm_name label).
// GET /projects/:projectId/app-servers/:serverName/metrics?range=1h
//
// @ID          getAppServerMetrics
// @Summary     Get VM resource metrics for an app server
// @Description Returns time-series CPU, memory, disk and network metrics for an app server (VM) from the central Prometheus. Read-only. The range query param selects the window (15m, 1h, 6h, 24h; default 1h).
// @Tags        appserver
// @Produce     json
// @Security    BearerAuth
// @Param       projectId  path     string true  "Project UUID"
// @Param       serverName path     string true  "App server name"
// @Param       range      query    string false "Time window: 15m, 1h, 6h or 24h (default 1h)"
// @Success     200        {object} map[string]interface{} "object with range, step and metrics series"
// @Failure     401        {object} map[string]string
// @Failure     404        {object} map[string]string
// @Failure     503        {object} map[string]string
// @Router      /projects/{projectId}/app-servers/{serverName}/metrics [get]
func (h *Handler) GetAppServerMetrics(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	if !h.requireProjectMember(c, projectID) {
		return
	}
	serverName := c.Param("serverName")

	// Verify the server belongs to this project (scopes the vm_name label).
	var exists bool
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM app_servers WHERE project_id = $1 AND name = $2)`,
		projectID, serverName,
	).Scan(&exists)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load app server")
		return
	}
	if !exists {
		respondNotFound(c)
		return
	}

	if h.prometheus == nil {
		respondError(c, http.StatusServiceUnavailable, "metrics not configured")
		return
	}

	start, end, step := parseRange(c)
	c.JSON(http.StatusOK, h.runMetricSpecs(c.Request.Context(), vmMetricSpecs, serverName, start, end, step))
}

// GetAppMetrics returns container resource metrics (CPU/RAM) for a compose app
// from the central Prometheus, keyed by the dada_io_app container label.
// GET /projects/:projectId/environments/:envId/apps/:appName/metrics?range=1h
//
// @ID          getAppMetrics
// @Summary     Get container resource metrics for an app
// @Description Returns time-series CPU and memory metrics for a compose app's containers from the central Prometheus. Read-only. The range query param selects the window (15m, 1h, 6h, 24h; default 1h).
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       envId     path     string true  "Environment UUID"
// @Param       appName   path     string true  "App name"
// @Param       range     query    string false "Time window: 15m, 1h, 6h or 24h (default 1h)"
// @Success     200       {object} map[string]interface{} "object with range, step and metrics series"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/metrics [get]
func (h *Handler) GetAppMetrics(c *gin.Context) {
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

	// Load the app's runtime + namespace + reconciler-recorded image. The JOIN
	// also proves the App exists in this project/environment (404 otherwise).
	var runtime, namespace, image string
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT e.runtime, e.namespace, COALESCE(rs.summary_json->>'image', '')
		 FROM environments e
		 JOIN resource_snapshots rs
		   ON rs.environment_id = e.id AND rs.kind = 'App' AND rs.name = $3
		 WHERE e.id = $2 AND rs.project_id = $1`,
		projectID, envID, appName,
	).Scan(&runtime, &namespace, &image)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load app")
		return
	}

	if h.prometheus == nil {
		respondError(c, http.StatusServiceUnavailable, "metrics not configured")
		return
	}

	start, end, step := parseRange(c)
	// k8s apps: scope by namespace + image (cAdvisor labels). Compose/VM apps:
	// fall back to the docker-compose project label.
	if runtime == "k8s" && namespace != "" && image != "" {
		c.JSON(http.StatusOK, h.runK8sContainerMetrics(c.Request.Context(), namespace, image, start, end, step))
		return
	}
	c.JSON(http.StatusOK, h.runMetricSpecs(c.Request.Context(), containerMetricSpecs, appName, start, end, step))
}
