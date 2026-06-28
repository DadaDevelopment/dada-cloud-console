package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/grafanaembed"
	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Monitoring resource read/health/dashboard layer (ADR-011). The ingestion chip
// owns the monitoring_apps table, its API key, and the metric/log write path;
// this file owns reading those back: health, native metric/log panels, and the
// Grafana deep link. Series + log docs are tagged by the ingest path with the
// labels defined in monitoringLabels — read and write MUST agree on them.

// monitoringLabels are the canonical series/log labels for a monitoring resource.
// They MUST match exactly what the ingest path stamps (gateway resolver / api
// ingest): project_id = project UUID, monitoring_app = the resource NAME (not its
// UUID), org_id = the project owner UUID or "" when the project has no owner.
// promSelector drops empty values, so an empty org_id selects the same series the
// gateway wrote (which also omits org_id when owner is nil). Using the app UUID or
// a project_id fallback for org_id here would select zero series — the ingest path
// never writes those values. Trade-off: monitoring_app = name means a rename
// re-points reads at the new name; acceptable until ingest stamps a stable id.
func monitoringLabels(orgID string, app *models.MonitoringApp) map[string]string {
	return map[string]string{
		"org_id":         orgID,
		"project_id":     app.ProjectID.String(),
		"monitoring_app": app.Name,
	}
}

// monitoringOrgLabel returns the org_id label value used to scope a project's
// metrics/logs on read. It MUST match what the ingest path writes
// (loadIngestTarget labels series with the project's owner_id), so reads and
// writes select the same series. Returns "" when unknown — callers fall back to
// project_id via monitoringLabels. Replaces the old per-request claims.OrgID,
// which no longer exists under native multi-org claims (ADR-009).
func (h *Handler) monitoringOrgLabel(ctx context.Context, projectID uuid.UUID) string {
	var owner *uuid.UUID
	if err := h.pool.QueryRow(ctx,
		`SELECT owner_id FROM projects WHERE id = $1`, projectID,
	).Scan(&owner); err != nil || owner == nil {
		return ""
	}
	return owner.String()
}

// promSelector renders the labels as a PromQL stream selector `{k="v",...}` with
// values escaped. Keys are emitted in a fixed order for stable queries.
func promSelector(labels map[string]string) string {
	order := []string{"org_id", "project_id", "monitoring_app"}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		if v, ok := labels[k]; ok && v != "" {
			parts = append(parts, fmt.Sprintf(`%s="%s"`, k, prometheus.EscapeLabelValue(v)))
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// loadMonitoringApp loads one monitoring resource scoped to project+environment.
// Returns pgx.ErrNoRows when absent (handler maps to 404).
func (h *Handler) loadMonitoringApp(ctx context.Context, projectID, envID, appID uuid.UUID) (*models.MonitoringApp, error) {
	var a models.MonitoringApp
	var folder, dashboard *string
	err := h.pool.QueryRow(ctx,
		`SELECT id, project_id, environment_id, name,
		        COALESCE(grafana_folder_uid, ''), COALESCE(grafana_dashboard_uid, ''), created_at
		   FROM monitoring_apps
		  WHERE id = $1 AND project_id = $2 AND environment_id = $3`,
		appID, projectID, envID,
	).Scan(&a.ID, &a.ProjectID, &a.EnvironmentID, &a.Name, &folder, &dashboard, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	if folder != nil {
		a.GrafanaFolderUID = *folder
	}
	if dashboard != nil {
		a.GrafanaDashboardUID = *dashboard
	}
	a.UpdatedAt = a.CreatedAt
	return &a, nil
}

// resolveMonitoringApp parses the path params, checks membership + env scoping,
// and loads the resource. On any failure it writes the response and returns nil.
func (h *Handler) resolveMonitoringApp(c *gin.Context) (*models.MonitoringApp, uuid.UUID, uuid.UUID) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return nil, uuid.Nil, uuid.Nil
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return nil, uuid.Nil, uuid.Nil
	}
	if !h.requireProjectMember(c, projectID) {
		return nil, uuid.Nil, uuid.Nil
	}
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return nil, uuid.Nil, uuid.Nil
	} else if !ok {
		respondNotFound(c)
		return nil, uuid.Nil, uuid.Nil
	}
	appID, err := uuid.Parse(c.Param("appId"))
	if err != nil {
		respondNotFound(c)
		return nil, uuid.Nil, uuid.Nil
	}
	app, err := h.loadMonitoringApp(c.Request.Context(), projectID, envID, appID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return nil, uuid.Nil, uuid.Nil
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load monitoring resource")
		return nil, uuid.Nil, uuid.Nil
	}
	return app, projectID, envID
}

// GetMonitoringApp returns one monitoring resource (detail page header).
//
// @ID          getMonitoringApp
// @Summary     Get a monitoring resource
// @Description Returns one monitoring resource (ingest target) scoped to a project + environment. Read-only.
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appId     path     string true "Monitoring resource UUID"
// @Success     200       {object} map[string]interface{} "object with the app"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId} [get]
func (h *Handler) GetMonitoringApp(c *gin.Context) {
	app, _, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"app": app})
}

// GetMonitoringHealth computes the resource health (ADR-011 §7). Inputs:
// last-seen (Prometheus + ES), ERROR-log rate over 15m (ES), firing Grafana
// alerts. Every external input is best-effort: a missing/erroring source
// degrades the signal (reason recorded) rather than failing the whole call.
//
// @ID          getMonitoringHealth
// @Summary     Get monitoring resource health
// @Description Computes resource health (healthy/degraded/down/unknown + critical) from last-seen, ERROR-log rate, and firing Grafana alerts. Read-only.
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appId     path     string true "Monitoring resource UUID"
// @Success     200       {object} map[string]interface{} "health status"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId}/health [get]
func (h *Handler) GetMonitoringHealth(c *gin.Context) {
	app, _, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	orgID := h.monitoringOrgLabel(c.Request.Context(), app.ProjectID)
	cfg := h.loadHealthConfig(c.Request.Context(), app.ID)
	status := h.computeHealth(c.Request.Context(), app, orgID, cfg)
	c.JSON(http.StatusOK, status)
}

// loadHealthConfig reads per-resource thresholds, applying code defaults for any
// unset field. Defaults: down after 5m of silence, degraded above 10 ERRORs/15m.
func (h *Handler) loadHealthConfig(ctx context.Context, appID uuid.UUID) models.HealthConfig {
	cfg := models.HealthConfig{DownAfterSeconds: 300, ErrorThreshold15m: 10}
	var raw models.HealthConfig
	if err := h.pool.QueryRow(ctx,
		`SELECT health_config FROM monitoring_apps WHERE id = $1`, appID,
	).Scan(&raw); err == nil {
		if raw.DownAfterSeconds > 0 {
			cfg.DownAfterSeconds = raw.DownAfterSeconds
		}
		if raw.ErrorThreshold15m > 0 {
			cfg.ErrorThreshold15m = raw.ErrorThreshold15m
		}
	}
	return cfg
}

// computeHealth merges the three signals into a state + critical flag.
func (h *Handler) computeHealth(ctx context.Context, app *models.MonitoringApp, orgID string, cfg models.HealthConfig) models.HealthStatus {
	labels := monitoringLabels(orgID, app)
	now := time.Now()
	st := models.HealthStatus{State: models.HealthUnknown, Reasons: []string{}}

	lastSeen := h.lastSeen(ctx, app, labels)
	st.LastSeen = lastSeen

	st.ErrorRate15m = h.errorCount(ctx, app, labels, now.Add(-15*time.Minute), now)

	if h.grafana != nil {
		if n, err := h.grafana.FiringAlerts(ctx, map[string]string{
			"project_id":     labels["project_id"],
			"monitoring_app": labels["monitoring_app"],
		}); err == nil {
			st.FiringAlerts = n
		} else {
			st.Reasons = append(st.Reasons, "alert state unavailable: "+err.Error())
		}
	}
	st.Critical = st.FiringAlerts > 0

	switch {
	case lastSeen == nil:
		st.State = models.HealthUnknown
		st.Reasons = append(st.Reasons, "no metrics or logs ever received")
	case now.Sub(*lastSeen) > time.Duration(cfg.DownAfterSeconds)*time.Second:
		st.State = models.HealthDown
		st.Reasons = append(st.Reasons, fmt.Sprintf("no data for %s (down after %ds)",
			now.Sub(*lastSeen).Truncate(time.Second), cfg.DownAfterSeconds))
	case st.ErrorRate15m > cfg.ErrorThreshold15m:
		st.State = models.HealthDegraded
		st.Reasons = append(st.Reasons, fmt.Sprintf("%d ERROR logs in last 15m (threshold %d)",
			st.ErrorRate15m, cfg.ErrorThreshold15m))
	default:
		st.State = models.HealthHealthy
	}
	if st.Critical {
		st.Reasons = append(st.Reasons, fmt.Sprintf("%d firing Grafana alert(s)", st.FiringAlerts))
	}
	return st
}

// lastSeen returns the most recent metric or log timestamp for the resource, or
// nil if it has never reported. Errors in either source are swallowed (treated
// as "no data from that source").
func (h *Handler) lastSeen(ctx context.Context, app *models.MonitoringApp, labels map[string]string) *time.Time {
	var newest *time.Time

	if h.prometheus != nil {
		// Real freshest-sample timestamp across all of the resource's metric series,
		// over a 24h lookback. Three constraints force this exact shape:
		//   - timestamp() reads the true sample time only off a raw selector (it
		//     returns the *eval* time when wrapped around last_over_time, so the old
		//     last_over_time form reported "now" for any data in-window — wrong).
		//   - a raw instant selector only sees samples inside Prometheus' 5m
		//     staleness window, so a [24h:1m] subquery scans the whole day and
		//     max_over_time keeps the latest real sample time.
		//   - timestamp() strips __name__, collapsing cpu/memory/temp to one
		//     identical labelset → "vector cannot contain metrics with the same
		//     labelset" and the whole query errored (→ nil → every multi-metric
		//     resource showed "unknown"). label_replace stashes __name__ in a side
		//     label first so the series stay distinct; max() then folds them to the
		//     single freshest timestamp.
		expr := fmt.Sprintf(
			`max(max_over_time(timestamp(label_replace(%s, "mname", "$1", "__name__", "(.+)"))[24h:1m]))`,
			promSelector(labels),
		)
		if samples, err := h.prometheus.QueryInstant(ctx, expr, time.Now()); err == nil {
			for _, s := range samples {
				ts := time.Unix(int64(s.Point.V), 0).UTC()
				if newest == nil || ts.After(*newest) {
					t := ts
					newest = &t
				}
			}
		}
	}

	if h.appLogsearch != nil {
		if res, err := h.appLogsearch.Search(ctx, logsearch.SearchOpts{
			ProjectID:     labels["project_id"],
			MonitoringApp: labels["monitoring_app"],
			Size:          1,
		}); err == nil && len(res.Entries) > 0 {
			if ts, perr := time.Parse(time.RFC3339, res.Entries[0].Timestamp); perr == nil {
				if newest == nil || ts.After(*newest) {
					newest = &ts
				}
			}
		}
	}
	return newest
}

// errorCount counts ERROR-level app logs in the window. Returns 0 when ES is
// unconfigured or the query errors.
func (h *Handler) errorCount(ctx context.Context, app *models.MonitoringApp, labels map[string]string, since, until time.Time) int {
	if h.appLogsearch == nil {
		return 0
	}
	res, err := h.appLogsearch.Search(ctx, logsearch.SearchOpts{
		ProjectID:     labels["project_id"],
		MonitoringApp: labels["monitoring_app"],
		Level:         "ERROR",
		Since:         since,
		Until:         until,
		Size:          1, // we only need Total
	})
	if err != nil {
		return 0
	}
	return res.Total
}

// GetMonitoringMetrics returns native metric panels for the resource. Metric
// names are discovered from the series labels (label-driven), so custom metrics
// render with no code change. Each metric is averaged across sources.
//
// @ID          getMonitoringMetrics
// @Summary     Get monitoring resource metrics
// @Description Returns native metric panels for the resource. Metric names are discovered from series labels, so custom metrics render with no code change. Read-only. Returns 503 when Prometheus is not configured.
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       envId     path     string true  "Environment UUID"
// @Param       appId     path     string true  "Monitoring resource UUID"
// @Param       range     query    string false "Time range (e.g. 15m, 1h, 6h, 24h)"
// @Success     200       {object} map[string]interface{} "metrics by name"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId}/metrics [get]
func (h *Handler) GetMonitoringMetrics(c *gin.Context) {
	app, _, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	if h.prometheus == nil {
		respondError(c, http.StatusServiceUnavailable, "metrics not configured")
		return
	}
	ctx := c.Request.Context()
	orgID := h.monitoringOrgLabel(ctx, app.ProjectID)
	labels := monitoringLabels(orgID, app)
	sel := promSelector(labels)

	start, end, step := parseRange(c)

	// Discover metric names present in the requested window. last_over_time keeps a
	// metric whose newest sample is older than the instant-query staleness window
	// (5m) but still inside the range, so sparsely-written custom metrics surface.
	window := fmt.Sprintf("%ds", int(end.Sub(start).Seconds()))
	names := []string{}
	discover := fmt.Sprintf("group by (__name__) (last_over_time(%s[%s]))", sel, window)
	if samples, err := h.prometheus.QueryInstant(ctx, discover, end); err == nil {
		for _, s := range samples {
			if n := s.Metric["__name__"]; n != "" {
				names = append(names, n)
			}
		}
	}

	metrics := gin.H{}
	var liveErr string
	for _, name := range names {
		expr := fmt.Sprintf("avg(%s%s)", name, sel)
		series, err := h.prometheus.QueryRange(ctx, expr, start, end, step)
		if err != nil {
			if liveErr == "" {
				liveErr = err.Error()
			}
			continue
		}
		points := []prometheus.Point{}
		if len(series) > 0 {
			points = series[0].Points
		}
		metrics[name] = gin.H{"unit": "", "series": points}
	}
	resp := gin.H{"range": end.Sub(start).String(), "step": step.String(), "metrics": metrics}
	if liveErr != "" {
		resp["live_error"] = liveErr
	}
	c.JSON(http.StatusOK, resp)
}

// GetMonitoringLogs reads back the resource's app logs (dada-app-logs-*), scoped
// by labels. Mirrors SearchLogs' response shape so the existing LogsViewer works.
//
// @ID          getMonitoringLogs
// @Summary     Get monitoring resource logs
// @Description Reads back the resource's app logs (dada-app-logs-*), scoped by labels. Read-only. Returns 503 when log search is not configured.
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       envId     path     string true  "Environment UUID"
// @Param       appId     path     string true  "Monitoring resource UUID"
// @Param       range     query    string false "Time range (15m, 1h, 6h, 24h, 7d)"
// @Param       q         query    string false "Full-text query"
// @Param       level     query    string false "Log level filter (e.g. ERROR)"
// @Success     200       {object} map[string]interface{} "log search result"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId}/logs [get]
func (h *Handler) GetMonitoringLogs(c *gin.Context) {
	app, _, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	if h.appLogsearch == nil {
		respondError(c, http.StatusServiceUnavailable, "log search not configured")
		return
	}
	orgID := h.monitoringOrgLabel(c.Request.Context(), app.ProjectID)
	labels := monitoringLabels(orgID, app)

	since := time.Hour
	switch c.Query("range") {
	case "15m":
		since = 15 * time.Minute
	case "6h":
		since = 6 * time.Hour
	case "24h":
		since = 24 * time.Hour
	case "7d":
		since = 7 * 24 * time.Hour
	}
	res, err := h.appLogsearch.Search(c.Request.Context(), logsearch.SearchOpts{
		ProjectID:     labels["project_id"],
		MonitoringApp: labels["monitoring_app"],
		Query:         c.Query("q"),
		Level:         c.Query("level"),
		Since:         time.Now().Add(-since),
		Size:          300,
	})
	if err != nil {
		respondError(c, http.StatusBadGateway, "log search failed: "+err.Error())
		return
	}
	if res.Entries == nil {
		res.Entries = []logsearch.LogEntry{}
	}
	c.JSON(http.StatusOK, res)
}

// GetMonitoringGrafanaLink ensures the resource's folder+dashboard exist and
// returns the browser deep link.
//
// @ID          getMonitoringGrafanaLink
// @Summary     Get Grafana deep link for a monitoring resource
// @Description Ensures the resource's Grafana folder + dashboard exist and returns the browser deep link. Returns 503 when Grafana is not configured.
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appId     path     string true "Monitoring resource UUID"
// @Success     200       {object} map[string]interface{} "object with the url"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId}/grafana-link [get]
func (h *Handler) GetMonitoringGrafanaLink(c *gin.Context) {
	app, projectID, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	if h.grafana == nil {
		respondError(c, http.StatusServiceUnavailable, "grafana not configured")
		return
	}
	orgID := h.monitoringOrgLabel(c.Request.Context(), projectID)
	if err := h.ensureGrafanaResource(c.Request.Context(), app, projectID, orgID); err != nil {
		respondError(c, http.StatusBadGateway, "grafana provisioning failed: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": h.grafanaEmbedURL(c, app, projectID)})
}

// grafanaEmbedURL builds the iframe deep link. When GRAFANA_EMBED_SECRET is set,
// the URL carries a short-lived HMAC embed token scoped to the requesting console
// user and this dashboard; the grafana-embed-gateway (which fronts
// grafana.dada-tuda.ru) verifies it and injects Grafana auth.proxy identity
// headers so the iframe authenticates with NO manual Grafana login. The
// project_id group drives Grafana team-sync → per-project folder ACL, so a user
// can only reach folders for projects they belong to. Falls back to the plain
// deep link (manual login) when no secret is configured.
func (h *Handler) grafanaEmbedURL(c *gin.Context, app *models.MonitoringApp, projectID uuid.UUID) string {
	base := fmt.Sprintf("%s/d/%s?kiosk&theme=light", h.grafana.PublicURL(), app.GrafanaDashboardUID)
	secret := h.cfg.GrafanaEmbedSecret
	if secret == "" {
		return base
	}
	claims, ok := auth.GetClaims(c)
	if !ok {
		return base
	}
	user := claims.Username
	if user == "" {
		user = claims.Email
	}
	if user == "" {
		user = claims.UserID.String()
	}
	// Grant this user View on the project folder so Grafana (OSS, per-user ACL)
	// renders the dashboard once auth.proxy authenticates the iframe as this same
	// login. Best-effort: a grant failure shows a blank panel but must not block
	// the link (logged via the returned URL still working for already-granted users).
	if app.GrafanaFolderUID != "" {
		_ = h.grafana.EnsureUserFolderAccess(c.Request.Context(), app.GrafanaFolderUID, user, claims.Email, claims.DisplayName)
	}
	tok, err := grafanaembed.Sign([]byte(secret), grafanaembed.Claims{
		User:      user,
		Email:     claims.Email,
		Dashboard: app.GrafanaDashboardUID,
	}, time.Now(), 2*time.Minute)
	if err != nil {
		return base
	}
	return base + "&" + grafanaembed.QueryParam + "=" + url.QueryEscape(tok)
}

// folderUIDForProject / dashboardUIDForApp derive stable Grafana UIDs (<=40 chars)
// from the resource UUIDs so we never need a lookup table.
func folderUIDForProject(projectID uuid.UUID) string {
	return "dpr" + hex.EncodeToString(projectID[:])
}

func dashboardUIDForApp(appID uuid.UUID) string {
	return "dma" + hex.EncodeToString(appID[:])
}
