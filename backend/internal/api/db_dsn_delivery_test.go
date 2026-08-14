package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

// fakeDBCredsResolver lets the delivery tests control what dbcreds.Resolve
// returns without a real cluster: notReady simulates the secret not
// existing yet, err simulates any other failure, and everything else
// returns creds.
type fakeDBCredsResolver struct {
	creds    cloudtask.DBCredentials
	notReady bool
	err      error
}

func (f fakeDBCredsResolver) Resolve(context.Context, string, string) (cloudtask.DBCredentials, error) {
	if f.notReady {
		return cloudtask.DBCredentials{}, cloudtask.ErrDBCredentialsNotReady
	}
	if f.err != nil {
		return cloudtask.DBCredentials{}, f.err
	}
	return f.creds, nil
}

func seedServiceDatabaseSnapshot(t *testing.T, h *Handler, projectID, envID uuid.UUID, name, appRef, namespace, datname string) {
	t.Helper()
	summary, err := json.Marshal(map[string]any{
		"name": name,
		"kind": "ServiceDatabaseV2",
		"spec": map[string]any{
			"appRef":    appRef,
			"database":  datname,
			"namespace": namespace,
		},
	})
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'ServiceDatabaseV2', $3, 'Ready', $4)`,
		projectID, envID, name, summary,
	); err != nil {
		t.Fatalf("seed ServiceDatabaseV2 snapshot: %v", err)
	}
}

func TestAppEnvVarIsSet(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	seedApp(t, pool, projectID, envID, "megafactory")

	set, err := h.appEnvVarIsSet(context.Background(), envID, "megafactory", "DATABASE_URL")
	if err != nil {
		t.Fatalf("appEnvVarIsSet on an absent key: %v", err)
	}
	if set {
		t.Fatalf("appEnvVarIsSet = true before any value was written")
	}

	if err := h.seedEnvVar(context.Background(), envID, "megafactory", "DATABASE_URL", "postgresql://u:p@h:5432/d", userID); err != nil {
		t.Fatalf("seedEnvVar: %v", err)
	}
	set, err = h.appEnvVarIsSet(context.Background(), envID, "megafactory", "DATABASE_URL")
	if err != nil {
		t.Fatalf("appEnvVarIsSet after seeding: %v", err)
	}
	if !set {
		t.Fatalf("appEnvVarIsSet = false right after seedEnvVar wrote a value")
	}
}

// The live incident this defends against: a database was created, its app
// already had a DATABASE_URL the user had pasted by hand (wrong, but theirs),
// and the delivery path must never clobber it.
func TestSeedDatabaseDSNIfAbsent_NeverOverwritesAnExistingValue(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	seedApp(t, pool, projectID, envID, "megafactory")

	if err := h.seedEnvVar(context.Background(), envID, "megafactory", "DATABASE_URL", "postgresql://user-typed-this", userID); err != nil {
		t.Fatalf("seedEnvVar (user value): %v", err)
	}

	seeded, err := h.seedDatabaseDSNIfAbsent(context.Background(), projectID, envID, "pg", "megafactory", "postgresql://auto-generated", userID, "auto")
	if err != nil {
		t.Fatalf("seedDatabaseDSNIfAbsent: %v", err)
	}
	if seeded {
		t.Fatalf("seedDatabaseDSNIfAbsent reported seeded=true over an existing value")
	}

	var encrypted []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT value_encrypted FROM env_vars WHERE environment_id = $1 AND app_name = $2 AND key = 'DATABASE_URL'`,
		envID, "megafactory",
	).Scan(&encrypted); err != nil {
		t.Fatalf("read back env var: %v", err)
	}
	outcome, reason, _ := lastAuditRow(t, pool, projectID, auditActionSeedDatabaseDSN)
	if outcome != auditOutcomeSuccess || reason != "already_set" {
		t.Fatalf("audit row = (%q, %q), want (success, already_set)", outcome, reason)
	}
}

func TestSeedDatabaseDSNIfAbsent_WritesWhenAbsentAndIsIdempotent(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	seedApp(t, pool, projectID, envID, "megafactory")

	const dsn = "postgresql://dada:pw@pg-router.databases.svc.cluster.local:5432/megafactory"
	seeded, err := h.seedDatabaseDSNIfAbsent(context.Background(), projectID, envID, "pg", "megafactory", dsn, userID, "auto")
	if err != nil {
		t.Fatalf("seedDatabaseDSNIfAbsent (first call): %v", err)
	}
	if !seeded {
		t.Fatalf("seedDatabaseDSNIfAbsent reported seeded=false on an absent var")
	}
	outcome, _, _ := lastAuditRow(t, pool, projectID, auditActionSeedDatabaseDSN)
	if outcome != auditOutcomeSuccess {
		t.Fatalf("audit outcome = %q, want success", outcome)
	}

	seeded, err = h.seedDatabaseDSNIfAbsent(context.Background(), projectID, envID, "pg", "megafactory", "postgresql://a-second-different-dsn", userID, "reveal")
	if err != nil {
		t.Fatalf("seedDatabaseDSNIfAbsent (second call): %v", err)
	}
	if seeded {
		t.Fatalf("second call reported seeded=true; the first value must stick")
	}
}

func TestAttemptDatabaseDSNDelivery_MissingSnapshotIsDone(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}, dbcreds: fakeDBCredsResolver{err: errors.New("must not be called")}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)

	done, err := h.attemptDatabaseDSNDelivery(context.Background(), projectID, envID, "ghost", "megafactory", userID, "auto")
	if err != nil {
		t.Fatalf("attemptDatabaseDSNDelivery on a missing database row: %v", err)
	}
	if !done {
		t.Fatalf("attemptDatabaseDSNDelivery = not done for a database row that does not exist")
	}
}

func TestAttemptDatabaseDSNDelivery_SecretNotReadyKeepsRetrying(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}, dbcreds: fakeDBCredsResolver{notReady: true}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	seedApp(t, pool, projectID, envID, "megafactory")
	seedServiceDatabaseSnapshot(t, h, projectID, envID, "pg", "megafactory", "ns-x", "megafactory")

	done, err := h.attemptDatabaseDSNDelivery(context.Background(), projectID, envID, "pg", "megafactory", userID, "auto")
	if err != nil {
		t.Fatalf("attemptDatabaseDSNDelivery while the secret is not ready: %v", err)
	}
	if done {
		t.Fatalf("attemptDatabaseDSNDelivery = done while the connection secret is still not ready")
	}
}

func TestAttemptDatabaseDSNDelivery_ReadySeedsTheEnvVar(t *testing.T) {
	pool := testOptimisticPool(t)
	creds := cloudtask.DBCredentials{Endpoint: "pg-router.databases.svc.cluster.local", Port: "5432", Username: "dada", Password: "s3cr3t"}
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}, dbcreds: fakeDBCredsResolver{creds: creds}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	seedApp(t, pool, projectID, envID, "megafactory")
	seedServiceDatabaseSnapshot(t, h, projectID, envID, "pg", "megafactory", "ns-x", "megafactory")

	done, err := h.attemptDatabaseDSNDelivery(context.Background(), projectID, envID, "pg", "megafactory", userID, "auto")
	if err != nil {
		t.Fatalf("attemptDatabaseDSNDelivery once the secret is ready: %v", err)
	}
	if !done {
		t.Fatalf("attemptDatabaseDSNDelivery = not done once credentials resolved")
	}
	set, err := h.appEnvVarIsSet(context.Background(), envID, "megafactory", "DATABASE_URL")
	if err != nil {
		t.Fatalf("appEnvVarIsSet after delivery: %v", err)
	}
	if !set {
		t.Fatalf("DATABASE_URL was not seeded after a successful delivery attempt")
	}
}
