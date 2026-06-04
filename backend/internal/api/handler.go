package api

import (
	"github.com/dada-tuda/console/backend/internal/config"
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
	return h
}
