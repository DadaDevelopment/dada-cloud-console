package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestHandoffActor covers all three attribution branches. The middle one is the
// regression this test exists for: a push-triggered redeploy of an app that
// already exists used to land on the system actor, which cohort analysis drops
// as synthetic -- so the users who deploy most (continuously, from git) were
// the ones missing from the funnel.
func TestHandoffActor(t *testing.T) {
	user := uuid.New()
	owner := uuid.New()

	cases := []struct {
		name          string
		build         *Build
		repo          *Repo
		wantActor     uuid.UUID
		wantInitiator string
	}{
		{
			name:          "manual build is attributed to the person who clicked",
			build:         &Build{TriggeredBy: &user},
			repo:          &Repo{CreatedBy: &owner},
			wantActor:     user,
			wantInitiator: initiatorManual,
		},
		{
			name:          "push redeploy is attributed to whoever connected the repo",
			build:         &Build{},
			repo:          &Repo{CreatedBy: &owner},
			wantActor:     owner,
			wantInitiator: initiatorPush,
		},
		{
			name:          "repo with no owner stays on the system actor",
			build:         &Build{},
			repo:          &Repo{},
			wantActor:     SystemUserID,
			wantInitiator: initiatorSystem,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actor, initiator := handoffActor(tc.build, tc.repo)
			if actor != tc.wantActor {
				t.Fatalf("actor = %s, want %s", actor, tc.wantActor)
			}
			if initiator != tc.wantInitiator {
				t.Fatalf("initiator = %q, want %q", initiator, tc.wantInitiator)
			}
		})
	}
}

// TestHandoffDeploy_PushRedeployKeepsTheOwner is the end-to-end half: a push
// build of an app that already exists must leave an audit row owned by the
// human who connected the repo, tagged initiator=push so manual and automatic
// deploys stay separable without discarding the owner.
func TestHandoffDeploy_PushRedeployKeepsTheOwner(t *testing.T) {
	pool := testPool(t)
	projectID, envID := seedProjectEnv(t, pool, "small")
	owner := seedUser(t, pool)

	appName := "push-redeploy"
	exec(t, pool,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3::text, 'Ready', jsonb_build_object('name', $3::text, 'kind', 'App', 'image', 'registry/old'), NOW())`,
		projectID, envID, appName)

	gitRepoID := seedGitRepo(t, pool, projectID, envID, appName, "small")
	exec(t, pool, `UPDATE git_repos SET created_by = $1 WHERE id = $2`, owner, gitRepoID)

	repo := &Repo{ProjectID: projectID, EnvironmentID: envID, AppName: appName, Port: 8080, Replicas: 1, Profile: "small", CreatedBy: &owner}
	b := seedBuild(t, pool, gitRepoID, envID, appName, "push01")

	opID, err := HandoffDeploy(context.Background(), pool, b, repo, "registry/new", DeployDetection{}, DefaultDomainOpts{})
	if err != nil {
		t.Fatalf("HandoffDeploy: %v", err)
	}

	action, _ := readOperation(t, pool, opID)
	if action != "DeployImageVersion" {
		t.Fatalf("action = %q, want DeployImageVersion", action)
	}

	var opActor, auditActor uuid.UUID
	var initiator string
	if err := pool.QueryRow(context.Background(),
		`SELECT o.actor_id, a.actor_id, a.metadata->>'initiator'
		   FROM operations o JOIN audit_events a ON a.operation_id = o.id
		  WHERE o.id = $1`, opID,
	).Scan(&opActor, &auditActor, &initiator); err != nil {
		t.Fatalf("read operation + audit row: %v", err)
	}
	if auditActor != owner {
		t.Fatalf("audit actor = %s, want repo owner %s (a push redeploy on the system actor is invisible to cohort analysis)", auditActor, owner)
	}
	if opActor != owner {
		t.Fatalf("operation actor = %s, want repo owner %s", opActor, owner)
	}
	if initiator != initiatorPush {
		t.Fatalf("metadata initiator = %q, want %q", initiator, initiatorPush)
	}
}

func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	short := id.String()[:8]
	exec(t, pool,
		`INSERT INTO users (id, username, email, password_hash, display_name)
		 VALUES ($1, $2, $3, 'x', 'Test Owner')`,
		id, "u-"+short, "u-"+short+"@example.com")
	return id
}
