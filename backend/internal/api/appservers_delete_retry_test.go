package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedAppServer inserts a terraform-sourced app_servers row in the given status
// and returns its name.
func seedAppServer(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, status string) string {
	t.Helper()
	name := "vm-" + uuid.NewString()[:8]
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO app_servers (project_id, name, source, status, error_message)
		 VALUES ($1, $2, 'terraform', $3, 'Region not found')`,
		projectID, name, status,
	); err != nil {
		t.Fatalf("seed app_server: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app_servers WHERE project_id = $1 AND name = $2`, projectID, name)
		dropSeededAudit(pool, "AppServer", name)
	})
	return name
}

func seedDeleteOperation(t *testing.T, pool *pgxpool.Pool, projectID, actorID uuid.UUID, serverName, status string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, 'DeleteAppServer', 'AppServer', $3, $4, '{}'::jsonb)`,
		actorID, projectID, serverName, status,
	); err != nil {
		t.Fatalf("seed operation: %v", err)
	}
}

func appServerParams(projectID uuid.UUID, serverName string) gin.Params {
	return gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "serverName", Value: serverName},
	}
}

// TestDeleteAppServer_RetriesAfterFailedDeletion pins the escape from the wedge
// a user hit head-on: a VM whose provisioning failed was deleted, the worker's
// teardown failed, and the row stayed in Deleting forever. The handler rejected
// every further delete purely because of that status, so the console showed an
// undeletable server with a stale provisioning error under it.
func TestDeleteAppServer_RetriesAfterFailedDeletion(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, _ := seedOptimisticFixture(t, pool)
	actorID := seedUser(t, pool)
	claims := godClaims(actorID)

	serverName := seedAppServer(t, pool, projectID, "Deleting")
	seedDeleteOperation(t, pool, projectID, actorID, serverName, "Failed")

	c, rec := newCreateCtx(t, "", appServerParams(projectID, serverName), claims)
	h.DeleteAppServer(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry after a failed deletion: status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeleteAppServer_RejectsWhileDeletionRuns keeps the retry from becoming a
// second concurrent teardown: what blocks a duplicate is a deletion still
// running, not the row's status.
func TestDeleteAppServer_RejectsWhileDeletionRuns(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, _ := seedOptimisticFixture(t, pool)
	actorID := seedUser(t, pool)
	claims := godClaims(actorID)

	serverName := seedAppServer(t, pool, projectID, "Deleting")
	seedDeleteOperation(t, pool, projectID, actorID, serverName, "Processing")

	c, rec := newCreateCtx(t, "", appServerParams(projectID, serverName), claims)
	h.DeleteAppServer(c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete while one runs: status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeleteAppServer_AlreadyDeletedStays404 keeps the loosened status filter
// from resurrecting rows the platform already tore down.
func TestDeleteAppServer_AlreadyDeletedStays404(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, _ := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))

	serverName := seedAppServer(t, pool, projectID, "Deleted")

	c, rec := newCreateCtx(t, "", appServerParams(projectID, serverName), claims)
	h.DeleteAppServer(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete of a Deleted server: status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
