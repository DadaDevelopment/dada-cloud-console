package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/dada-tuda/console/backend/internal/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ingest primitives (rate limiter, key generation, metric-name sanitization)
// live in internal/telemetry so the console and the gateway share one
// implementation (ADR-012, no fork/drift). The thin aliases below keep the
// existing call sites and tests in this package readable.

// ingestLimiter is the shared per-app token bucket.
type ingestLimiter = telemetry.IngestLimiter

func newIngestLimiter(perMin int) *ingestLimiter { return telemetry.NewIngestLimiter(perMin) }

// generateMonitoringKey mints a plaintext key shown once, plus a displayable
// prefix and an argon2id hash (salt(16)||digest(32)). The plaintext is never
// persisted. This is the local issuance seam — a user-service IAM mint would
// replace it.
func generateMonitoringKey() (full, prefix string, hash []byte, err error) {
	return telemetry.GenerateKey()
}

// sanitizeMetricName coerces an arbitrary metric key into a valid Prometheus
// metric name ([a-zA-Z_][a-zA-Z0-9_]*).
func sanitizeMetricName(s string) string { return telemetry.SanitizeMetricName(s) }

// ---- request/response shapes ----

type createMonitoringRequest struct {
	Name string `json:"name"`
}

type ingestMetricsRequest struct {
	Timestamp string             `json:"timestamp"`
	Source    string             `json:"source"`
	Metrics   map[string]float64 `json:"metrics"`
}

type ingestLogsRequest struct {
	Source  string `json:"source"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

const (
	maxSourceLen  = 200
	maxMessageLen = 32 * 1024
)

// ---- handlers ----

// CreateMonitoringApp registers a new monitoring resource in a project
// environment and issues its scoped API key (returned in plaintext exactly once).
//
// @ID          createMonitoringApp
// @Summary     Create a monitoring resource
// @Description Registers a monitoring app in a project environment and issues a scoped API key (metrics:write, logs:write). The plaintext key is returned exactly once and never recoverable.
// @Tags        monitoring
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                  true "Project UUID"
// @Param       envId     path     string                  true "Environment UUID"
// @Param       body      body     createMonitoringRequest true "Monitoring resource name"
// @Success     201       {object} map[string]interface{} "object with monitoring_app and api_key (shown once)"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/monitoring [post]
func (h *Handler) CreateMonitoringApp(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}

	var req createMonitoringRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		respondError(c, http.StatusBadRequest, "name is required")
		return
	}
	if err := validateKubeName(req.Name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	// Uniqueness within (project, environment).
	var existing int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM monitoring_apps WHERE project_id = $1 AND environment_id = $2 AND name = $3`,
		projectID, envID, req.Name,
	).Scan(&existing); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check name uniqueness")
		return
	}
	if existing > 0 {
		respondError(c, http.StatusConflict, "a monitoring resource with that name already exists in this environment")
		return
	}

	full, prefix, hash, err := generateMonitoringKey()
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to issue api key")
		return
	}
	scopes := []string{"metrics:write", "logs:write"}

	var app models.MonitoringApp
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO monitoring_apps (project_id, environment_id, name, api_key_prefix, api_key_hash, scopes)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, project_id, environment_id, name, api_key_prefix, scopes, created_at`,
		projectID, envID, req.Name, prefix, hash, scopes,
	)
	if err := row.Scan(&app.ID, &app.ProjectID, &app.EnvironmentID, &app.Name, &app.APIKeyPrefix, &app.Scopes, &app.CreatedAt); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create monitoring resource")
		return
	}

	// Best-effort audit trail.
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, 'CreateMonitoringApp', 'Monitoring', $3, '{}')`,
		claims.UserID, projectID, req.Name,
	)

	c.JSON(http.StatusCreated, gin.H{
		"monitoring_app": app,
		"api_key":        full, // shown once; not recoverable later
		"message":        "store this key now — it will not be shown again",
	})
}

// ListMonitoringApps lists the monitoring resources in a project (all
// environments). Secrets are never returned — only the displayable prefix.
//
// @ID          listMonitoringApps
// @Summary     List monitoring resources
// @Description Returns the monitoring apps in a project across all environments. Only the displayable api_key_prefix is returned; secrets are never exposed.
// @Tags        monitoring
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "object with a monitoring_apps array"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/monitoring [get]
func (h *Handler) ListMonitoringApps(c *gin.Context) {
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
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT m.id, m.project_id, m.environment_id, m.name, COALESCE(m.api_key_prefix, ''),
		        m.scopes, m.created_at, e.name
		 FROM monitoring_apps m
		 JOIN environments e ON e.id = m.environment_id
		 WHERE m.project_id = $1
		 ORDER BY m.name`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query monitoring resources")
		return
	}
	defer rows.Close()

	apps := []models.MonitoringApp{}
	for rows.Next() {
		var a models.MonitoringApp
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.EnvironmentID, &a.Name, &a.APIKeyPrefix, &a.Scopes, &a.CreatedAt, &a.Environment); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan monitoring resource")
			return
		}
		apps = append(apps, a)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading monitoring resources")
		return
	}

	c.JSON(http.StatusOK, gin.H{"monitoring_apps": apps})
}

// monitoringTarget holds the authoritative labels for an ingest target, loaded
// from the DB (never trusted from the request body).
type monitoringTarget struct {
	appName string
	envName string
	orgID   string
}

// loadIngestTarget resolves the monitoring app by id, enforcing that it belongs
// to the path project (tenant isolation). Returns false (and writes 404) when
// the app does not exist or the project does not match.
func (h *Handler) loadIngestTarget(c *gin.Context, appID, projectID uuid.UUID) (monitoringTarget, bool) {
	var t monitoringTarget
	var owner *uuid.UUID
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT m.name, e.name, p.owner_id
		 FROM monitoring_apps m
		 JOIN environments e ON e.id = m.environment_id
		 JOIN projects p ON p.id = m.project_id
		 WHERE m.id = $1 AND m.project_id = $2`,
		appID, projectID,
	).Scan(&t.appName, &t.envName, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return t, false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load monitoring resource")
		return t, false
	}
	if owner != nil {
		t.orgID = owner.String()
	}
	return t, true
}

// IngestMetrics converts a JSON metrics payload into Prometheus remote-write and
// pushes it. Scope (metrics:write) is enforced by RequireScope middleware.
//
// @ID          ingestMetrics
// @Summary     Ingest metrics
// @Description Converts a JSON metrics payload into Prometheus remote-write and pushes it. Requires the metrics:write scope. Tenancy labels (org_id, project_id, environment) are applied from authoritative DB values.
// @Tags        monitoring
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string               true "Project UUID"
// @Param       appId     path     string               true "Monitoring app UUID"
// @Param       body      body     ingestMetricsRequest true "Metrics payload"
// @Success     202       {object} map[string]interface{} "object with ingested count"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     413       {object} map[string]string
// @Failure     429       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/monitoring/{appId}/metrics [post]
func (h *Handler) IngestMetrics(c *gin.Context) {
	projectID, appID, ok := parseProjectApp(c)
	if !ok {
		respondNotFound(c)
		return
	}
	if h.promwrite == nil {
		respondError(c, http.StatusServiceUnavailable, "metrics ingestion not configured")
		return
	}
	target, ok := h.loadIngestTarget(c, appID, projectID)
	if !ok {
		return
	}
	if !h.ingestLimiter.Allow(appID) {
		respondError(c, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	var req ingestMetricsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Metrics) == 0 {
		respondError(c, http.StatusBadRequest, "metrics is required and must be non-empty")
		return
	}
	// Cardinality guard: cap metrics per request and bound the source label.
	if len(req.Metrics) > h.maxLabels {
		respondError(c, http.StatusRequestEntityTooLarge, "too many metrics in one request")
		return
	}
	if len(req.Source) > maxSourceLen {
		respondError(c, http.StatusBadRequest, "source too long")
		return
	}

	tsMS := time.Now().UnixMilli()
	if req.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339, req.Timestamp); err == nil {
			tsMS = parsed.UnixMilli()
		}
	}

	series := make([]prometheus.WriteSeries, 0, len(req.Metrics))
	for name, value := range req.Metrics {
		series = append(series, prometheus.WriteSeries{
			Labels: map[string]string{
				"__name__":       sanitizeMetricName(name),
				"org_id":         target.orgID,
				"project_id":     projectID.String(),
				"environment":    target.envName,
				"source":         req.Source,
				"monitoring_app": target.appName,
			},
			Value:       value,
			TimestampMS: tsMS,
		})
	}

	if err := h.promwrite.Write(c.Request.Context(), series); err != nil {
		respondError(c, http.StatusBadGateway, "remote-write failed: "+err.Error())
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ingested": len(series)})
}

// IngestLogs writes a single application log document to Elasticsearch
// (dada-app-logs-*). Scope (logs:write) is enforced by RequireScope middleware.
//
// @ID          ingestLogs
// @Summary     Ingest a log line
// @Description Writes a single application log document to Elasticsearch (dada-app-logs-*). Requires the logs:write scope. Tenancy labels are applied from authoritative DB values.
// @Tags        monitoring
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string            true "Project UUID"
// @Param       appId     path     string            true "Monitoring app UUID"
// @Param       body      body     ingestLogsRequest true "Log payload"
// @Success     202       {object} map[string]interface{} "object with ingested count"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     413       {object} map[string]string
// @Failure     429       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/monitoring/{appId}/logs [post]
func (h *Handler) IngestLogs(c *gin.Context) {
	projectID, appID, ok := parseProjectApp(c)
	if !ok {
		respondNotFound(c)
		return
	}
	if h.eswrite == nil {
		respondError(c, http.StatusServiceUnavailable, "log ingestion not configured")
		return
	}
	target, ok := h.loadIngestTarget(c, appID, projectID)
	if !ok {
		return
	}
	if !h.ingestLimiter.Allow(appID) {
		respondError(c, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	var req ingestLogsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		respondError(c, http.StatusBadRequest, "message is required")
		return
	}
	if len(req.Message) > maxMessageLen {
		respondError(c, http.StatusRequestEntityTooLarge, "message too long")
		return
	}
	if len(req.Source) > maxSourceLen {
		respondError(c, http.StatusBadRequest, "source too long")
		return
	}

	if err := h.eswrite.Index(c.Request.Context(), logsearch.AppLog{
		Timestamp:     time.Now(),
		Source:        req.Source,
		Level:         strings.ToUpper(strings.TrimSpace(req.Level)),
		Message:       req.Message,
		OrgID:         target.orgID,
		ProjectID:     projectID.String(),
		Environment:   target.envName,
		MonitoringApp: target.appName,
	}); err != nil {
		respondError(c, http.StatusBadGateway, "log write failed: "+err.Error())
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ingested": 1})
}

// parseProjectApp parses :projectId and :appId path params.
func parseProjectApp(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	appID, err := uuid.Parse(c.Param("appId"))
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, appID, true
}
