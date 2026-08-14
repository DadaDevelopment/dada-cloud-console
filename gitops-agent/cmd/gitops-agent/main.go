package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/k8s"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/dada-tuda/console/gitops-agent/internal/server"
	"github.com/dada-tuda/console/gitops-agent/internal/worker"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("loading config")
	}

	renderer.PgRouterClusterIP = cfg.PgRouterClusterIP

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("connecting to database")
	}
	defer pool.Close()

	defaultMgr := git.New(git.RepoConfig{
		RepoURL:   cfg.DefaultRepoURL,
		Branch:    cfg.DefaultBranch,
		Username:  cfg.DefaultUsername,
		Token:     cfg.DefaultToken,
		LocalBase: cfg.RepoLocalPath,
	})
	if err := defaultMgr.EnsureCloned(); err != nil {
		log.Fatal().Err(err).Msg("cloning default repo")
	}

	// In-cluster k8s clients, shared by the DB-watcher (MoveApp re-adopts the
	// cluster-scoped PublicApi onto the target Argo app) and the status
	// reconciler. Absent outside a cluster (local dev): both consumers degrade
	// gracefully on a nil handle rather than crash.
	clients, err := k8s.NewInClusterClients()
	if err != nil {
		log.Warn().Err(err).Msg("no in-cluster k8s config: MoveApp domain-handoff + status-reconciler degraded")
		clients = nil
	}

	dbw := worker.NewDBWatcher(pool, cfg, clients)
	gitw := worker.NewGitWatcher(pool, cfg, defaultMgr)
	reaper := worker.NewReaper(pool, cfg)
	var statusReconciler *worker.StatusReconciler
	if cfg.StatusReconcileEnabled && clients != nil {
		statusReconciler = worker.NewStatusReconciler(pool, cfg, clients)
		dbw.WithDeployObserver(statusReconciler.ObserveDeployment)
	}

	if err := dbw.BootstrapProjects(ctx); err != nil {
		log.Fatal().Err(err).Msg("bootstrapping project manifests")
	}

	go dbw.Start(ctx)
	go gitw.Start(ctx)
	go reaper.Start(ctx)

	// Live-state reconciler: mirror k8s Deployment status onto App snapshots so
	// the console shows real phase/image/replicas instead of git's "Unknown".
	// Disabled gracefully when there's no in-cluster config (e.g. local dev).
	if cfg.StatusReconcileEnabled {
		if clients == nil {
			log.Warn().Msg("status-reconciler disabled: no in-cluster k8s config")
		} else {
			go statusReconciler.Start(ctx)
		}
	}

	if cfg.WebhookPort != "" {
		hub := server.NewHub()
		gitw.WithValuesNotifier(hub)

		srv := server.New(":"+cfg.WebhookPort, "", gitw, &server.ServerOptions{
			Pool:        pool,
			Manager:     defaultMgr,
			Hub:         hub,
			TokenSecret: cfg.ValuesTokenSecret,
			Config:      cfg,
		})
		go func() {
			if err := srv.Start(ctx); err != nil {
				log.Error().Err(err).Msg("webhook server error")
			}
		}()
	}

	<-ctx.Done()
	log.Info().Msg("shutting down")
}
