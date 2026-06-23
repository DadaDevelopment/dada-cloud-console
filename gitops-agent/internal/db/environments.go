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
