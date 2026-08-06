package api

import (
	"context"
	"errors"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Advisory lock keys for the backend's background loops. Each ticker whose
// body has non-idempotent side effects (DNS/domain writes, alert emails,
// Kanister ActionSets) takes its key with pg_try_advisory_lock before running,
// so with replicas > 1 exactly one pod fires per tick and the others skip.
// Idempotent loops (billing meter upsert, reveal-row cleanup, metrics
// collector) deliberately run unguarded on every replica.
//
// The cost cache warmer is guarded despite being idempotent, which is the one
// exception to that split: its result goes to SHARED Redis, so a second
// replica computing it adds no value, and its cost is not local CPU but a
// multi-minute OpenCost/Mimir aggregation. Unguarded on two replicas it
// doubled the load on the cluster's single busiest component.
//
// Keys are arbitrary but must stay distinct and stable across versions: a
// rolling deploy briefly runs old and new pods against the same database.
const (
	lockKeyDomainReconcile   int64 = 0x64616461_0001
	lockKeyBackupReconcile   int64 = 0x64616461_0002
	lockKeyAppHealthWatch    int64 = 0x64616461_0003
	lockKeyAppVolumeWatch    int64 = 0x64616461_0004
	lockKeyCostWarmFast      int64 = 0x64616461_0005
	lockKeyCostWarmSlow      int64 = 0x64616461_0006
	lockKeyAppAutoscaleWatch int64 = 0x64616461_0007
	lockKeyIdentityDelivery  int64 = 0x64616461_0008
	lockKeyDemoAppReap       int64 = 0x64616461_0009
	lockKeyAgentChatFold     int64 = 0x64616461_000A
	lockKeyAppURLWatch       int64 = 0x64616461_000B
	lockKeyDBQuotaWatch      int64 = 0x64616461_000C
)

// runWithAdvisoryLock executes fn while holding the session-scoped Postgres
// advisory lock key on a dedicated pooled connection, or does nothing when
// another session (replica) already holds it. Returns whether fn ran.
//
// A nil pool is a no-op: callers construct a Handler for route-enumeration
// purposes (e.g. the OpenAPI coverage test) without a live database, and the
// background loops started from NewHandler must not crash a caller who never
// asked for a working pool.
//
// The unlock uses context.WithoutCancel so a canceled tick context cannot
// leak the lock into the pool; if the unlock still fails, the underlying
// connection is torn down instead of being returned holding the lock, since a
// pooled session that silently keeps an advisory lock would starve every
// replica (including this one) forever.
func runWithAdvisoryLock(ctx context.Context, pool *pgxpool.Pool, key int64, name string, fn func(context.Context)) bool {
	if pool == nil {
		return false
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		log.Warn().Err(err).Str("loop", name).Msg("advisory lock: acquire connection failed")
		return false
	}
	locked := false
	defer func() {
		if locked {
			if _, err := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, key); err != nil {
				log.Warn().Err(err).Str("loop", name).Msg("advisory lock: unlock failed, discarding connection")
				conn.Conn().Close(context.WithoutCancel(ctx))
			}
		}
		conn.Release()
	}()
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		log.Warn().Err(err).Str("loop", name).Msg("advisory lock: try-lock query failed")
		return false
	}
	if !locked {
		return false
	}
	fn(ctx)
	return true
}

// RunDomainMaintenanceTick runs one pass of the custom-domain background
// loops (TXT verification, hostname reconcile, active-route revalidation,
// delegation poll, default-domain backfill) under the domain-reconcile
// advisory lock, so with multiple backend replicas only one runs the pass per
// tick. Called from main's DNS ticker.
func RunDomainMaintenanceTick(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) {
	runWithAdvisoryLock(ctx, pool, lockKeyDomainReconcile, "domain-maintenance", func(ctx context.Context) {
		if err := VerifyPendingDomains(ctx, pool, cfg); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn().Err(err).Msg("custom-domain DNS verification failed")
		}
		if err := ReconcilePendingHostnames(ctx, pool, cfg); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn().Err(err).Msg("custom-domain hostname reconcile failed")
		}
		if err := RevalidateActiveHostnameRoutes(ctx, pool); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn().Err(err).Msg("active hostname route revalidation failed")
		}
		if err := PollPendingDelegations(ctx, pool, cfg); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn().Err(err).Msg("managed-dns delegation poll failed")
		}
		if err := BackfillMissingDefaultDomains(ctx, pool, cfg); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn().Err(err).Msg("default-domain backfill failed")
		}
	})
}
