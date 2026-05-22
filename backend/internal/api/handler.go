package api

import (
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/mlflow"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler holds shared dependencies for all API handlers.
type Handler struct {
	pool   *pgxpool.Pool
	cfg    *config.Config
	mlflow *mlflow.Client
}

// NewHandler constructs a Handler with the given dependencies.
func NewHandler(pool *pgxpool.Pool, cfg *config.Config) *Handler {
	h := &Handler{pool: pool, cfg: cfg}
	if cfg.AIStudioEnabled && cfg.MLflowBaseURL != "" {
		h.mlflow = mlflow.New(cfg.MLflowBaseURL, cfg.MLflowAuthHeader)
	}
	return h
}
