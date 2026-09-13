// Command composio-broker is the platform's integration broker: one MCP
// endpoint per project that agents point at, plus the console-facing API behind
// the "Connect" button of the Integrations page.
//
// Why a separate binary rather than a handler in the console backend: it holds
// the platform's Composio API key and it is on the hot path of every tool call
// an agent makes, so it scales and fails independently of the console. Same
// posture tg-gateway has toward tg_bindings -- it owns its tables, the console
// proxies through its internal HTTP API. Migrations run at the console
// backend's boot, not here.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dada-tuda/console/backend/internal/composio"
	"github.com/dada-tuda/console/backend/internal/db"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// defaultCatalog is the app list the platform offers when COMPOSIO_TOOLKITS is
// unset. Curated rather than "all 1500": the console already curates its own
// tool surface for the same reason -- an app list nobody reviewed is an
// authorization surface nobody reviewed.
var defaultCatalog = []string{"gmail", "github", "googlecalendar", "slack", "notion", "linear", "googlesheets", "googledrive"}

func main() {
	_ = godotenv.Load()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	if os.Getenv("LOG_LEVEL") == "debug" {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	apiKey := strings.TrimSpace(os.Getenv("COMPOSIO_API_KEY"))
	if apiKey == "" {
		log.Fatal().Msg("COMPOSIO_API_KEY is required")
	}

	dbURL := os.Getenv("COMPOSIO_BROKER_DB_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DB_URL")
	}
	if dbURL == "" {
		log.Fatal().Msg("COMPOSIO_BROKER_DB_URL or DB_URL is required")
	}

	pool, err := db.Connect(context.Background(), dbURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()

	client := composio.NewClient(os.Getenv("COMPOSIO_BASE_URL"), apiKey)
	store := composio.NewStore(pool)
	svc := composio.NewService(client, store, os.Getenv("COMPOSIO_USER_PREFIX"))

	catalog := defaultCatalog
	if raw := strings.TrimSpace(os.Getenv("COMPOSIO_TOOLKITS")); raw != "" {
		catalog = nil
		for _, part := range strings.Split(raw, ",") {
			if slug := strings.ToLower(strings.TrimSpace(part)); slug != "" {
				catalog = append(catalog, slug)
			}
		}
	}

	srv := composio.NewServer(svc, composio.ServerOptions{
		Catalog:    catalog,
		ConsoleURL: os.Getenv("COMPOSIO_BROKER_PUBLIC_URL"),
		BasePath:   os.Getenv("COMPOSIO_BROKER_BASE_PATH"),
		Token:      os.Getenv("COMPOSIO_BROKER_TOKEN"),
	})

	port := os.Getenv("COMPOSIO_BROKER_PORT")
	if port == "" {
		port = "8085"
	}
	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info().Str("port", port).Int("toolkits", len(catalog)).Msg("composio-broker listening")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("http server failed")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn().Err(err).Msg("shutdown")
	}
}
