// Command migrate applies the SQL migrations in ./migrations and exits.
//
// It exists for two callers that must not have to boot the whole server just to
// get a schema:
//
//   - CI, where a postgres sidecar comes up alongside the build and the DB-backed
//     tests need a schema before `go test` runs;
//   - a developer who wants to reset a scratch database.
//
// It retries the initial connection, because in CI the sidecar and the build
// container start together and Jenkins does not wait for sidecar readiness before
// running steps. Retrying here means CI needs no psql or pg_isready in the build
// image, and no sleep guess in the Jenkinsfile.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/dada-tuda/console/backend/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dir := flag.String("dir", "migrations", "directory holding the .sql migrations")
	wait := flag.Duration("wait", 90*time.Second, "how long to keep retrying the initial connection")
	flag.Parse()

	// DATABASE_URL is what the server uses; TEST_DATABASE_URL is what the
	// DB-backed tests use. Accept either so the same command serves CI and a
	// developer's scratch database without a wrapper.
	dbURL := firstNonEmpty(os.Getenv("DATABASE_URL"), os.Getenv("TEST_DATABASE_URL"))
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "migrate: set DATABASE_URL or TEST_DATABASE_URL")
		os.Exit(2)
	}

	ctx := context.Background()
	pool, err := connectWithRetry(ctx, dbURL, *wait)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: connect: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.RunMigrations(ctx, pool, *dir); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("migrate: schema up to date")
}

func connectWithRetry(ctx context.Context, dbURL string, wait time.Duration) (*pgxpool.Pool, error) {
	deadline := time.Now().Add(wait)
	var lastErr error
	for attempt := 1; ; attempt++ {
		pool, err := db.Connect(ctx, dbURL)
		if err == nil {
			return pool, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("gave up after %s: %w", wait, lastErr)
		}
		fmt.Fprintf(os.Stderr, "migrate: attempt %d failed, retrying: %v\n", attempt, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
