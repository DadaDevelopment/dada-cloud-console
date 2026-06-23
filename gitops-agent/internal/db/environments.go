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
}

// ListK8sEnvironments returns every k8s-runtime environment with a namespace.
// VM-runtime envs are excluded — their app state is owned by the portainer-agent.
func ListK8sEnvironments(ctx context.Context, pool *pgxpool.Pool) ([]K8sEnvironment, error) {
	rows, err := pool.Query(ctx, `
		SELECT project_id, id, namespace
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
		if err := rows.Scan(&e.ProjectID, &e.EnvID, &e.Namespace); err != nil {
			return nil, fmt.Errorf("scan environment: %w", err)
		}
		envs = append(envs, e)
	}
	return envs, rows.Err()
}

// AppSnapshotEnvs maps an App snapshot name to the environment IDs that have a
// snapshot with that name. Used by the status reconciler to resolve a Deployment
// living in a namespace-override namespace (App spec.namespace differs from the
// env namespace, e.g. dada-agent in argocd-prod) back to its owning environment
// — but only when the name is unambiguous (exactly one env).
func AppSnapshotEnvs(ctx context.Context, pool *pgxpool.Pool) (map[string][]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		SELECT name, environment_id
		FROM resource_snapshots
		WHERE kind = 'App' AND environment_id IS NOT NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("list app snapshot envs: %w", err)
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
