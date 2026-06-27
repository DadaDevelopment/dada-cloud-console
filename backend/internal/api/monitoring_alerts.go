package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/grafana"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Alerts, channels (Grafana contact points) and dashboard provisioning for the
// monitoring resource (ADR-011). Grafana stays the source of truth for alert
// evaluation/routing/dashboards; the monitoring_channels / monitoring_alert_rules
// tables are a best-effort Postgres mirror so the console renders a native list
// UI and computes health without round-tripping Grafana on every request.

// ---- shared helpers --------------------------------------------------------

// requireProjectWriter gates write surfaces: caller must be a project member AND
// hold a write-capable role (Owner/Admin/Developer). Writes the response and
// returns false on any failure.
func (h *Handler) requireProjectWriter(c *gin.Context, projectID uuid.UUID) bool {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return false
	}
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return false
	}
	if !canWrite(role) {
		respondForbidden(c)
		return false
	}
	return true
}

// resolveMonitoringProject parses projectId+envId, enforces membership (or write
// role when write=true) and that the env belongs to the project. Returns Nil ids
// and false (response already written) on failure.
func (h *Handler) resolveMonitoringProject(c *gin.Context, write bool) (uuid.UUID, uuid.UUID, bool) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, false
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, false
	}
	if write {
		if !h.requireProjectWriter(c, projectID) {
			return uuid.Nil, uuid.Nil, false
		}
	} else if !h.requireProjectMember(c, projectID) {
		return uuid.Nil, uuid.Nil, false
	}
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return uuid.Nil, uuid.Nil, false
	} else if !ok {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, envID, true
}

// receiverName is the deterministic Grafana contact-point (receiver) name a
// channel is provisioned under. Alert rules route by this name. Prefixed +
// channel-id keeps it unique inside the shared Grafana org.
func receiverName(channelID uuid.UUID) string {
	return "dch-" + channelID.String()
}

// ruleUIDForID derives a stable Grafana alert-rule uid (<=40 chars) from the
// mirror row id, so re-provisioning is idempotent.
func ruleUIDForID(ruleID uuid.UUID) string {
	return "dar" + hex.EncodeToString(ruleID[:])
}

// ---- dashboard / folder provisioning ---------------------------------------

// ensureGrafanaResource makes the project folder and the per-resource dashboard
// exist, then persists their UIDs on the monitoring_apps row. Idempotent: safe
// to call on every grafana-link / alert-rule request. Panels are discovered from
// the series labels so custom metrics show up with no code change.
func (h *Handler) ensureGrafanaResource(ctx context.Context, app *models.MonitoringApp, projectID uuid.UUID, orgID string) error {
	if h.grafana == nil {
		return fmt.Errorf("grafana not configured")
	}
	folderUID := app.GrafanaFolderUID
	if folderUID == "" {
		folderUID = folderUIDForProject(projectID)
	}
	dashUID := app.GrafanaDashboardUID
	if dashUID == "" {
		dashUID = dashboardUIDForApp(app.ID)
	}

	if err := h.grafana.EnsureFolder(ctx, folderUID, "Project "+projectID.String()); err != nil {
		return err
	}
	// Isolation baseline: strip the inherited Editor/Viewer role grants so the
	// folder is reachable only by users the console explicitly grants (per
	// requester, at grafana-link time — see grafanaEmbedURL/EnsureUserFolderAccess).
	// Best-effort; older Grafana may reject.
	_ = h.grafana.SetFolderTenant(ctx, folderUID)

	labels := monitoringLabels(orgID, app)
	sel := promSelector(labels)
	dash := grafana.BuildDashboard(dashUID, app.Name, h.cfg.GrafanaPromDatasourceUID, h.discoverPanels(ctx, sel))
	if err := h.grafana.UpsertDashboard(ctx, folderUID, dash); err != nil {
		return err
	}

	if app.GrafanaFolderUID != folderUID || app.GrafanaDashboardUID != dashUID {
		if _, err := h.pool.Exec(ctx,
			`UPDATE monitoring_apps SET grafana_folder_uid = $1, grafana_dashboard_uid = $2 WHERE id = $3`,
			folderUID, dashUID, app.ID,
		); err != nil {
			return err
		}
		app.GrafanaFolderUID = folderUID
		app.GrafanaDashboardUID = dashUID
	}
	return nil
}

// discoverPanels builds one timeseries panel per metric name present for the
// resource (label-driven). Empty when Prometheus is unconfigured or silent.
func (h *Handler) discoverPanels(ctx context.Context, sel string) []grafana.MetricPanel {
	panels := []grafana.MetricPanel{}
	if h.prometheus == nil {
		return panels
	}
	samples, err := h.prometheus.QueryInstant(ctx, "group by (__name__) (last_over_time("+sel+"[6h]))", time.Now())
	if err != nil {
		return panels
	}
	for _, s := range samples {
		if n := s.Metric["__name__"]; n != "" {
			panels = append(panels, grafana.MetricPanel{
				Title: n,
				Expr:  fmt.Sprintf("avg(%s%s)", n, sel),
			})
		}
	}
	return panels
}

// ---- channels (Grafana contact points) -------------------------------------

// createChannelReq is the union of per-type contact-point settings. Only the
// fields for the chosen type are read.
type createChannelReq struct {
	Name string `json:"name"`
	Type string `json:"type"` // telegram | email | webhook
	// telegram
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
	// email
	Addresses []string `json:"addresses"`
	// webhook
	URL string `json:"url"`
}

// ListChannels returns the project's notification channels (mirror rows).
//
// @ID          listMonitoringChannels
// @Summary     List notification channels
// @Description Lists the project's monitoring notification channels (Grafana contact points: telegram/email/webhook). Read-only.
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Success     200       {object} map[string]interface{} "object with channels"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/channels [get]
func (h *Handler) ListChannels(c *gin.Context) {
	projectID, _, ok := h.resolveMonitoringProject(c, false)
	if !ok {
		return
	}
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, name, type, COALESCE(grafana_contactpoint_uid, ''), created_at
		   FROM monitoring_channels WHERE project_id = $1 ORDER BY created_at`, projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list channels")
		return
	}
	defer rows.Close()
	out := []models.MonitoringChannel{}
	for rows.Next() {
		var ch models.MonitoringChannel
		if err := rows.Scan(&ch.ID, &ch.ProjectID, &ch.Name, &ch.Type, &ch.GrafanaContactpointUID, &ch.CreatedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan channel")
			return
		}
		out = append(out, ch)
	}
	c.JSON(http.StatusOK, gin.H{"channels": out})
}

// CreateChannel provisions a Grafana contact point and mirrors it.
//
// @ID          createMonitoringChannel
// @Summary     Create a notification channel
// @Description Provisions a Grafana contact point (telegram/email/webhook) and mirrors it. Write-gated (Owner/Admin/Developer). Returns 503 when Grafana is not configured.
// @Tags        monitoring
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                 true "Project UUID"
// @Param       envId     path     string                 true "Environment UUID"
// @Param       body      body     map[string]interface{} true "Channel: name, type, and type-specific settings"
// @Success     201       {object} map[string]interface{} "object with the channel"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/channels [post]
func (h *Handler) CreateChannel(c *gin.Context) {
	projectID, _, ok := h.resolveMonitoringProject(c, true)
	if !ok {
		return
	}
	if h.grafana == nil {
		respondError(c, http.StatusServiceUnavailable, "grafana not configured")
		return
	}
	var req createChannelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" {
		respondError(c, http.StatusBadRequest, "name is required")
		return
	}
	settings, err := channelSettings(req)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx := c.Request.Context()
	// Insert the mirror row first so the contact point can be named by its id.
	channelID := uuid.New()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO monitoring_channels (id, project_id, name, type) VALUES ($1, $2, $3, $4)`,
		channelID, projectID, req.Name, req.Type,
	); err != nil {
		respondError(c, http.StatusConflict, "channel name already exists or insert failed")
		return
	}

	uid, err := h.grafana.CreateContactPoint(ctx, grafana.ContactPoint{
		Name:     receiverName(channelID),
		Type:     req.Type,
		Settings: settings,
	})
	if err != nil {
		// Roll back the mirror row so we don't leave an unroutable channel.
		_, _ = h.pool.Exec(ctx, `DELETE FROM monitoring_channels WHERE id = $1`, channelID)
		respondError(c, http.StatusBadGateway, "grafana contact point failed: "+err.Error())
		return
	}
	if _, err := h.pool.Exec(ctx,
		`UPDATE monitoring_channels SET grafana_contactpoint_uid = $1 WHERE id = $2`, uid, channelID,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to persist contact point uid")
		return
	}

	c.JSON(http.StatusCreated, gin.H{"channel": models.MonitoringChannel{
		ID:                     channelID,
		ProjectID:              projectID,
		Name:                   req.Name,
		Type:                   req.Type,
		GrafanaContactpointUID: uid,
		CreatedAt:              time.Now().UTC(),
	}})
}

// channelSettings maps the request to Grafana contact-point settings, validating
// the required fields for the chosen type.
func channelSettings(req createChannelReq) (map[string]any, error) {
	switch req.Type {
	case "telegram":
		if req.BotToken == "" || req.ChatID == "" {
			return nil, fmt.Errorf("telegram requires bot_token and chat_id")
		}
		return map[string]any{"bottoken": req.BotToken, "chatid": req.ChatID}, nil
	case "email":
		if len(req.Addresses) == 0 {
			return nil, fmt.Errorf("email requires at least one address")
		}
		// Grafana joins multiple recipients on ';'. SMTP itself is Grafana
		// server-level config (shared with IAM invites), not per-contact-point.
		addrs := ""
		for i, a := range req.Addresses {
			if i > 0 {
				addrs += ";"
			}
			addrs += a
		}
		return map[string]any{"addresses": addrs, "singleEmail": false}, nil
	case "webhook":
		if req.URL == "" {
			return nil, fmt.Errorf("webhook requires url")
		}
		return map[string]any{"url": req.URL, "httpMethod": "POST"}, nil
	default:
		return nil, fmt.Errorf("type must be telegram, email or webhook")
	}
}

// DeleteChannel removes the contact point and its mirror row. Alert rules that
// referenced it keep firing but lose routing (channel_id ON DELETE SET NULL).
//
// @ID          deleteMonitoringChannel
// @Summary     Delete a notification channel
// @Description Removes the Grafana contact point and its mirror row. Write-gated (Owner/Admin/Developer).
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path string true "Project UUID"
// @Param       envId     path string true "Environment UUID"
// @Param       id        path string true "Channel UUID"
// @Success     204       "deleted"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/channels/{id} [delete]
func (h *Handler) DeleteChannel(c *gin.Context) {
	projectID, _, ok := h.resolveMonitoringProject(c, true)
	if !ok {
		return
	}
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondNotFound(c)
		return
	}
	ctx := c.Request.Context()
	var uid string
	err = h.pool.QueryRow(ctx,
		`SELECT COALESCE(grafana_contactpoint_uid, '') FROM monitoring_channels WHERE id = $1 AND project_id = $2`,
		channelID, projectID,
	).Scan(&uid)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load channel")
		return
	}
	if h.grafana != nil {
		if err := h.grafana.DeleteContactPoint(ctx, uid); err != nil {
			respondError(c, http.StatusBadGateway, "grafana delete failed: "+err.Error())
			return
		}
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM monitoring_channels WHERE id = $1`, channelID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to delete channel")
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- alert rules -----------------------------------------------------------

type createAlertRuleReq struct {
	Name      string     `json:"name"`
	Metric    string     `json:"metric"`
	Condition string     `json:"condition"` // >, <, >=, <=
	Threshold float64    `json:"threshold"`
	Duration  string     `json:"duration"`
	ChannelID *uuid.UUID `json:"channel_id"`
}

var validConditions = map[string]bool{">": true, "<": true, ">=": true, "<=": true}

// ListAlertRules returns the resource's alert rules (mirror rows), with the
// channel name resolved for display.
//
// @ID          listMonitoringAlertRules
// @Summary     List alert rules
// @Description Lists the resource's alert rules (Postgres mirror of Grafana rules), with channel name resolved. Read-only.
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appId     path     string true "Monitoring resource UUID"
// @Success     200       {object} map[string]interface{} "object with alert_rules"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId}/alert-rules [get]
func (h *Handler) ListAlertRules(c *gin.Context) {
	app, _, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT r.id, r.monitoring_app_id, r.channel_id, COALESCE(ch.name, ''),
		        r.name, r.metric, r.condition, r.threshold, r.duration, r.enabled,
		        COALESCE(r.grafana_rule_uid, ''), r.created_at
		   FROM monitoring_alert_rules r
		   LEFT JOIN monitoring_channels ch ON ch.id = r.channel_id
		  WHERE r.monitoring_app_id = $1 ORDER BY r.created_at`, app.ID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list alert rules")
		return
	}
	defer rows.Close()
	out := []models.MonitoringAlertRule{}
	for rows.Next() {
		var r models.MonitoringAlertRule
		if err := rows.Scan(&r.ID, &r.MonitoringAppID, &r.ChannelID, &r.ChannelName,
			&r.Name, &r.Metric, &r.Condition, &r.Threshold, &r.Duration, &r.Enabled,
			&r.GrafanaRuleUID, &r.CreatedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan alert rule")
			return
		}
		out = append(out, r)
	}
	c.JSON(http.StatusOK, gin.H{"alert_rules": out})
}

// CreateAlertRule provisions a Grafana threshold alert and mirrors it.
//
// @ID          createMonitoringAlertRule
// @Summary     Create an alert rule
// @Description Provisions a Grafana threshold alert (metric + condition + threshold, optional channel) and mirrors it. Write-gated (Owner/Admin/Developer). Returns 503 when Grafana is not configured.
// @Tags        monitoring
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                 true "Project UUID"
// @Param       envId     path     string                 true "Environment UUID"
// @Param       appId     path     string                 true "Monitoring resource UUID"
// @Param       body      body     map[string]interface{} true "Rule: name, metric, condition, threshold, duration, channel_id"
// @Success     201       {object} map[string]interface{} "object with the alert_rule"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId}/alert-rules [post]
func (h *Handler) CreateAlertRule(c *gin.Context) {
	app, projectID, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	if !h.requireProjectWriter(c, projectID) {
		return
	}
	if h.grafana == nil {
		respondError(c, http.StatusServiceUnavailable, "grafana not configured")
		return
	}
	var req createAlertRuleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" || req.Metric == "" {
		respondError(c, http.StatusBadRequest, "name and metric are required")
		return
	}
	if !validConditions[req.Condition] {
		respondError(c, http.StatusBadRequest, "condition must be one of >, <, >=, <=")
		return
	}
	duration := req.Duration
	if duration == "" {
		duration = "5m"
	}

	ctx := c.Request.Context()
	orgID := h.monitoringOrgLabel(ctx, projectID)

	// Resolve + validate the channel (must belong to this project).
	var contactPoint string
	if req.ChannelID != nil {
		var exists bool
		if err := h.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM monitoring_channels WHERE id = $1 AND project_id = $2)`,
			*req.ChannelID, projectID,
		).Scan(&exists); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to verify channel")
			return
		}
		if !exists {
			respondError(c, http.StatusBadRequest, "channel not found in this project")
			return
		}
		contactPoint = receiverName(*req.ChannelID)
	}

	// Folder must exist before the rule references it.
	if err := h.ensureGrafanaResource(ctx, app, projectID, orgID); err != nil {
		respondError(c, http.StatusBadGateway, "grafana provisioning failed: "+err.Error())
		return
	}

	labels := monitoringLabels(orgID, app)
	sel := promSelector(labels)
	ruleID := uuid.New()
	rule := grafana.BuildThresholdRule(h.cfg.GrafanaPromDatasourceUID, grafana.ThresholdRule{
		UID:          ruleUIDForID(ruleID),
		Title:        req.Name,
		FolderUID:    app.GrafanaFolderUID,
		RuleGroup:    app.ID.String(),
		Expr:         fmt.Sprintf("avg(%s%s)", req.Metric, sel),
		Condition:    req.Condition,
		Threshold:    req.Threshold,
		For:          duration,
		ContactPoint: contactPoint,
		Labels:       labels,
	})
	grafanaUID, err := h.grafana.CreateAlertRule(ctx, rule)
	if err != nil {
		respondError(c, http.StatusBadGateway, "grafana alert rule failed: "+err.Error())
		return
	}
	if grafanaUID == "" {
		grafanaUID = ruleUIDForID(ruleID)
	}

	if _, err := h.pool.Exec(ctx,
		`INSERT INTO monitoring_alert_rules
		   (id, monitoring_app_id, channel_id, name, metric, condition, threshold, duration, enabled, grafana_rule_uid)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true, $9)`,
		ruleID, app.ID, req.ChannelID, req.Name, req.Metric, req.Condition, req.Threshold, duration, grafanaUID,
	); err != nil {
		// Don't orphan the Grafana rule if the mirror insert fails.
		_ = h.grafana.DeleteAlertRule(ctx, grafanaUID)
		respondError(c, http.StatusConflict, "rule name already exists or insert failed")
		return
	}

	c.JSON(http.StatusCreated, gin.H{"alert_rule": models.MonitoringAlertRule{
		ID:              ruleID,
		MonitoringAppID: app.ID,
		ChannelID:       req.ChannelID,
		Name:            req.Name,
		Metric:          req.Metric,
		Condition:       req.Condition,
		Threshold:       req.Threshold,
		Duration:        duration,
		Enabled:         true,
		GrafanaRuleUID:  grafanaUID,
		CreatedAt:       time.Now().UTC(),
	}})
}

// DeleteAlertRule removes the Grafana rule and its mirror row.
//
// @ID          deleteMonitoringAlertRule
// @Summary     Delete an alert rule
// @Description Removes the Grafana alert rule and its mirror row. Write-gated (Owner/Admin/Developer).
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path string true "Project UUID"
// @Param       envId     path string true "Environment UUID"
// @Param       appId     path string true "Monitoring resource UUID"
// @Param       ruleId    path string true "Alert rule UUID"
// @Success     204       "deleted"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId}/alert-rules/{ruleId} [delete]
func (h *Handler) DeleteAlertRule(c *gin.Context) {
	app, projectID, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	if !h.requireProjectWriter(c, projectID) {
		return
	}
	ruleID, err := uuid.Parse(c.Param("ruleId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	ctx := c.Request.Context()
	var uid string
	err = h.pool.QueryRow(ctx,
		`SELECT COALESCE(grafana_rule_uid, '') FROM monitoring_alert_rules
		  WHERE id = $1 AND monitoring_app_id = $2`, ruleID, app.ID,
	).Scan(&uid)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load alert rule")
		return
	}
	if h.grafana != nil {
		if err := h.grafana.DeleteAlertRule(ctx, uid); err != nil {
			respondError(c, http.StatusBadGateway, "grafana delete failed: "+err.Error())
			return
		}
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM monitoring_alert_rules WHERE id = $1`, ruleID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to delete alert rule")
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- resource teardown -----------------------------------------------------

// DeleteMonitoringApp tears down the Grafana objects (alert rules + dashboard)
// and deletes the resource row. Mirror alert-rule rows cascade; project-scoped
// channels are left intact. The ingestion chip owns create; delete lives here
// because it must clean up the alerting/dashboard objects this layer created.
//
// @ID          deleteMonitoringApp
// @Summary     Delete a monitoring resource
// @Description Tears down the Grafana objects (alert rules + dashboard) and deletes the monitoring resource. Mirror alert-rule rows cascade; project-scoped channels are left intact. Write-gated (Owner/Admin/Developer).
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path string true "Project UUID"
// @Param       envId     path string true "Environment UUID"
// @Param       appId     path string true "Monitoring resource UUID"
// @Success     204       "deleted"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring/{appId} [delete]
func (h *Handler) DeleteMonitoringApp(c *gin.Context) {
	app, projectID, _ := h.resolveMonitoringApp(c)
	if app == nil {
		return
	}
	if !h.requireProjectWriter(c, projectID) {
		return
	}
	ctx := c.Request.Context()

	if h.grafana != nil {
		// Delete each Grafana alert rule before dropping the mirror rows.
		rows, err := h.pool.Query(ctx,
			`SELECT COALESCE(grafana_rule_uid, '') FROM monitoring_alert_rules WHERE monitoring_app_id = $1`, app.ID)
		if err == nil {
			uids := []string{}
			for rows.Next() {
				var uid string
				if rows.Scan(&uid) == nil && uid != "" {
					uids = append(uids, uid)
				}
			}
			rows.Close()
			for _, uid := range uids {
				_ = h.grafana.DeleteAlertRule(ctx, uid)
			}
		}
		dashUID := app.GrafanaDashboardUID
		if dashUID == "" {
			dashUID = dashboardUIDForApp(app.ID)
		}
		_ = h.grafana.DeleteDashboard(ctx, dashUID)
	}

	if _, err := h.pool.Exec(ctx, `DELETE FROM monitoring_apps WHERE id = $1`, app.ID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to delete monitoring resource")
		return
	}
	c.Status(http.StatusNoContent)
}
