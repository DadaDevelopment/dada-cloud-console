package worker

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPruneVanishedSnapshots_DropsWhatTheFileNoLongerCarries is the regression
// test for the ghosts the agents migration left behind: two agents moved from a
// hand-written kagent file into their own carrier, and the console went on
// showing the removed raw CRs next to the live claims, because the reverse sync
// only ever upserted. A row nothing in git or in the cluster answers for is
// worse than a missing one -- it is indistinguishable from a live resource.
//
// The same test pins the two rows that must survive the prune: a resource
// another app owns, and a snapshot the API wrote after the commit (the LWW rule
// of the upsert, applied to deletion).
func TestPruneVanishedSnapshots_DropsWhatTheFileNoLongerCarries(t *testing.T) {
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

	applyMigrations(t, ctx, pool)

	projectID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO projects (id, name, display_name) VALUES ($1, $2, 'Test')`,
		projectID, "proj-"+uuid.New().String()[:8])
	envID := uuid.New()
	exec(t, ctx, pool,
		`INSERT INTO environments (id, project_id, name, namespace, type)
		 VALUES ($1, $2, 'prod', $3, 'prod')`,
		envID, projectID, "ns-"+uuid.New().String()[:8])

	commitTime := time.Now()
	seed := func(kind, name, appName string, at time.Time) {
		summary, err := json.Marshal(map[string]any{"app_name": appName, "kind": kind, "name": name})
		if err != nil {
			t.Fatalf("marshal summary: %v", err)
		}
		exec(t, ctx, pool,
			`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
			 VALUES ($1, $2, $3, $4, 'Unknown', $5, $6)`,
			projectID, envID, kind, name, summary, at)
	}
	seed("Agent", "telemost-poc", "kagent", commitTime.Add(-time.Hour))
	seed("RemoteMCPServer", "telemost-task-tools", "kagent", commitTime.Add(-time.Hour))
	seed("Agent", "poc-echo", "other-app", commitTime.Add(-time.Hour))
	seed("ManagedAgent", "reels-poc", "kagent", commitTime.Add(time.Minute))

	w := &GitWatcher{pool: pool}
	manifests := []resourceManifest{{Kind: "RemoteMCPServer"}}
	manifests[0].Metadata.Name = "telemost-task-tools"

	pruned := w.pruneVanishedSnapshots(ctx, projectID, &envID, "kagent", manifests, git.Commit{When: commitTime})
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1 (only the Agent the file stopped carrying)", pruned)
	}

	rows, err := pool.Query(ctx,
		`SELECT kind || '/' || name FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 ORDER BY name`, projectID, envID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var left []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		left = append(left, s)
	}
	want := []string{"Agent/poc-echo", "ManagedAgent/reels-poc", "RemoteMCPServer/telemost-task-tools"}
	if len(left) != len(want) {
		t.Fatalf("rows left = %v, want %v", left, want)
	}
	for i := range want {
		if left[i] != want[i] {
			t.Fatalf("rows left = %v, want %v", left, want)
		}
	}
}
