package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/dada-tuda/console/backend/internal/api"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/db"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// @title                      DADA Cloud Console API
// @version                    1.0
// @description                Platform API to order and manage cloud resources — managed databases, apps, VMs (app servers), AI models, and public API endpoints — on the DADA Cloud platform.
// @description                Mutations are asynchronous: create/update/delete enqueue an operation and return 202 Accepted with an operation object; poll GET /projects/{projectId}/operations/{operationId} until the operation reaches a terminal status (Ready or Failed).
// @BasePath                   /api/v1
// @securityDefinitions.apikey BearerAuth
// @in                         header
// @name                       Authorization
func main() {
	// Load .env if present (dev mode)
	_ = godotenv.Load()

	// Logger setup
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// Configure logging based on dev mode
	if cfg.DevMode {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
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

	// Run database migrations
	migrationsDir := resolveMigrationsDir()
	log.Info().Str("dir", migrationsDir).Msg("running migrations")
	if err := db.RunMigrations(context.Background(), pool, migrationsDir); err != nil {
		log.Fatal().Err(err).Msg("failed to run migrations")
	}
	log.Info().Msg("migrations complete")

	// Set up HTTP router
	router := api.SetupRouter(pool, cfg)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info().Str("port", cfg.Port).Msg("HTTP server starting")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	// Periodic cleanup of expired AI Studio API-key reveal rows. The rows
	// hold plaintext keys for 15 min then become unrecoverable; the reveal
	// endpoint deletes on consume but unconsumed rows just sit there as
	// dead plaintext. A 1-minute sweep keeps the window tight.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-cleanupCtx.Done():
				return
			case <-ticker.C:
				_, err := pool.Exec(cleanupCtx,
					`DELETE FROM aimodel_api_key_reveals WHERE expires_at < NOW()`)
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Warn().Err(err).Msg("aimodel reveal cleanup failed")
				}
			}
		}
	}()

	// Custom-domain DNS verification poller. Re-checks the TXT challenge for every
	// not-yet-verified apex authorization so ownership flips to verified without the
	// user hitting the manual verify endpoint.
	dnsCtx, dnsCancel := context.WithCancel(context.Background())
	defer dnsCancel()
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-dnsCtx.Done():
				return
			case <-ticker.C:
				if err := api.VerifyPendingDomains(dnsCtx, pool, cfg); err != nil && !errors.Is(err, context.Canceled) {
					log.Warn().Err(err).Msg("custom-domain DNS verification failed")
				}
			}
		}
	}()

	<-quit
	log.Info().Msg("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("server forced to shutdown")
	}
}

// resolveMigrationsDir returns the path to the migrations directory.
// It tries MIGRATIONS_DIR env var first, then walks up from the binary/source location.
func resolveMigrationsDir() string {
	if dir := os.Getenv("MIGRATIONS_DIR"); dir != "" {
		return dir
	}

	// During development (go run), resolve relative to this source file.
	_, filename, _, ok := runtime.Caller(0)
	if ok {
		// filename is .../backend/cmd/server/main.go
		// migrations dir is .../backend/migrations
		candidate := filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	// Fallback: relative to CWD (works when binary is run from repo root)
	return "migrations"
}
