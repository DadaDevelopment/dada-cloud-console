package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dada-tuda/console/build-agent/internal/config"
	"github.com/dada-tuda/console/build-agent/internal/db"
	"github.com/dada-tuda/console/build-agent/internal/server"
	"github.com/dada-tuda/console/build-agent/internal/worker"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("loading config")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("connecting to database")
	}
	defer pool.Close()

	hub := server.NewHub()

	// Runner publishes build-log frames into the hub keyed by build/<id>.
	runner := worker.NewRunner(pool, cfg, func(buildID, line string) {
		hub.PublishLog(buildID, line)
	})

	poller := worker.NewPoller(cfg.PollInterval, runner)
	go poller.Start(ctx)

	if cfg.WebhookPort != "" {
		srv := server.New(":"+cfg.WebhookPort, &server.Options{
			Pool:        pool,
			Hub:         hub,
			Nudger:      runner,
			TokenSecret: cfg.TokenSecret,
			Config:      cfg,
		})
		go func() {
			if err := srv.Start(ctx); err != nil {
				log.Error().Err(err).Msg("server error")
			}
		}()
	}

	log.Info().Msg("build-agent started")
	<-ctx.Done()
	log.Info().Msg("shutting down")
}
