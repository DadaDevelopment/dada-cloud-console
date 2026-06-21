package api

import (
	"github.com/dada-tuda/console/backend/internal/buildagent"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/grafana"
	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/dada-tuda/console/backend/internal/mlflow"
	"github.com/dada-tuda/console/backend/internal/portainer"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler holds shared dependencies for all API handlers.
type Handler struct {
	pool       *pgxpool.Pool
	cfg        *config.Config
	mlflow     *mlflow.Client
	portainer  *portainer.Client  // nil when PORTAINER_URL/PORTAINER_API_TOKEN unset
	prometheus *prometheus.Client // nil when PROMETHEUS_QUERY_URL unset
	logsearch  *logsearch.Client  // nil when ELASTICSEARCH_URL unset
	buildagent *buildagent.Client // nil when BUILD_AGENT_URL unset

	// Monitoring read/alert/health layer (ADR-011).
	grafana      *grafana.Client   // nil when GRAFANA_BASE_URL/GRAFANA_API_TOKEN unset
	appLogsearch *logsearch.Client // ES client on the dada-app-logs-* index; nil when ES unset

	// Monitoring write path (PRD-monitoring / ADR-011).
	promwrite     *prometheus.WriteClient // nil when no remote-write URL resolved
	eswrite       *logsearch.WriteClient  // nil when ELASTICSEARCH_URL unset
	ingestLimiter *ingestLimiter          // per-app token bucket
	maxLabels     int                     // cardinality guard: metrics per ingest request
}

// NewHandler constructs a Handler with the given dependencies.
func NewHandler(pool *pgxpool.Pool, cfg *config.Config) *Handler {
	h := &Handler{pool: pool, cfg: cfg}
	if cfg.AIStudioEnabled && cfg.MLflowBaseURL != "" {
		h.mlflow = mlflow.New(cfg.MLflowBaseURL, cfg.MLflowAuthHeader)
	}
	h.portainer = portainer.New(cfg.PortainerURL, cfg.PortainerAPIToken)
	h.prometheus = prometheus.New(cfg.PrometheusQueryURL, cfg.PrometheusQueryUser, cfg.PrometheusQueryPass)
	h.logsearch = logsearch.New(cfg.ElasticsearchURL, cfg.ElasticsearchAPIKey, cfg.ElasticsearchIndex)
	h.buildagent = buildagent.New(cfg.BuildAgentURL)
	h.grafana = grafana.New(cfg.GrafanaBaseURL, cfg.GrafanaAPIToken, cfg.GrafanaPromDatasourceUID, cfg.GrafanaPublicURL)
	h.appLogsearch = logsearch.New(cfg.ElasticsearchURL, cfg.ElasticsearchAPIKey, cfg.MonitoringLogIndex)

	// Monitoring write path. Remote-write defaults to the query Prometheus +
	// its creds when a dedicated receiver URL/creds are not set.
	rwURL := cfg.PrometheusRemoteWriteURL
	rwUser := cfg.PrometheusRemoteWriteUser
	rwPass := cfg.PrometheusRemoteWritePass
	if rwURL == "" {
		rwURL = cfg.PrometheusQueryURL
	}
	if rwUser == "" && rwPass == "" {
		rwUser, rwPass = cfg.PrometheusQueryUser, cfg.PrometheusQueryPass
	}
	h.promwrite = prometheus.NewWriteClient(rwURL, rwUser, rwPass)
	h.eswrite = logsearch.NewWriteClient(cfg.ElasticsearchURL, cfg.ElasticsearchAPIKey, cfg.MonitoringLogIndex)
	h.ingestLimiter = newIngestLimiter(cfg.MonitoringRateLimitPerMin)
	h.maxLabels = cfg.MonitoringMaxLabels
	if h.maxLabels <= 0 {
		h.maxLabels = 30
	}
	return h
}
