package api

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testAdvisoryPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping advisory-lock DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestRunWithAdvisoryLockExcludesSecondHolder(t *testing.T) {
	poolA := testAdvisoryPool(t)
	poolB := testAdvisoryPool(t)
	ctx := context.Background()
	key := int64(0x7e57_0001)

	ranA := runWithAdvisoryLock(ctx, poolA, key, "test-a", func(ctx context.Context) {
		if ran := runWithAdvisoryLock(ctx, poolB, key, "test-b", func(context.Context) {
			t.Errorf("second session must not run while lock is held")
		}); ran {
			t.Errorf("runWithAdvisoryLock reported ran=true for contended lock")
		}
	})
	if !ranA {
		t.Fatalf("first session failed to take a free lock")
	}

	if ran := runWithAdvisoryLock(ctx, poolB, key, "test-b", func(context.Context) {}); !ran {
		t.Fatalf("lock was not released back to the pool after the first run")
	}
}

func TestClaimAppHealthAlertSlotCooldown(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	ns := "test-ns-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_health_alerts WHERE namespace = $1`, ns)
	})

	if !claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod/web", 24*time.Hour) {
		t.Fatalf("first claim for a fresh app must succeed")
	}
	if claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod/web", 24*time.Hour) {
		t.Fatalf("second claim inside cooldown must be rejected")
	}
	if !claimAppHealthAlertSlot(ctx, pool, ns, "api", "OOMKilled", "pod/api", 24*time.Hour) {
		t.Fatalf("claim for a different app must be independent")
	}

	if _, err := pool.Exec(ctx,
		`UPDATE app_health_alerts SET last_sent_at = now() - interval '25 hours'
		 WHERE namespace = $1 AND app_name = 'web'`, ns); err != nil {
		t.Fatalf("backdate cooldown row: %v", err)
	}
	if !claimAppHealthAlertSlot(ctx, pool, ns, "web", "CrashLoopBackOff", "pod/web", 24*time.Hour) {
		t.Fatalf("claim after cooldown expiry must succeed")
	}
}

func TestManagedDomainHostnameUniquePerEnvApp(t *testing.T) {
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"ha-dup-test-"+suffix).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		dropSeededProject(pool, projectID)
	})

	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+suffix).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM domain_hostnames WHERE environment_id = $1`, envID)
	})

	insert := func(hostname string) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, managed)
			 VALUES (NULL, $1, 'web', $2, 'CNAME', 'pending', 'pending', true)`,
			envID, hostname)
		return err
	}
	if err := insert("web-aaaa" + suffix + ".test.example"); err != nil {
		t.Fatalf("first managed hostname insert must succeed: %v", err)
	}
	if err := insert("web-bbbb" + suffix + ".test.example"); err == nil {
		t.Fatalf("second managed hostname for same (env, app) must violate uniq_domain_hostnames_managed_env_app")
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, managed)
		 VALUES (NULL, $1, 'web', $2, 'CNAME', 'pending', 'pending', false)`,
		envID, "custom-"+suffix+".test.example"); err != nil {
		t.Fatalf("unmanaged (custom) hostname for the same app must stay allowed: %v", err)
	}
}
