// Command fincore-sync pushes Dada Cloud's own economics into FinCore, the
// analytical CRM this team also builds.
//
// It exists as a separate binary for the backfill: the first run writes the
// whole history into a customer's live database, and that run has to be
// inspectable before it happens. With -dry-run it builds every payload, prints
// them, and writes nothing. Without it, the same payloads are pushed through
// the idempotent ingest seam, so re-running is a no-op rather than a second
// booking of the same money.
//
// The ongoing hourly push runs inside the server (internal/fincore.Syncer);
// this tool is the manual lever over the same code path.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/beget"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/fincore"
)

func main() {
	dryRun := flag.Bool("dry-run", true, "build the payloads and print them without writing to FinCore")
	verbose := flag.Bool("payloads", false, "print every payload, not just the counts")
	includeInternal := flag.Bool("include-internal-orgs", false, "count payments made by the platform's own orgs as revenue")
	flag.Parse()

	if err := run(*dryRun, *verbose, *includeInternal); err != nil {
		fmt.Fprintln(os.Stderr, "fincore-sync:", err)
		os.Exit(1)
	}
}

func run(dryRun, verbose, includeInternal bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	client := fincore.New(cfg.FinCoreBaseURL, cfg.FinCoreToken, cfg.FinCoreTenantSlug)
	if client == nil {
		return fmt.Errorf("FinCore not configured: set FINCORE_BASE_URL, FINCORE_TOKEN, FINCORE_TENANT_SLUG")
	}
	if !dryRun && !client.LooksLikeServiceToken() {
		return fmt.Errorf("FINCORE_TOKEN is not a service token (fcs_...); the ingest seam answers 401 to a human JWT")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DBURL)
	if err != nil {
		return fmt.Errorf("connect console database: %w", err)
	}
	defer pool.Close()

	syncer := fincore.NewSyncer(pool, client, beget.New(cfg.BegetK8SToken), cfg.BegetK8SClusterSlug, cfg.HardwareMonthlyCostRUB, cfg.FinCoreProjectID, includeInternal || cfg.FinCoreIncludeInternalOrgs)
	if syncer == nil {
		return fmt.Errorf("syncer not constructible: database or FinCore client missing")
	}

	report, err := syncer.Run(ctx, dryRun)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	summary := map[string]any{
		"dry_run":                  report.DryRun,
		"tenant":                   cfg.FinCoreTenantSlug,
		"clients":                  len(report.Clients),
		"clients_created":          report.ClientsCreated,
		"clients_updated":          report.ClientsUpdated,
		"transactions":             len(report.Transactions),
		"transactions_created":     report.TransactionsCreated,
		"transactions_updated":     report.TransactionsUpdated,
		"transactions_unchanged":   report.Unchanged,
		"payments_unlinked":        report.PaymentsUnlinked,
		"payments_settled_in_bank": report.PaymentsSettledInBank,
		"hosting_cost_rub":         report.HostingCostRUB,
		"hosting_cost_ingested":    false,
	}
	if report.BegetSkipped != "" {
		summary["hosting_cost_unavailable"] = report.BegetSkipped
	}
	if err := enc.Encode(summary); err != nil {
		return err
	}
	if verbose {
		return enc.Encode(map[string]any{
			"client_payloads":      report.Clients,
			"transaction_payloads": report.Transactions,
		})
	}
	return nil
}
