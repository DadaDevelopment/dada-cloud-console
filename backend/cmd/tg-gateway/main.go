// Command tg-gateway is the Telegram <-> kagent agent bridge: a standalone
// service that owns the tg_bindings table, exposes an internal (no-auth,
// ClusterIP-only) HTTP API the console backend proxies through, and runs one
// long-poll goroutine per bound Telegram bot token. Single replica by hard
// requirement (two pollers on one token race getUpdates and Telegram answers
// the second 409 Conflict). Schema migrations run at the console backend's
// boot, not here -- tg-gateway only reads/writes rows.
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
	"github.com/dada-tuda/console/backend/internal/tggateway"
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

	dbURL := cfg.TGGatewayDBURL
	if dbURL == "" {
		dbURL = cfg.DBURL
	}
	pool, err := db.Connect(context.Background(), dbURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	store := tggateway.NewPGStore(pool)
	mgr := tggateway.NewManager(store, tggateway.NewTelegramClient(""), tggateway.NewA2AClient())

	runCtx, stopRun := context.WithCancel(context.Background())
	defer stopRun()
	go mgr.Run(runCtx)

	srv := tggateway.NewServer(mgr)
	srv.SetDBPinger(pool.Ping)

	port := cfg.TGGatewayPort
	if port == "" {
		port = "8082"
	}
	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info().Str("port", port).Msg("tg-gateway starting")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("tg-gateway server error")
		}
	}()

	<-quit
	log.Info().Msg("shutting down tg-gateway")
	stopRun()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("tg-gateway forced to shutdown")
	}
}
