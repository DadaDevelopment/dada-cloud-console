// Command grafana-embed-gateway is the auth shim that fronts grafana.dada-tuda.ru
// so the console can embed Grafana dashboards in an iframe without a manual
// Grafana login (ADR-012 follow-up).
//
// It reverse-proxies every request to the internal Grafana service. Requests
// carrying a valid console-minted embed token (query param on the first iframe
// hit, sliding-session cookie thereafter) are authenticated: the gateway injects
// Grafana auth.proxy identity headers (X-WEBAUTH-USER / -EMAIL / -GROUPS), which
// Grafana (auth.proxy enabled, whitelist = this pod's CIDR) trusts. Requests
// without a token pass through untouched, so direct admin SSO to
// grafana.dada-tuda.ru keeps working. Cross-tenant isolation is enforced by
// Grafana folder ACLs keyed to the per-project teams the GROUPS header drives.
//
// Stateless: it holds only the shared HMAC secret (token verify + cookie
// re-mint). Deploy it as the upstream of the grafana.dada-tuda.ru Ingress, in
// front of the Grafana Service. See internal/grafanaembed.
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
	"github.com/dada-tuda/console/backend/internal/grafanaembed"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	_ = godotenv.Load()
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg, err := config.LoadGatewayEmbed()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if cfg.LogLevel == "debug" {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	proxy, err := grafanaembed.NewProxy(grafanaembed.Config{
		UpstreamURL:  cfg.GrafanaEmbedInternalURL,
		Secret:       []byte(cfg.GrafanaEmbedSecret),
		CookieDom:    cfg.GrafanaEmbedCookieDomain,
		UpstreamHost: cfg.GrafanaEmbedUpstreamHost,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to build grafana embed proxy")
	}

	mux := http.NewServeMux()
	// Liveness only — does NOT touch Grafana, so a Grafana blip doesn't flap the
	// gateway pod. Real Grafana health rides through the proxy at /api/health.
	mux.HandleFunc("/_gateway/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", proxy)

	srv := &http.Server{
		Addr:              cfg.GrafanaEmbedListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info().Str("addr", cfg.GrafanaEmbedListenAddr).
			Str("upstream", cfg.GrafanaEmbedInternalURL).
			Msg("grafana-embed-gateway listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Info().Msg("grafana-embed-gateway stopped")
}
