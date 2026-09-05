// Command agent-runtime is the agent platform conversation runtime: a
// service that owns conversation state, executes lifecycle hooks, and provides
// domain instructions to agents. It sits between channel gateways (tg-gateway,
// slack-gateway, etc) and kagent agents, enabling conversation persistence,
// CRM integration, and follow-up automation without requiring agents to
// implement that logic in their prompts. See docs/plans/agent-harness-conversation-runtime.md
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dada-tuda/console/backend/internal/agentruntime"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/db"
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

	pool, err := db.Connect(context.Background(), cfg.DBURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	gitopsBasePath := os.Getenv("GITOPS_BASE_PATH")
	if gitopsBasePath == "" {
		gitopsBasePath = "/tmp/dada-state-repo"
	}

	srv := agentruntime.NewServer(pool, gitopsBasePath)
	retryCtx, stopRetry := context.WithCancel(context.Background())
	defer stopRetry()
	retryDone := make(chan struct{})
	go func() {
		defer close(retryDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			// Run once on startup so persisted failures recover after a restart.
			if _, err := srv.ReconcilePaused(retryCtx, 5); err != nil && retryCtx.Err() == nil {
				log.Error().Err(err).Msg("CRM pause retry failed")
			}
			select {
			case <-retryCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	idleTick := 60
	if v := os.Getenv("AGENT_RUNTIME_IDLE_TICK_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			idleTick = n
		}
	}
	srv.StartIdleScheduler(context.Background(), idleTick, os.Getenv("TG_GATEWAY_OUTBOUND_URL"))

	port := os.Getenv("AGENT_RUNTIME_PORT")
	if port == "" {
		port = "8083"
	}
	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info().Str("port", port).Msg("agent-runtime starting")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("agent-runtime server error")
		}
	}()

	<-quit
	log.Info().Msg("shutting down agent-runtime")
	stopRetry()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("agent-runtime forced to shutdown")
	}
	select {
	case <-retryDone:
	case <-ctx.Done():
		log.Error().Msg("CRM pause retry shutdown timed out")
	}
}
