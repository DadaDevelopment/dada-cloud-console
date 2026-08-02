package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestHandoffDeploy_AuditRowCarriesEnvironment pins the environment on the audit
// row of the path every git deploy takes. Migration 068 gave audit_events an
// environment_id and f8b84f8 filled it in on the backend's rollback/promote and
// deploy-hook handlers, but this insert -- the one that fires for push and
// manual builds alike -- kept writing NULL, so on prod every DeployImageVersion
// row that came from a build had no environment while the operation row three
// lines above it did.
func TestHandoffDeploy_AuditRowCarriesEnvironment(t *testing.T) {
	pool := testPool(t)
	projectID, envID := seedProjectEnv(t, pool, "small")
	owner := seedUser(t, pool)

	appName := "audit-env-" + uuid.NewString()[:8]
	gitRepoID := seedGitRepo(t, pool, projectID, envID, appName, "small")
	exec(t, pool, `UPDATE git_repos SET created_by = $1 WHERE id = $2`, owner, gitRepoID)

	repo := &Repo{ProjectID: projectID, EnvironmentID: envID, AppName: appName, Port: 8080, Replicas: 1, Profile: "small", CreatedBy: &owner}
	b := seedBuild(t, pool, gitRepoID, envID, appName, "envaud1")

	opID, err := HandoffDeploy(context.Background(), pool, b, repo, "registry/new", DeployDetection{}, DefaultDomainOpts{})
	if err != nil {
		t.Fatalf("HandoffDeploy: %v", err)
	}

	var auditEnv *uuid.UUID
	var opEnv *uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT a.environment_id, o.environment_id
		   FROM operations o JOIN audit_events a ON a.operation_id = o.id
		  WHERE o.id = $1`, opID,
	).Scan(&auditEnv, &opEnv); err != nil {
		t.Fatalf("read operation + audit row: %v", err)
	}
	if opEnv == nil || *opEnv != envID {
		t.Fatalf("operation environment_id = %v, want %s", opEnv, envID)
	}
	if auditEnv == nil {
		t.Fatalf("audit row has no environment_id while the operation it points at does — a deploy into a preview is then indistinguishable from a deploy into prod")
	}
	if *auditEnv != envID {
		t.Fatalf("audit environment_id = %s, want %s", *auditEnv, envID)
	}
}
