package api

import (
	"context"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// meterAlignOffset delays each aligned run past the boundary it belongs to.
// MeterUsage stamps the bucket as time.Now().Truncate(time.Hour), so a replica
// whose clock runs a hair fast would otherwise fire at 12:59:59.98 and write the
// 12:00 row a second time while 13:00 stays empty.
const meterAlignOffset = 30 * time.Second

// NextMeterDelay returns how long to wait before the next metering run, aligned
// to wall-clock interval boundaries rather than to the moment this process
// started.
//
// The loop used to be a plain time.Ticker, which inherits the pod's start phase:
// a deploy at :36 moved every subsequent run to :36 forever, and the hour
// buckets straddling the restart got no usage_records row at all. Prod lost
// 17:00 and 18:00 on 2026-08-03 to exactly that, which is why the console's
// usage bars went stale for two hours. Alignment makes a restart cost at most
// the current bucket, and the caller covers that one by metering immediately on
// startup.
//
// Missed buckets are deliberately NOT backfilled. Three of the four metered
// resources are stocks -- how many apps exist right now -- and the count an hour
// ago is unrecoverable once the hour has passed. Writing today's number into
// yesterday's row would turn a visible gap into an invisible lie.
func NextMeterDelay(now time.Time, interval time.Duration) time.Duration {
	if interval <= 0 {
		interval = time.Hour
	}
	offset := meterAlignOffset
	if offset >= interval {
		offset = 0
	}
	utc := now.UTC()
	next := utc.Truncate(interval).Add(interval + offset)
	for !next.After(utc) {
		next = next.Add(interval)
	}
	return next.Sub(utc)
}

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

	// box_minutes joins the same list as apps/databases/domains rather than getting
	// a meter of its own, and that ONE-WORD ADDITION is why every existing
	// /billing/* surface (account quotas+usage, invoice preview, the console's usage
	// bars) shows box minutes without another line of code: they all read
	// usage_records, keyed by (org, resource, period_start).
	//
	// It is nonetheless a different KIND of number from its neighbours: apps and
	// domains are stocks (how many exist right now), box_minutes is a flow (how many
	// were billed since the first of the month). Both are correct as "the value of
	// this resource at this hour", which is exactly what a usage_records row means,
	// so no new shape was needed — see meterCountResource.
	countable := []string{"apps", "databases", "domains", "box_minutes"}
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
			WHERE p.org_id = $1 AND rs.kind = 'App' AND `+notOrphanedSnapshot+`
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

	case "box_minutes":
		return countOrgBoxMinutes(ctx, pool, orgID, time.Now().UTC())
	}
	return 0, nil
}

// countOrgBoxMinutes counts the org's BILLED ACTIVE box minutes in the calendar
// month containing now. Shared by the meter (usage_records) and by the quota gate
// (checkQuota -> countResource) so the number a customer is shown and the number
// they are gated on cannot disagree.
//
// Only kind='active' counts. suspended_disk rows are storage accrual for a box that
// is asleep, and folding them in would consume a customer's minute allowance while
// they were not using anything — which is precisely the bill-shock the "idle is not
// billed" promise rules out. The disk accrual is still visible as money in
// /billing/consumption and still enforced by the per-box spend cap; it just is not
// a minute of use.
//
// The ledger carries org_id itself (denormalized in migration 063), so this survives
// the deletion of the project and the box it describes.
func countOrgBoxMinutes(ctx context.Context, pool *pgxpool.Pool, orgID string, now time.Time) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM box_usage
		 WHERE org_id = $1 AND kind = $2 AND minute_start >= $3
	`, orgID, boxUsageKindActive, monthStart(now)).Scan(&n)
	return n, err
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
