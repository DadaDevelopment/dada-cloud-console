package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultMaxConns is the pool size used when the connection string does not
// carry an explicit pool_max_conns.
//
// pgxpool's own default is max(4, runtime.NumCPU()), and runtime.NumCPU()
// reports the HOST's cores, not the container's CPU limit. On this cluster
// that made the pool 7 connections on a 7-core node and 12 on a 12-core one
// for the very same image, so how much concurrency the backend could sustain
// depended on where the scheduler happened to place the pod. A fixed number
// makes the pool a property of the service instead of a property of the node.
//
// The value is deliberately modest: the platform database is shared, and the
// backend runs two replicas.
const DefaultMaxConns int32 = 10

// Connect creates and validates a PostgreSQL connection pool.
func Connect(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parsing db url: %w", err)
	}
	if !strings.Contains(dbURL, "pool_max_conns") {
		cfg.MaxConns = DefaultMaxConns
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}
