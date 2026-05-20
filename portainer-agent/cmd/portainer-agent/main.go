package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dada-tuda/console/portainer-agent/internal/config"
	"github.com/dada-tuda/console/portainer-agent/internal/db"
	"github.com/dada-tuda/console/portainer-agent/internal/server"
	"github.com/dada-tuda/console/portainer-agent/internal/worker"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	_ = godotenv.Load() // optional .env file in dev

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("config load failed")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("db connect failed")
	}
	defer pool.Close()

	// Health endpoint
	srv := server.Start(ctx, ":8090")
	defer srv.Shutdown(ctx) //nolint:errcheck

	log.Info().Msg("portainer-agent starting")
	w := worker.NewVMWatcher(pool, cfg)
	w.Start(ctx)

	log.Info().Msg("portainer-agent stopped")
}
