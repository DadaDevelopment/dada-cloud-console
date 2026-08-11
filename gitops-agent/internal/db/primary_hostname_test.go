package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPrimaryHostname_PublicApiFallback exercises PrimaryHostname against a
// real Postgres: with no domain_hostnames row the fqdn of a PublicApi snapshot
// tagged with the app's app_name is returned (git-originated domains) with
// status "unknown", an existing domain_hostnames row always wins over the
// fallback and carries its own status/status_reason, a PublicApi snapshot for
// a different app_name never leaks, and an app with neither source yields a
// zero PrimaryHostnameInfo. Skipped unless TEST_DATABASE_URL is set,
// mirroring TestReapExpiredPreviewEnvs (opt-in, since CI runs without
// Docker).
func TestPrimaryHostname_PublicApiFallback(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	applyMigrationsForReapTest(t, ctx, pool)

	projectID := uuid.New()
	execReap(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])
	envID := seedPreviewEnv(t, ctx, pool, projectID, false, time.Now().Add(time.Hour))

	const domainBase = "dada-tuda.ru"

	got, err := PrimaryHostname(ctx, pool, envID, "profi", domainBase)
	if err != nil {
		t.Fatalf("PrimaryHostname (empty): %v", err)
	}
	if got != (PrimaryHostnameInfo{}) {
		t.Errorf("empty state = %+v, want zero value", got)
	}

	seedPublicApiSnapshot(t, ctx, pool, projectID, envID, "profi-backend", "profi-backend", "profi-backend.dada-tuda.ru")
	seedPublicApiSnapshot(t, ctx, pool, projectID, envID, "profi", "profi", "profi.dada-tuda.ru")

	got, err = PrimaryHostname(ctx, pool, envID, "profi", domainBase)
	if err != nil {
		t.Fatalf("PrimaryHostname (fallback): %v", err)
	}
	want := PrimaryHostnameInfo{Hostname: "profi.dada-tuda.ru", Status: "unknown"}
	if got != want {
		t.Errorf("fallback = %+v, want %+v", got, want)
	}

	got, err = PrimaryHostname(ctx, pool, envID, "no-such-app", domainBase)
	if err != nil {
		t.Fatalf("PrimaryHostname (foreign app): %v", err)
	}
	if got != (PrimaryHostnameInfo{}) {
		t.Errorf("foreign app = %+v, want zero value", got)
	}

	execReap(t, ctx, pool,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status)
		 VALUES ($1, 'profi', 'profi-abc123.dada-tuda.ru', 'CNAME', 'active')`,
		envID)

	got, err = PrimaryHostname(ctx, pool, envID, "profi", domainBase)
	if err != nil {
		t.Fatalf("PrimaryHostname (row wins): %v", err)
	}
	want = PrimaryHostnameInfo{Hostname: "profi-abc123.dada-tuda.ru", Status: "active"}
	if got != want {
		t.Errorf("hostname with domain_hostnames row = %+v, want %+v", got, want)
	}
}

// TestPrimaryHostname_StatusReason exercises the status/status_reason mapping
// directly: a pending row with a reason surfaces both verbatim, a failed row
// surfaces its own reason, and an active custom domain outranks a pending
// surrogate even though the surrogate was created first.
func TestPrimaryHostname_StatusReason(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	applyMigrationsForReapTest(t, ctx, pool)

	projectID := uuid.New()
	execReap(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "p-"+projectID.String()[:8])
	envID := seedPreviewEnv(t, ctx, pool, projectID, false, time.Now().Add(time.Hour))

	const domainBase = "dada-tuda.ru"

	execReap(t, ctx, pool,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status, status_reason)
		 VALUES ($1, 'pend-app', 'pend-app.dada-tuda.ru', 'CNAME', 'pending', 'dns_not_pointed')`,
		envID)

	got, err := PrimaryHostname(ctx, pool, envID, "pend-app", domainBase)
	if err != nil {
		t.Fatalf("PrimaryHostname (pending): %v", err)
	}
	want := PrimaryHostnameInfo{Hostname: "pend-app.dada-tuda.ru", Status: "pending", Reason: "dns_not_pointed"}
	if got != want {
		t.Errorf("pending = %+v, want %+v", got, want)
	}

	execReap(t, ctx, pool,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status, status_reason)
		 VALUES ($1, 'failed-app', 'failed-app.example.com', 'CNAME', 'failed', 'attach_timeout')`,
		envID)

	got, err = PrimaryHostname(ctx, pool, envID, "failed-app", domainBase)
	if err != nil {
		t.Fatalf("PrimaryHostname (failed): %v", err)
	}
	want = PrimaryHostnameInfo{Hostname: "failed-app.example.com", Status: "failed", Reason: "attach_timeout"}
	if got != want {
		t.Errorf("failed = %+v, want %+v", got, want)
	}

	execReap(t, ctx, pool,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status)
		 VALUES ($1, 'mixed-app', 'mixed-app.dada-tuda.ru', 'CNAME', 'pending')`,
		envID)
	execReap(t, ctx, pool,
		`INSERT INTO domain_hostnames (environment_id, app_name, hostname, record_type, status)
		 VALUES ($1, 'mixed-app', 'mixed-app.example.com', 'CNAME', 'active')`,
		envID)

	got, err = PrimaryHostname(ctx, pool, envID, "mixed-app", domainBase)
	if err != nil {
		t.Fatalf("PrimaryHostname (active outranks pending): %v", err)
	}
	want = PrimaryHostnameInfo{Hostname: "mixed-app.example.com", Status: "active"}
	if got != want {
		t.Errorf("active outranks pending = %+v, want %+v", got, want)
	}
}

// seedPublicApiSnapshot inserts a PublicApi resource_snapshots row shaped like
// the ones the git watcher / status reconciler write (spec.dns.fqdn + app_name).
func seedPublicApiSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	projectID, envID uuid.UUID, name, appName, fqdn string,
) {
	t.Helper()
	execReap(t, ctx, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'PublicApi', $3, 'Ready',
		         jsonb_build_object('app_name', $4::text, 'spec',
		                            jsonb_build_object('dns', jsonb_build_object('fqdn', $5::text, 'enabled', true))))`,
		projectID, envID, name, appName, fqdn)
}
