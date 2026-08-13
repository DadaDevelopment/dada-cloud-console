package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

// TestViewAppsAuditMetadataBreakdown is a DB-free unit test of the pure
// metadata builder. It exists so the screen-state breakdown (empty vs
// populated, healthy vs unhealthy, source mix) has coverage even when
// TEST_DATABASE_URL is unset, since viewAppsAuditMetadata takes no database
// argument at all -- everything it needs, ListApps already computed.
func TestViewAppsAuditMetadataBreakdown(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		got := viewAppsAuditMetadata(nil, 0)
		want := map[string]any{
			"apps": 0, "git_repos": 0, "empty": true,
			"healthy": 0, "unhealthy": 0, "sources": map[string]int{},
		}
		assertViewAppsMetadata(t, got, want)
	})

	t.Run("mixed health and source", func(t *testing.T) {
		apps := []models.ResourceSnapshot{
			summaryApp("Ready", `{"source":"git"}`, nil),
			summaryApp("Ready", `{"source":"archive"}`, nil),
			summaryApp("Failed", `{"source":"image"}`, nil),
			summaryApp("Ready", `{"source":"git"}`, []models.AppAlert{{Type: "crash", DetectedAt: time.Now()}}),
			summaryApp("Building", ``, nil),
		}
		got := viewAppsAuditMetadata(apps, 3)
		want := map[string]any{
			"apps": 5, "git_repos": 3, "empty": false,
			"healthy": 2, "unhealthy": 3,
			"sources": map[string]int{"git": 2, "archive": 1, "image": 1, "unknown": 1},
		}
		assertViewAppsMetadata(t, got, want)
	})
}

func summaryApp(phase, summaryJSON string, alerts []models.AppAlert) models.ResourceSnapshot {
	var raw json.RawMessage
	if summaryJSON != "" {
		raw = json.RawMessage(summaryJSON)
	}
	return models.ResourceSnapshot{
		ID: uuid.New(), Phase: phase, SummaryJSON: raw, Alerts: alerts,
	}
}

func assertViewAppsMetadata(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("viewAppsAuditMetadata = %s, want %s", gotJSON, wantJSON)
	}
}

// TestViewAppsAuditRowCarriesScreenState proves the metadata actually
// round-trips through the real write path (writeAuditRow -> audit_events)
// rather than only through the in-process map. It writes via h.recordAudit
// synchronously -- the same call recordViewAudit makes from inside a
// goroutine -- so the test is not racing recordViewAudit's own dedup window
// or its background goroutine, while still exercising the identical SQL and
// JSON marshaling ListApps's audit row goes through.
func TestViewAppsAuditRowCarriesScreenState(t *testing.T) {
	pool := testAuditPool(t)
	ctx := context.Background()
	actorID, projectID := seedAuditActor(t, pool)

	h := &Handler{pool: pool}
	envID := uuid.New()
	action := "ViewAppsMetaTest" + uuid.NewString()[:8]

	apps := []models.ResourceSnapshot{
		summaryApp("Ready", `{"source":"git"}`, nil),
		summaryApp("NotDeployed", `{"source":"archive"}`, nil),
	}
	h.recordAudit(ctx, actorID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        action,
		ResourceKind:  "AppList",
		ResourceName:  envID.String(),
		Metadata:      viewAppsAuditMetadata(apps, 1),
	})
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE action = $1`, action)
	})

	var metaJSON []byte
	if err := pool.QueryRow(ctx,
		`SELECT metadata FROM audit_events WHERE action = $1 AND actor_id = $2`,
		action, actorID,
	).Scan(&metaJSON); err != nil {
		t.Fatalf("read written audit row: %v", err)
	}

	var meta map[string]any
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		t.Fatalf("unmarshal stored metadata: %v", err)
	}

	if meta["apps"] != float64(2) {
		t.Errorf("apps = %v, want 2", meta["apps"])
	}
	if meta["git_repos"] != float64(1) {
		t.Errorf("git_repos = %v, want 1", meta["git_repos"])
	}
	if meta["empty"] != false {
		t.Errorf("empty = %v, want false", meta["empty"])
	}
	if meta["healthy"] != float64(1) {
		t.Errorf("healthy = %v, want 1", meta["healthy"])
	}
	if meta["unhealthy"] != float64(1) {
		t.Errorf("unhealthy = %v, want 1", meta["unhealthy"])
	}
	sources, ok := meta["sources"].(map[string]any)
	if !ok {
		t.Fatalf("sources = %v (%T), want object", meta["sources"], meta["sources"])
	}
	if sources["git"] != float64(1) || sources["archive"] != float64(1) {
		t.Errorf("sources = %v, want git:1 archive:1", sources)
	}
}
