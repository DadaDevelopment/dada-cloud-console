package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeEnabledPodTarExporter struct{}

func (fakeEnabledPodTarExporter) Enabled() bool { return true }

func (fakeEnabledPodTarExporter) FindRunningPod(context.Context, string, string) (string, string, error) {
	return "test-pod", "app", nil
}

func (fakeEnabledPodTarExporter) StreamTarball(context.Context, string, string, string, string, io.Writer) error {
	return nil
}

type fakeEnabledDBBackupPresigner struct{}

func (fakeEnabledDBBackupPresigner) Enabled() bool { return true }

func (fakeEnabledDBBackupPresigner) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	return "https://example.com/presigned", nil
}

func (fakeEnabledDBBackupPresigner) PutObject(context.Context, string, io.Reader, int64, string) error {
	return nil
}

func (fakeEnabledDBBackupPresigner) DeleteOldObjects(context.Context, string, time.Duration) (int, error) {
	return 0, nil
}

func testVolumeExportPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping volume-export DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedVolumeExportApp(t *testing.T, pool *pgxpool.Pool, volumeJSON string) (projectID, envID uuid.UUID, appName string) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	appName = "app-" + suffix

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"volume-export-test-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID)
	})

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	summary := "{}"
	if volumeJSON != "" {
		summary = volumeJSON
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, summary_json) VALUES ($1, $2, 'App', $3, $4)`,
		projectID, envID, appName, summary,
	); err != nil {
		t.Fatalf("seed resource_snapshot: %v", err)
	}
	return projectID, envID, appName
}

func newVolumeExportCtx(projectID, envID uuid.UUID, appName string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	path := "/api/v1/projects/" + projectID.String() + "/environments/" + envID.String() + "/apps/" + appName + "/volume/export"
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
	}
	auth.SetClaims(c, &auth.Claims{UserID: uuid.New(), Groups: []string{"/platform-admins"}})
	return c, rec
}

func TestExportAppVolume_NoVolume_Conflict(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{}`)

	h := &Handler{
		pool:              pool,
		podTarExporter:    fakeEnabledPodTarExporter{},
		dbBackupPresigner: fakeEnabledDBBackupPresigner{},
	}
	c, rec := newVolumeExportCtx(projectID, envID, appName)
	h.ExportAppVolume(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s want 409", rec.Code, rec.Body.String())
	}
}

func TestExportAppVolume_NotConfigured_ServiceUnavailable(t *testing.T) {
	pool := testVolumeExportPool(t)
	projectID, envID, appName := seedVolumeExportApp(t, pool, `{"volume":{"path":"/data","size":"1Gi"}}`)

	h := &Handler{
		pool:              pool,
		podTarExporter:    cloudtask.NewPodTarExporter(),
		dbBackupPresigner: cloudtask.NewDBBackupPresigner("", "", "", "", "", false),
	}
	c, rec := newVolumeExportCtx(projectID, envID, appName)
	h.ExportAppVolume(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s want 503", rec.Code, rec.Body.String())
	}
}
