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
	"github.com/dada-tuda/console/backend/internal/billing"
	"github.com/dada-tuda/console/backend/internal/billing/costengine"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/db"
	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/jackc/pgx/v5/pgxpool"
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
	handler := api.NewPreviewGate(pool, cfg, router)

	// Refresh Prometheus state gauges (operations / domain health) served at
	// /metrics so stuck or failed operations alert the platform team.
	metricsCtx, metricsCancel := context.WithCancel(context.Background())
	defer metricsCancel()
	metrics.StartCollector(metricsCtx, pool, 30*time.Second)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handler,
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
				api.RunDomainMaintenanceTick(dnsCtx, pool, cfg)
			}
		}
	}()

	if cfg.BillingEnabled {
		billingPlans, planErr := billing.LoadPlans("")
		if planErr != nil {
			log.Fatal().Err(planErr).Msg("billing: failed to load plans (BILLING_ENABLED=true)")
		}
		startOverageCollector(metricsCtx, pool, billingPlans, cfg.BillingExemptOrgs)

		meterInterval := time.Duration(cfg.BillingMeterIntervalSec) * time.Second
		meterCtx, meterCancel := context.WithCancel(context.Background())
		defer meterCancel()
		go func() {
			api.MeterUsage(meterCtx, pool, cfg, billingPlans)
			for {
				timer := time.NewTimer(api.NextMeterDelay(time.Now(), meterInterval))
				select {
				case <-meterCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
					api.MeterUsage(meterCtx, pool, cfg, billingPlans)
				}
			}
		}()
		log.Info().Dur("interval", meterInterval).Msg("billing meter started")

		// Dada Box per-minute meter and lifecycle reaper. Two loops on ONE ticker
		// with deliberately different concurrency rules:
		//
		//   MeterBoxMinutes runs UNGUARDED on every replica. Its writes are keyed by
		//   the box_usage primary key (box_id, minute_start, kind), so a replay, a
		//   crashed pod and two racing replicas all collapse onto one row — and a
		//   lock would instead turn one replica's outage into unbillable minutes
		//   that no backfill can recover.
		//
		//   RunBoxMaintenanceTick takes the box-reaper advisory lock, because it
		//   enqueues operations and sends customer email. That is the same class as
		//   RunDomainMaintenanceTick above, and running it on three replicas would
		//   mean three "your box will be deleted" emails per tick.
		//
		// Both live under BILLING_ENABLED with the rest of the metering: a
		// deployment that is not billing must not be suspending anyone's box for
		// spending money it is not charging for.
		// THE REAPER IS NOT CONDITIONAL ON THE METER, and it used to be. Both were
		// built inside one if/else on NewBoxMeter, so a box-fleet-cost.yaml this
		// process could not read did not merely stop billing box minutes — it also
		// stopped the only loop that puts idle boxes to sleep, warns about boxes
		// asleep too long and destroys them. That is the wrong way round: a fleet
		// nobody is charging for is exactly the fleet that most needs collecting,
		// and coupling the two turns a pricing-file typo into a bill that grows on
		// its own. They are now started independently, and the meter's failure is
		// reported as the billing gap it is rather than as a silent lifecycle stop.
		boxNotifier := notify.New(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
		boxMeter, bmErr := api.NewBoxMeter(pool, cfg, billingPlans, boxNotifier)
		if bmErr != nil {
			// Not fatal, and not silent either. A broken box-fleet-cost.yaml must not
			// take the whole console down — but it does mean box minutes are not being
			// billed, which is exactly the kind of thing that is otherwise discovered
			// a month later.
			log.Error().Err(bmErr).Msg("box meter NOT started: failed to derive box fleet unit cost; box minutes are not being billed")
		}
		boxReaper := api.NewBoxReaper(pool, cfg, boxNotifier)
		boxInterval := time.Duration(cfg.BoxMeterIntervalSecs) * time.Second
		go func() {
			ticker := time.NewTicker(boxInterval)
			defer ticker.Stop()
			for {
				select {
				case <-meterCtx.Done():
					return
				case <-ticker.C:
					if boxMeter != nil {
						boxMeter.MeterBoxMinutes(meterCtx)
					}
					boxReaper.RunBoxMaintenanceTick(meterCtx)
				}
			}
		}()
		if boxMeter != nil {
			unit := boxMeter.UnitCost()
			log.Info().
				Dur("interval", boxInterval).
				Float64("per_vcpu_rub_month", unit.PerVCPU).
				Float64("per_gb_ram_rub_month", unit.PerGBRAM).
				Float64("per_gb_storage_rub_month", unit.PerGBStorage).
				Float64("box_standard_rub_minute", boxMeter.PerMinuteRub("box-standard")).
				Msg("box meter and reaper started")
		} else {
			log.Warn().Dur("interval", boxInterval).
				Msg("box reaper started WITHOUT the meter: boxes are still collected, minutes are not billed")
		}

		expiryNotifier := notify.New(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
		autopayCharger := api.NewAutopayCharger(pool, cfg)
		go func() {
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			api.SweepPaymentPlanMismatch(meterCtx, pool, cfg.AuditNotifyEmail, time.Now().UTC())
			for {
				select {
				case <-meterCtx.Done():
					return
				case <-ticker.C:
					now := time.Now().UTC()
					api.SweepAutopay(meterCtx, pool, autopayCharger, expiryNotifier, cfg.AuditNotifyEmail, billingPlans, now)
					api.SweepPlanExpiry(meterCtx, pool, expiryNotifier, cfg.AuditNotifyEmail, now)
					api.SweepQuotaGrace(meterCtx, pool, expiryNotifier, cfg.AuditNotifyEmail, billingPlans, now)
					api.SweepPaymentPlanMismatch(meterCtx, pool, cfg.AuditNotifyEmail, now)
				}
			}
		}()
		log.Info().Msg("billing autopay, plan-expiry and quota-grace sweepers started")
	}

	<-quit
	log.Info().Msg("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("server forced to shutdown")
	}
}

// startOverageCollector publishes the per-org "consumed vs included" gauges the
// overage alert compares.
//
// The allowance is derived here rather than in the metrics package because it
// needs both halves of the pricing input: plans.yaml for the published price
// and cluster-cost.yaml for the unit cost behind the free tier's floor. A
// cluster-cost file this process cannot read is not fatal and not silent: the
// console keeps running and the collector simply does not start, which is
// reported as the blind spot it is. Alerting on a fabricated allowance would be
// worse than not alerting.
//
// exempt is the same BILLING_EXEMPT_ORGS list the quota gate honours. An org the
// platform has already decided never to bill must not be the loudest thing in an
// alert about unbilled consumption.
func startOverageCollector(ctx context.Context, pool *pgxpool.Pool, plans []pricing.Plan, exempt []string) {
	clusterCost, err := billing.LoadClusterCost("")
	if err != nil {
		log.Error().Err(err).Msg("org overage collector NOT started: cluster cost config unreadable; over-consuming accounts will not alert")
		return
	}
	unit, err := costengine.ComputeUnitCost(clusterCost)
	if err != nil {
		log.Error().Err(err).Msg("org overage collector NOT started: unit cost not derivable; over-consuming accounts will not alert")
		return
	}
	allowance := map[string]float64{}
	for _, p := range plans {
		if included := pricing.IncludedConsumptionRub(p, unit); included > 0 {
			allowance[p.Key] = included
		}
	}
	metrics.StartOverageCollector(ctx, pool, 5*time.Minute, allowance, exempt)
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
