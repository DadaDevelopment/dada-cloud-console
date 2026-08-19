package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedCreateApp inserts one CreateApp operation, the row a failed create leaves
// behind as the app's only surviving spec.
func seedCreateApp(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	projectID, envID uuid.UUID, appName, payload, status string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO operations (id, actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload, created_at)
		VALUES ($1, $2, $3, $4, 'CreateApp', 'App', $5, $6, $7, $8)
	`, uuid.New(), db.SystemActorID, projectID, envID, appName, status, []byte(payload), time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("seed CreateApp: %v", err)
	}
}

// seedDeleteApp inserts a landed DeleteApp, the record that an app is gone on
// purpose.
func seedDeleteApp(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	projectID, envID uuid.UUID, appName string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO operations (id, actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload, created_at)
		VALUES ($1, $2, $3, $4, 'DeleteApp', 'App', $5, 'Committed', $6, $7)
	`, uuid.New(), db.SystemActorID, projectID, envID, appName,
		[]byte(`{"name":"`+appName+`"}`), time.Now().Add(-30*time.Minute)); err != nil {
		t.Fatalf("seed DeleteApp: %v", err)
	}
}

func recoveryPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration test")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	applyMigrations(t, ctx, pool)
	return pool
}

// TestAppCreateSummaryCarriesTheWholeSpec pins the summary shape the deploy path
// reads back, so a rebuilt snapshot is indistinguishable from the original.
func TestAppCreateSummaryCarriesTheWholeSpec(t *testing.T) {
	var p createAppPayload
	if err := json.Unmarshal([]byte(`{
		"name":"viteprobe","image":"nexus/viteprobe:1","framework":"vite","port":8080,
		"replicas":2,"profile":"small","workload_type":"web","worker":false,
		"volume":{"path":"/data","size":"1Gi","storage_class":"longhorn","fs_group":1000}
	}`), &p); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := appCreateSummary(p, "viteprobe-prod-abcd")

	if got["image"] != "nexus/viteprobe:1" || got["framework"] != "vite" ||
		got["port"] != 8080 || got["replicas"] != 2 || got["profile"] != "small" ||
		got["argo_name"] != "viteprobe-prod-abcd" || got["workload_type"] != "web" {
		t.Fatalf("summary lost part of the spec: %#v", got)
	}
	if _, ok := got["worker"]; ok {
		t.Fatalf("worker=false must stay absent, a stray true would drop the Service: %#v", got)
	}
	vol, ok := got["volume"].(map[string]any)
	if !ok || vol["path"] != "/data" || vol["size"] != "1Gi" ||
		vol["storage_class"] != "longhorn" || vol["fs_group"] != int64(1000) {
		t.Fatalf("volume lost: %#v", got["volume"])
	}
}

// TestLoadAppSummaryRebuildsFromCreateOperation is the regression for the
// 2026-08-19 wedge: a CreateApp that failed at the git push left no snapshot,
// and every later DeployImageVersion died on "loading app snapshot: no rows in
// result set" for the life of the app.
func TestLoadAppSummaryRebuildsFromCreateOperation(t *testing.T) {
	ctx := context.Background()
	pool := recoveryPool(t, ctx)
	defer pool.Close()

	projectID := uuid.New()
	envID := seedProjectEnv(t, ctx, pool, projectID)
	seedCreateApp(t, ctx, pool, projectID, envID, "e117viteprobe",
		`{"name":"e117viteprobe","image":"nexus/e117viteprobe:1","framework":"vite","port":3000,"replicas":1,"profile":"small"}`,
		"Failed")
	op := seedRelease(t, ctx, pool, projectID, envID, "e117viteprobe", "nexus/e117viteprobe:2", "Processing", time.Now())
	defer dropSeededOperations(ctx, pool, projectID)

	w := &DBWatcher{pool: pool}
	cur, err := w.loadAppSummary(ctx, op, "e117viteprobe", "prod")
	if err != nil {
		t.Fatalf("loadAppSummary: %v; one transient push failure must not brick the app", err)
	}
	if cur["framework"] != "vite" || cur["port"] != 3000 {
		t.Fatalf("rebuilt summary lost the create spec: %#v", cur)
	}
	if cur["argo_name"] == "" || cur["argo_name"] == nil {
		t.Fatalf("rebuilt summary has no argo_name: %#v", cur)
	}
}

// TestLoadAppSummaryPrefersTheStoredSnapshot keeps recovery off the normal path:
// a live snapshot always wins over replaying the create.
func TestLoadAppSummaryPrefersTheStoredSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := recoveryPool(t, ctx)
	defer pool.Close()

	projectID := uuid.New()
	envID := seedProjectEnv(t, ctx, pool, projectID)
	seedCreateApp(t, ctx, pool, projectID, envID, "backend",
		`{"name":"backend","image":"nexus/backend:1","framework":"go","port":8080}`, "Ready")
	op := seedRelease(t, ctx, pool, projectID, envID, "backend", "nexus/backend:2", "Processing", time.Now())
	defer dropSeededOperations(ctx, pool, projectID)

	if err := db.UpsertSnapshot(ctx, pool, projectID, &envID, "App", "backend", "Ready",
		[]byte(`{"image":"nexus/backend:9","framework":"go","port":9090,"argo_name":"live"}`), time.Now()); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	w := &DBWatcher{pool: pool}
	cur, err := w.loadAppSummary(ctx, op, "backend", "prod")
	if err != nil {
		t.Fatalf("loadAppSummary: %v", err)
	}
	if cur["port"] != float64(9090) || cur["argo_name"] != "live" {
		t.Fatalf("recovery overrode the live snapshot: %#v", cur)
	}
}

// TestLoadAppSummaryStillFailsForAnAppThatNeverExisted keeps the recovery from
// inventing apps: with no snapshot and no CreateApp there is nothing to deploy.
func TestLoadAppSummaryStillFailsForAnAppThatNeverExisted(t *testing.T) {
	ctx := context.Background()
	pool := recoveryPool(t, ctx)
	defer pool.Close()

	projectID := uuid.New()
	envID := seedProjectEnv(t, ctx, pool, projectID)
	op := seedRelease(t, ctx, pool, projectID, envID, "ghost", "nexus/ghost:1", "Processing", time.Now())
	defer dropSeededOperations(ctx, pool, projectID)

	w := &DBWatcher{pool: pool}
	if _, err := w.loadAppSummary(ctx, op, "ghost", "prod"); err == nil {
		t.Fatal("loadAppSummary invented an app with no CreateApp operation")
	}
}

// TestLoadAppSummaryRefusesToResurrectADeletedApp keeps recovery from undoing a
// delete: envprobe0816 was deleted on 2026-08-16 and a stale build pipeline was
// still releasing into it three days later.
func TestLoadAppSummaryRefusesToResurrectADeletedApp(t *testing.T) {
	ctx := context.Background()
	pool := recoveryPool(t, ctx)
	defer pool.Close()

	projectID := uuid.New()
	envID := seedProjectEnv(t, ctx, pool, projectID)
	seedCreateApp(t, ctx, pool, projectID, envID, "envprobe0816",
		`{"name":"envprobe0816","image":"nexus/envprobe:1","port":8080}`, "Committed")
	seedDeleteApp(t, ctx, pool, projectID, envID, "envprobe0816")
	op := seedRelease(t, ctx, pool, projectID, envID, "envprobe0816", "nexus/envprobe:2", "Processing", time.Now())
	defer dropSeededOperations(ctx, pool, projectID)

	w := &DBWatcher{pool: pool}
	if _, err := w.loadAppSummary(ctx, op, "envprobe0816", "prod"); !errors.Is(err, errAppDeleted) {
		t.Fatalf("loadAppSummary returned %v, want errAppDeleted: a deleted app must stay deleted", err)
	}
}

// TestLoadComposeAppSummaryRebuildsFromCreateOperation is the VM twin: a compose
// release must survive a missing snapshot the same way.
func TestLoadComposeAppSummaryRebuildsFromCreateOperation(t *testing.T) {
	ctx := context.Background()
	pool := recoveryPool(t, ctx)
	defer pool.Close()

	projectID := uuid.New()
	envID := seedProjectEnv(t, ctx, pool, projectID)
	seedCreateApp(t, ctx, pool, projectID, envID, "findata",
		`{"name":"findata","image":"nexus/findata:1","port":8080,"app_server_name":"vm-1"}`, "Failed")
	op := seedRelease(t, ctx, pool, projectID, envID, "findata", "nexus/findata:2", "Processing", time.Now())
	defer dropSeededOperations(ctx, pool, projectID)

	w := &DBWatcher{pool: pool}
	cur, err := w.loadComposeAppSummary(ctx, op, "findata")
	if err != nil {
		t.Fatalf("loadComposeAppSummary: %v", err)
	}
	if cur["runtime"] != "compose" {
		t.Fatalf("rebuilt compose summary is not compose: %#v", cur)
	}
	if _, ok := cur["desired"]; !ok {
		t.Fatalf("rebuilt compose summary has no desired spec: %#v", cur)
	}
}
