package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	if outcome != auditOutcomeSuccess || reason != "user_modified" {
		t.Fatalf("audit row = (%q, %q), want (success, user_modified)", outcome, reason)
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

// TestSeedDatabaseDSN_RefreshesOnlyThePlatformsOwnStaleValue pins the rule that
// decides whether the platform may rewrite a DATABASE_URL it wrote earlier.
//
// The 2026-08-15 measurement behind it: the credentials endpoint had started
// handing out db.pv.dada-tuda.ru with sslmode=require, while both live user
// apps still carried the pre-flag pg-router string with no sslmode. The stored
// value has to move for those, and must not move for anything the user touched.
func TestSeedDatabaseDSN_RefreshesOnlyThePlatformsOwnStaleValue(t *testing.T) {
	const freshDSN = "postgresql://svc-app:pw123@db.pv.dada-tuda.ru:5432/appdb?sslmode=require"

	cases := []struct {
		name       string
		existing   string
		wantWrite  bool
		wantReason string
	}{
		{
			name:       "no value yet is seeded",
			existing:   "",
			wantWrite:  true,
			wantReason: "seeded",
		},
		{
			name:       "our own pre-TLS string is refreshed",
			existing:   "postgresql://svc-app:pw123@pg-router.databases.svc.cluster.local:5432/appdb",
			wantWrite:  true,
			wantReason: "refreshed_stale_platform_dsn",
		},
		{
			name:       "our own shard-era string is refreshed",
			existing:   "postgresql://svc-app:pw123@pg-shard-0-postgresql.databases.svc.cluster.local:5432/appdb?sslmode=disable",
			wantWrite:  true,
			wantReason: "refreshed_stale_platform_dsn",
		},
		{
			name:       "identical value is left alone",
			existing:   freshDSN,
			wantWrite:  false,
			wantReason: "already_current",
		},
		{
			name:       "different password means the user edited it",
			existing:   "postgresql://svc-app:their-own-pw@pg-router.databases.svc.cluster.local:5432/appdb",
			wantWrite:  false,
			wantReason: "user_modified",
		},
		{
			name:       "different database means it is not this database",
			existing:   "postgresql://svc-app:pw123@pg-router.databases.svc.cluster.local:5432/some-other-db",
			wantWrite:  false,
			wantReason: "user_modified",
		},
		{
			name:       "a host we never issued is never rewritten",
			existing:   "postgresql://svc-app:pw123@db.neon.tech:5432/appdb?sslmode=require",
			wantWrite:  false,
			wantReason: "user_modified",
		},
		{
			name:       "free text the user pasted is never rewritten",
			existing:   "postgresql://user-typed-this",
			wantWrite:  false,
			wantReason: "user_modified",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testOptimisticPool(t)
			h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
			userID := seedUser(t, pool)
			projectID, envID := seedOptimisticFixture(t, pool)
			seedApp(t, pool, projectID, envID, "megafactory")

			if tc.existing != "" {
				if err := h.seedEnvVar(context.Background(), envID, "megafactory", "DATABASE_URL", tc.existing, userID); err != nil {
					t.Fatalf("seedEnvVar (existing value): %v", err)
				}
			}

			wrote, err := h.seedDatabaseDSNIfAbsent(context.Background(), projectID, envID, "pg", "megafactory", freshDSN, userID, "reveal")
			if err != nil {
				t.Fatalf("seedDatabaseDSNIfAbsent: %v", err)
			}
			if wrote != tc.wantWrite {
				t.Fatalf("wrote = %v, want %v", wrote, tc.wantWrite)
			}

			outcome, reason, _ := lastAuditRow(t, pool, projectID, auditActionSeedDatabaseDSN)
			if outcome != auditOutcomeSuccess {
				t.Fatalf("audit outcome = %q, want success", outcome)
			}
			if reason != tc.wantReason {
				t.Fatalf("audit reason = %q, want %q", reason, tc.wantReason)
			}

			stored, found, err := h.appEnvVarValue(context.Background(), envID, "megafactory", "DATABASE_URL")
			if err != nil {
				t.Fatalf("read back DATABASE_URL: %v", err)
			}
			if !found {
				if tc.existing != "" || tc.wantWrite {
					t.Fatalf("DATABASE_URL vanished")
				}
				return
			}
			want := tc.existing
			if tc.wantWrite {
				want = freshDSN
			}
			if stored != want {
				t.Fatalf("stored DATABASE_URL = %q, want %q", stored, want)
			}
		})
	}
}

// TestSeedDatabaseDSN_AuditNeverCarriesThePassword guards the audit metadata
// added alongside the refresh: it records which host the value moved from and
// to, and a DSN password must never ride along into audit_events.
func TestSeedDatabaseDSN_AuditNeverCarriesThePassword(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	seedApp(t, pool, projectID, envID, "megafactory")

	const secret = "sup3r-s3cret-pw"
	if err := h.seedEnvVar(context.Background(), envID, "megafactory", "DATABASE_URL",
		"postgresql://svc-app:"+secret+"@pg-router.databases.svc.cluster.local:5432/appdb", userID); err != nil {
		t.Fatalf("seedEnvVar: %v", err)
	}

	wrote, err := h.seedDatabaseDSNIfAbsent(context.Background(), projectID, envID, "pg", "megafactory",
		"postgresql://svc-app:"+secret+"@db.pv.dada-tuda.ru:5432/appdb?sslmode=require", userID, "reveal")
	if err != nil {
		t.Fatalf("seedDatabaseDSNIfAbsent: %v", err)
	}
	if !wrote {
		t.Fatalf("stale platform DSN was not refreshed")
	}

	meta := lastAuditMetadata(t, pool, projectID, auditActionSeedDatabaseDSN)
	if len(meta) == 0 {
		t.Fatalf("audit row carried no metadata")
	}
	if strings.Contains(meta, secret) {
		t.Fatalf("audit metadata leaked the DSN password: %s", meta)
	}
	for _, want := range []string{"pg-router.databases.svc.cluster.local", "db.pv.dada-tuda.ru"} {
		if !strings.Contains(meta, want) {
			t.Fatalf("audit metadata missing %q: %s", want, meta)
		}
	}
}

// lastAuditMetadata returns the newest audit row's metadata for action as JSON
// text, so a test can assert on what the row does and does not carry.
func lastAuditMetadata(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, action string) string {
	t.Helper()
	var meta map[string]any
	err := pool.QueryRow(context.Background(),
		`SELECT metadata FROM audit_events
		  WHERE project_id = $1 AND action = $2
		  ORDER BY created_at DESC LIMIT 1`,
		projectID, action,
	).Scan(&meta)
	if err != nil {
		t.Fatalf("expected a %s audit row, got error: %v", action, err)
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal audit metadata: %v", err)
	}
	return string(raw)
}

// TestDeliverDatabaseDSNAsync_StopsOnAPermanentlyBrokenKey pins the second half
// of the 2026-08-19 outage. The GITOPS_ENCRYPTION_KEY Secret carried a trailing
// newline, so every encrypt failed with "encoding/hex: invalid byte: U+000A" --
// a verdict that cannot change on a retry. The loop retried it anyway, every ten
// seconds for the full thirty-minute window, and wrote 172 identical failure
// rows for one user's single database.
func TestDeliverDatabaseDSNAsync_StopsOnAPermanentlyBrokenKey(t *testing.T) {
	pool := testOptimisticPool(t)
	creds := cloudtask.DBCredentials{Endpoint: "pg-router.databases.svc.cluster.local", Port: "5432", Username: "dada", Password: "s3cr3t"}
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey + "\n\n" + installTestKey}, dbcreds: fakeDBCredsResolver{creds: creds}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)
	seedApp(t, pool, projectID, envID, "megafactory")
	seedServiceDatabaseSnapshot(t, h, projectID, envID, "pg", "megafactory", "ns-x", "megafactory")

	returned := make(chan struct{})
	go func() {
		h.deliverDatabaseDSNAsync(context.Background(), projectID, envID, "pg", "megafactory", userID)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(dbDSNDeliveryPollInterval + 5*time.Second):
		t.Fatalf("deliverDatabaseDSNAsync kept retrying a key the process can never decode")
	}

	var attempts int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE project_id = $1 AND action = $2 AND outcome = 'failure'`,
		projectID, auditActionSeedDatabaseDSN,
	).Scan(&attempts); err != nil {
		t.Fatalf("count failure audit rows: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("failure audit rows = %d, want exactly 1 for a permanent config error", attempts)
	}
}
