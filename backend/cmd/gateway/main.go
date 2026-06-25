// Command gateway is the dada-cloud Telemetry Gateway (ADR-012): a standalone,
// stateless write-plane service for device-facing telemetry ingest. It speaks
// OTLP/HTTP (metrics + logs, protobuf + json) and the bespoke DADA JSON, auth'd
// by scoped dmon_ keys, and forwards to the same Prometheus + Elasticsearch the
// console reads from. Deployed and scaled independently of the console; it holds
// only a read-only DB role (key verify + tenant resolve).
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/db"
	"github.com/dada-tuda/console/backend/internal/gateway"
	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	_ = godotenv.Load()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if cfg.LogLevel == "debug" {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	// Read-only DB pool (key verify + tenant resolve only). The gateway never
	// writes to Postgres; grant it a RO role at deploy time.
	dbURL := cfg.GatewayDBURL
	if dbURL == "" {
		dbURL = cfg.DBURL
	}
	pool, err := db.Connect(context.Background(), dbURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	// Forward targets — same wiring the console uses (remote-write falls back to
	// the query Prometheus + creds when no dedicated receiver is set).
	rwURL := cfg.PrometheusRemoteWriteURL
	rwUser := cfg.PrometheusRemoteWriteUser
	rwPass := cfg.PrometheusRemoteWritePass
	if rwURL == "" {
		rwURL = cfg.PrometheusQueryURL
	}
	if rwUser == "" && rwPass == "" {
		rwUser, rwPass = cfg.PrometheusQueryUser, cfg.PrometheusQueryPass
	}
	promwrite := prometheus.NewWriteClient(rwURL, rwUser, rwPass)
	eswrite := logsearch.NewWriteClient(cfg.ElasticsearchURL, cfg.ElasticsearchAPIKey, cfg.MonitoringLogIndex)
	if promwrite == nil {
		log.Warn().Msg("prometheus remote-write not configured — /v1/metrics and /api/v1/metrics will 503")
	}
	if eswrite == nil {
		log.Warn().Msg("elasticsearch not configured — /v1/logs and /api/v1/logs will 503")
	}

	srv := gateway.NewServer(gateway.NewPGKeyStore(pool), promwrite, eswrite, gateway.Config{
		MaxLabels:       cfg.MonitoringMaxLabels,
		MaxSeriesPerReq: cfg.MonitoringMaxSeriesPerReq,
		RateLimitPerMin: cfg.MonitoringRateLimitPerMin,
		MaxMessageBytes: 32 * 1024,
	})

	port := cfg.GatewayPort
	if port == "" {
		port = "8081"
	}
	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info().Str("port", port).Msg("telemetry gateway starting")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("gateway server error")
		}
	}()

	<-quit
	log.Info().Msg("shutting down gateway")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("gateway forced to shutdown")
	}
}
