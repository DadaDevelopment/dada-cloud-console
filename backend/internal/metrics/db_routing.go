package metrics

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
)

// Reasons a managed database gets no line of its own in the router's table.
// They are published with an explicit zero rather than left absent, so the
// graph of "databases the router cannot address" is continuous and an alert
// resolves on a real zero instead of on missing data.
const (
	RouteDropAmbiguousName   = "ambiguous_name"
	RouteDropShardUnaddresed = "shard_unaddressed"
	RouteDropUnsafeName      = "unsafe_name"
)

var routeDropReasons = []string{
	RouteDropAmbiguousName,
	RouteDropShardUnaddresed,
	RouteDropUnsafeName,
}

var (
	dbRoutes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_db_routes",
		Help: "Databases with an explicit line in the connection router's table, i.e. databases living somewhere other than the default shard. Zero is normal before the first move.",
	})

	dbRoutesDropped = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_db_routes_dropped",
		Help: "Databases the router cannot address by name, by reason. Each one is served by the wildcard, so it reaches the default shard no matter where it actually lives: after a move that means connections land on an instance that no longer holds the data. Alert on >0.",
	}, []string{"reason"})

	dbDatabasesByShard = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_db_databases_by_shard",
		Help: "Managed databases per shard, as reported by the live CRs. This is the placement the router routes on, not the registry's intent.",
	}, []string{"shard"})

	dbShardCapacityBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_db_shard_capacity_bytes",
		Help: "Declared capacity of a shard from the db_shards registry. 0 means the shard is bounded only by its volume. Pair with pg_database_size_bytes to see how full an instance is.",
	}, []string{"shard"})

	dbShardState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_db_shard_state",
		Help: "1 for the state a shard is in (open|closed|draining). A shard that is not open takes no new databases, so all placement lands on the remaining open ones.",
	}, []string{"shard", "state", "is_platform"})
)

// SetDBRouting publishes what the router's routing table actually says. It is
// called from the handler that renders the table, so the numbers describe the
// file that was served rather than a second computation that could disagree
// with it.
func SetDBRouting(routed int, dropped map[string]int, perShard map[string]int) {
	dbRoutes.Set(float64(routed))

	for _, reason := range routeDropReasons {
		dbRoutesDropped.WithLabelValues(reason).Set(float64(dropped[reason]))
	}

	dbDatabasesByShard.Reset()
	for shard, n := range perShard {
		dbDatabasesByShard.WithLabelValues(shard).Set(float64(n))
	}
}

// collectDBShards refreshes the registry-side shard gauges. Unlike SetDBRouting
// these do not depend on the router asking for a table, so a shard that has
// been closed or given a capacity is visible even while nothing is being
// routed.
func collectDBShards(ctx context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(ctx,
		`SELECT name, state, is_platform, capacity_bytes FROM db_shards`)
	if err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: db_shards query failed")
		return
	}
	defer rows.Close()

	dbShardCapacityBytes.Reset()
	dbShardState.Reset()
	for rows.Next() {
		var name, state string
		var isPlatform bool
		var capacity int64
		if err := rows.Scan(&name, &state, &isPlatform, &capacity); err != nil {
			collectErrors.Inc()
			continue
		}
		dbShardCapacityBytes.WithLabelValues(name).Set(float64(capacity))
		dbShardState.WithLabelValues(name, state, strconv.FormatBool(isPlatform)).Set(1)
	}
	if err := rows.Err(); err != nil {
		collectErrors.Inc()
		log.Warn().Err(err).Msg("metrics: db_shards scan failed")
	}
}
