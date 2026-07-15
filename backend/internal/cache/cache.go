// Package cache is a small, fail-open Redis cache-aside layer for read-heavy
// API responses. It exists to shave tail latency off endpoints whose work is an
// expensive external round-trip (OpenCost aggregation, Prometheus/Mimir range
// queries, OpenSearch searches) whose results tolerate a few seconds of
// staleness.
//
// Two invariants keep it safe to wrap any handler:
//
//   - Nil-safe: New returns nil when REDIS_ADDR is unset, mirroring the other
//     optional clients on Handler. Every method is a no-op / passthrough on a
//     nil receiver, so the cache can be wired unconditionally.
//   - Fail-open: a Redis miss, timeout or unmarshal error never surfaces to the
//     caller. Fetch always falls back to computing the fresh value, so Redis
//     being down degrades latency, never correctness or availability.
package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

var ops = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cache_ops_total",
	Help: "Cache-aside outcomes by result: hit (served from Redis), miss (computed and stored), error (Redis unavailable, fell back to compute).",
}, []string{"result"})

// Cache is a Redis-backed cache-aside store. A nil *Cache is valid and behaves
// as a disabled (always-miss) cache. The opTimeout field bounds each Redis call
// so a slow or hung Redis can never add more latency than it saves; on timeout
// Fetch falls back to computing the fresh value.
type Cache struct {
	rdb       *redis.Client
	opTimeout time.Duration
}

// New builds a Cache from a Redis address (host:port). Returns nil when addr is
// empty so callers can treat caching as disabled without a branch. The returned
// Cache is usable immediately; the connection is established lazily on first use.
func New(addr string) *Cache {
	if addr == "" {
		return nil
	}
	return &Cache{
		rdb: redis.NewClient(&redis.Options{
			Addr:         addr,
			DialTimeout:  1 * time.Second,
			ReadTimeout:  1 * time.Second,
			WriteTimeout: 1 * time.Second,
			PoolSize:     10,
			MaxRetries:   -1,
		}),
		opTimeout: 1 * time.Second,
	}
}

// Enabled reports whether the cache is active (non-nil with a client).
func (c *Cache) Enabled() bool { return c != nil && c.rdb != nil }

// Ping verifies connectivity, for startup logging. Safe on a nil cache.
func (c *Cache) Ping(ctx context.Context) error {
	if !c.Enabled() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	return c.rdb.Ping(ctx).Err()
}

// Close releases the Redis connection pool. Safe on a nil cache.
func (c *Cache) Close() error {
	if !c.Enabled() {
		return nil
	}
	return c.rdb.Close()
}

// Fetch is the cache-aside primitive: return the JSON value stored at key, or
// call compute, store its result under key with ttl, and return it. It is
// generic over the value type so handlers get a typed result with no manual
// (un)marshalling.
//
// Fail-open by design: any Redis error (down, timeout) or a corrupt cached
// entry is logged at debug and treated as a miss, so the endpoint still serves
// a freshly computed value. compute's error is returned to the caller unwrapped.
func Fetch[T any](ctx context.Context, c *Cache, key string, ttl time.Duration, compute func() (T, error)) (T, error) {
	if !c.Enabled() {
		return compute()
	}

	if v, ok := get[T](ctx, c, key); ok {
		ops.WithLabelValues("hit").Inc()
		return v, nil
	}

	v, err := compute()
	if err != nil {
		return v, err
	}
	set(ctx, c, key, ttl, v)
	ops.WithLabelValues("miss").Inc()
	return v, nil
}

// Store writes v under key with ttl, bypassing the read side. Used by proactive
// cache warmers that compute a value on a schedule and populate the cache so
// user requests always hit. No-op on a disabled cache; a Redis error is
// swallowed (the value is recomputed on the next warm tick or request).
func Store[T any](ctx context.Context, c *Cache, key string, ttl time.Duration, v T) {
	if !c.Enabled() {
		return
	}
	set(ctx, c, key, ttl, v)
}

func get[T any](ctx context.Context, c *Cache, key string) (T, bool) {
	var zero T
	rctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()

	b, err := c.rdb.Get(rctx, key).Bytes()
	if err == redis.Nil {
		return zero, false
	}
	if err != nil {
		ops.WithLabelValues("error").Inc()
		log.Debug().Err(err).Str("key", key).Msg("cache: get failed, falling back to compute")
		return zero, false
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		log.Debug().Err(err).Str("key", key).Msg("cache: unmarshal failed, treating as miss")
		return zero, false
	}
	return v, true
}

func set[T any](ctx context.Context, c *Cache, key string, ttl time.Duration, v T) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, c.opTimeout)
	defer cancel()
	if err := c.rdb.Set(rctx, key, b, ttl).Err(); err != nil {
		ops.WithLabelValues("error").Inc()
		log.Debug().Err(err).Str("key", key).Msg("cache: set failed")
	}
}
