package api

import (
	"context"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// MeterUsage snapshots current resource counts for every org that owns at
// least one project or has a billing_accounts row, then upserts usage_records
// idempotent on (org_id, resource, period_start).
// period_start is the start of the current UTC hour.
//
// This function is safe to call concurrently; each call is a full snapshot.
// Gaps (e.g. Prometheus unavailable for storage) are skipped so the record
// is absent rather than double-counted on the next run.
func MeterUsage(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, plans []pricing.Plan) {
	periodStart := time.Now().UTC().Truncate(time.Hour)

	orgs, err := meterResolveOrgs(ctx, pool)
	if err != nil {
		log.Warn().Err(err).Msg("billing meter: failed to resolve orgs")
		return
	}

	countable := []string{"apps", "databases", "domains"}
	for _, orgID := range orgs {
		for _, resource := range countable {
			count, err := meterCountResource(ctx, pool, orgID, resource)
			if err != nil {
				log.Warn().Err(err).Str("org", orgID).Str("resource", resource).Msg("billing meter: count failed")
				continue
			}
			meterUpsert(ctx, pool, orgID, resource, count, periodStart)
		}
	}
}

func meterResolveOrgs(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT org_id FROM (
			SELECT org_id FROM billing_accounts WHERE org_id IS NOT NULL
			UNION
			SELECT org_id FROM projects WHERE org_id IS NOT NULL
		) sub
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orgs []string
	for rows.Next() {
		var org string
		if err := rows.Scan(&org); err != nil {
			continue
		}
		if org != "" {
			orgs = append(orgs, org)
		}
	}
	return orgs, rows.Err()
}

func meterCountResource(ctx context.Context, pool *pgxpool.Pool, orgID, resource string) (int, error) {
	switch resource {
	case "apps":
		var n int
		err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM resource_snapshots rs
			JOIN projects p ON p.id = rs.project_id
			WHERE p.org_id = $1 AND rs.kind = 'App'
		`, orgID).Scan(&n)
		return n, err
	case "databases":
		var n int
		err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM resource_snapshots rs
			JOIN projects p ON p.id = rs.project_id
			WHERE p.org_id = $1 AND rs.kind IN ('ServiceDatabase', 'ServiceDatabaseV2')
		`, orgID).Scan(&n)
		return n, err
	case "domains":
		var n int
		err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM domain_authorizations da
			JOIN projects p ON p.id = da.project_id
			WHERE p.org_id = $1
		`, orgID).Scan(&n)
		return n, err
	}
	return 0, nil
}

func meterUpsert(ctx context.Context, pool *pgxpool.Pool, orgID, resource string, used int, periodStart time.Time) {
	_, err := pool.Exec(ctx, `
		INSERT INTO usage_records (org_id, resource, used, period_start)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, resource, period_start) DO UPDATE
		  SET used        = EXCLUDED.used,
		      recorded_at = NOW()
	`, orgID, resource, used, periodStart)
	if err != nil {
		log.Warn().Err(err).
			Str("org", orgID).
			Str("resource", resource).
			Msg("billing meter: upsert usage_records failed")
	}
}
