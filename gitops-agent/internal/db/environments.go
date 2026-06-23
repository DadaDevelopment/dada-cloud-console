package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// K8sEnvironment is the minimal env shape the status reconciler needs: which
// namespace to read Deployments from and which project/env the snapshots belong
// to.
type K8sEnvironment struct {
	ProjectID uuid.UUID
	EnvID     uuid.UUID
	Namespace string
	Name      string
}

// ListK8sEnvironments returns every k8s-runtime environment with a namespace.
// VM-runtime envs are excluded — their app state is owned by the portainer-agent.
func ListK8sEnvironments(ctx context.Context, pool *pgxpool.Pool) ([]K8sEnvironment, error) {
	rows, err := pool.Query(ctx, `
		SELECT project_id, id, namespace, name
		FROM environments
		WHERE runtime = 'k8s' AND namespace <> ''
		ORDER BY namespace
	`)
	if err != nil {
		return nil, fmt.Errorf("list k8s environments: %w", err)
	}
	defer rows.Close()

	var envs []K8sEnvironment
	for rows.Next() {
		var e K8sEnvironment
		if err := rows.Scan(&e.ProjectID, &e.EnvID, &e.Namespace, &e.Name); err != nil {
			return nil, fmt.Errorf("scan environment: %w", err)
		}
		envs = append(envs, e)
	}
	return envs, rows.Err()
}

// EnvProject identifies an environment and its owning project.
type EnvProject struct {
	ProjectID uuid.UUID
	EnvID     uuid.UUID
}

// EnvProjects maps every environment id to its project id (all runtimes). Used to
// resolve a snapshot's existing environment back to its project during cluster
// discovery.
func EnvProjects(ctx context.Context, pool *pgxpool.Pool) (map[uuid.UUID]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `SELECT id, project_id FROM environments`)
	if err != nil {
		return nil, fmt.Errorf("env projects: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]uuid.UUID{}
	for rows.Next() {
		var env, proj uuid.UUID
		if err := rows.Scan(&env, &proj); err != nil {
			return nil, fmt.Errorf("scan env project: %w", err)
		}
		out[env] = proj
	}
	return out, rows.Err()
}

// PlatformTarget returns the project + an environment to attach cluster-only
// resources that can't be mapped to a normal project env (infra PublicApis,
// shared databases, etc). Prefers project "platform" and its prod env. ok=false
// when no such project/env exists.
func PlatformTarget(ctx context.Context, pool *pgxpool.Pool) (EnvProject, bool, error) {
	var ep EnvProject
	err := pool.QueryRow(ctx, `
		SELECT p.id, e.id
		FROM projects p JOIN environments e ON e.project_id = p.id
		WHERE p.name = 'platform'
		ORDER BY (e.name = 'prod') DESC, e.name
		LIMIT 1
	`).Scan(&ep.ProjectID, &ep.EnvID)
	if err != nil {
		return EnvProject{}, false, nil //nolint:nilerr // absence is not an error
	}
	return ep, true, nil
}

// AppSnapshotEnvs is SnapshotEnvsByKind for kind='App'.
func AppSnapshotEnvs(ctx context.Context, pool *pgxpool.Pool) (map[string][]uuid.UUID, error) {
	return SnapshotEnvsByKind(ctx, pool, "App")
}

// SnapshotEnvsByKind maps a snapshot name (of the given kind) to the environment
// IDs that have a snapshot with that name. Used by the status reconciler to
// resolve a workload living in a namespace that isn't its env namespace (e.g.
// AIModel InferenceServices in ml-prod, or App spec.namespace overrides) back to
// its owning environment — but only when the name is unambiguous (one env).
func SnapshotEnvsByKind(ctx context.Context, pool *pgxpool.Pool, kind string) (map[string][]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		SELECT name, environment_id
		FROM resource_snapshots
		WHERE kind = $1 AND environment_id IS NOT NULL
	`, kind)
	if err != nil {
		return nil, fmt.Errorf("list snapshot envs: %w", err)
	}
	defer rows.Close()

	out := map[string][]uuid.UUID{}
	for rows.Next() {
		var name string
		var envID uuid.UUID
		if err := rows.Scan(&name, &envID); err != nil {
			return nil, fmt.Errorf("scan app snapshot env: %w", err)
		}
		out[name] = append(out[name], envID)
	}
	return out, rows.Err()
}
