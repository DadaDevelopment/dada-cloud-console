package api

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"time"
)

// Shard states, mirroring the db_shards.state check constraint.
const (
	dbShardStateOpen     = "open"
	dbShardStateClosed   = "closed"
	dbShardStateDraining = "draining"
)

// dbShard is one PostgreSQL instance a managed database can live on.
//
// MetricsSelector is the PromQL label matcher that isolates this instance's
// series in pg_database_size_bytes; placement sums it to find the emptiest
// shard. CapacityBytes of 0 means the shard is bounded only by its volume.
type dbShard struct {
	Name            string
	State           string
	IsPlatform      bool
	CapacityBytes   int64
	MetricsSelector string
}

// tenantShardCandidates returns the shards a tenant database may be placed on:
// open, not reserved for the platform. Ordered by name so placement is
// deterministic when several shards are equally empty.
func (h *Handler) tenantShardCandidates(ctx context.Context) ([]dbShard, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT name, state, is_platform, capacity_bytes, metrics_selector
		   FROM db_shards
		  WHERE state = $1 AND is_platform = FALSE
		  ORDER BY name`, dbShardStateOpen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []dbShard
	for rows.Next() {
		var s dbShard
		if err := rows.Scan(&s.Name, &s.State, &s.IsPlatform, &s.CapacityBytes, &s.MetricsSelector); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// shardUsedBytes measures every candidate shard once. A shard whose selector
// matches nothing is simply absent from the result: an unmeasurable shard must
// not look infinitely large (it would never receive a database again) nor
// abort placement, so callers treat "missing" as unknown and fall back to the
// order of the registry.
func (h *Handler) shardUsedBytes(ctx context.Context, shards []dbShard) map[string]int64 {
	used := make(map[string]int64, len(shards))
	if h.prometheus == nil {
		return used
	}
	for _, s := range shards {
		if s.MetricsSelector == "" {
			continue
		}
		q := fmt.Sprintf("sum(pg_database_size_bytes{%s})", s.MetricsSelector)
		samples, err := h.prometheus.QueryInstant(ctx, q, time.Time{}, "")
		if err != nil {
			log.Printf("db-shards: size query for %s failed: %v", s.Name, err)
			continue
		}
		for _, sample := range samples {
			v := sample.Point.V
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				continue
			}
			used[s.Name] = int64(v)
			break
		}
	}
	return used
}

// pickTenantShard chooses the shard a new tenant database lands on: the open
// shard with the least data on it, skipping any shard already at its declared
// capacity. Pure, so the rule is testable without Prometheus or a database.
//
// A shard with no measurement counts as empty. That is the deliberate choice:
// a freshly added shard has no series yet and should attract the next database,
// and a broken exporter must not silently freeze placement onto one instance.
// Ties (including "nothing is measured at all") resolve to the first shard by
// name, so placement is reproducible.
//
// Returns "" when no shard qualifies — the caller then omits spec.shard and the
// XRD default applies, i.e. exactly today's placement.
func pickTenantShard(candidates []dbShard, used map[string]int64) string {
	sorted := make([]dbShard, len(candidates))
	copy(sorted, candidates)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	best := ""
	var bestUsed int64
	for _, s := range sorted {
		u := used[s.Name]
		if s.CapacityBytes > 0 && u >= s.CapacityBytes {
			continue
		}
		if best == "" || u < bestUsed {
			best = s.Name
			bestUsed = u
		}
	}
	return best
}

// placeTenantDatabaseShard resolves the shard for a database about to be
// created. Placement is automatic and only automatic: there is no request field
// and no console control that can override it, because a shard chosen by hand
// is a shard nobody rebalances.
//
// Every failure degrades to "" (no spec.shard → the XRD default, the shared
// instance), so a registry or Prometheus problem can never block a tenant from
// creating a database.
func (h *Handler) placeTenantDatabaseShard(ctx context.Context) string {
	candidates, err := h.tenantShardCandidates(ctx)
	if err != nil {
		log.Printf("db-shards: candidate lookup failed, falling back to default shard: %v", err)
		return ""
	}
	if len(candidates) == 0 {
		return ""
	}
	return pickTenantShard(candidates, h.shardUsedBytes(ctx, candidates))
}
