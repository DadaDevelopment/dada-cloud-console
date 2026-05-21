package db

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AppServerRow is the minimal DB representation of an app_server row used by portainer-agent.
type AppServerRow struct {
	ID                  uuid.UUID
	ProjectID           uuid.UUID
	Name                string
	VMIP                *string
	VMProviderID        *string
	TerraformWorkspace  *string
	PortainerEndpointID *int
	Status              string
	ErrorMessage        *string
}

// GetAppServerByName fetches an app_server by project + name.
func GetAppServerByName(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, name string) (*AppServerRow, error) {
	var s AppServerRow
	err := pool.QueryRow(ctx,
		`SELECT id, project_id, name, vm_ip, vm_provider_id, terraform_workspace,
		        portainer_endpoint_id, status, error_message
		 FROM app_servers WHERE project_id = $1 AND name = $2`,
		projectID, name,
	).Scan(&s.ID, &s.ProjectID, &s.Name, &s.VMIP, &s.VMProviderID,
		&s.TerraformWorkspace, &s.PortainerEndpointID, &s.Status, &s.ErrorMessage)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateAppServer inserts a new app_server row in Provisioning status and returns its UUID.
func CreateAppServer(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, name, workspace string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO app_servers (project_id, name, terraform_workspace, status)
		 VALUES ($1, $2, $3, 'Provisioning')
		 ON CONFLICT (project_id, name) DO UPDATE
		   SET terraform_workspace = EXCLUDED.terraform_workspace,
		       status = 'Provisioning',
		       updated_at = NOW()
		 RETURNING id`,
		projectID, name, workspace,
	).Scan(&id)
	return id, err
}

// SetAppServerWorkspace persists the terraform workspace path after it has been created.
func SetAppServerWorkspace(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, workspace string) error {
	_, err := pool.Exec(ctx,
		`UPDATE app_servers SET terraform_workspace=$2, updated_at=NOW() WHERE id=$1`,
		id, workspace,
	)
	return err
}

// SetAppServerProvisioned updates vm_ip and vm_provider_id after terraform apply.
func SetAppServerProvisioned(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, vmIP, vmProviderID string) error {
	_, err := pool.Exec(ctx,
		`UPDATE app_servers SET vm_ip=$2, vm_provider_id=$3, status='WaitingForAgent', updated_at=NOW() WHERE id=$1`,
		id, vmIP, vmProviderID,
	)
	return err
}

// SetAppServerReady sets status=Ready and records the portainer endpoint ID.
func SetAppServerReady(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, portainerEndpointID int) error {
	_, err := pool.Exec(ctx,
		`UPDATE app_servers SET portainer_endpoint_id=$2, status='Ready', updated_at=NOW() WHERE id=$1`,
		id, portainerEndpointID,
	)
	return err
}

// SetAppServerFailed sets status=Failed with an error message.
func SetAppServerFailed(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, errMsg string) error {
	_, err := pool.Exec(ctx,
		`UPDATE app_servers SET status='Failed', error_message=$2, updated_at=NOW() WHERE id=$1`,
		id, errMsg,
	)
	return err
}

// SetAppServerDeleting sets status=Deleting.
func SetAppServerDeleting(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	_, err := pool.Exec(ctx,
		`UPDATE app_servers SET status='Deleting', updated_at=NOW() WHERE id=$1`,
		id,
	)
	return err
}

// SetAppServerDeleted sets status=Deleted.
func SetAppServerDeleted(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	_, err := pool.Exec(ctx,
		`UPDATE app_servers SET status='Deleted', updated_at=$2 WHERE id=$1`,
		id, time.Now(),
	)
	return err
}

// ListReadyAppServers returns all app_servers with status=Ready.
func ListReadyAppServers(ctx context.Context, pool *pgxpool.Pool) ([]AppServerRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, project_id, name, vm_ip, vm_provider_id, terraform_workspace,
		        portainer_endpoint_id, status, error_message
		 FROM app_servers WHERE status = 'Ready'`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var result []AppServerRow
	for rows.Next() {
		var s AppServerRow
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Name, &s.VMIP, &s.VMProviderID,
			&s.TerraformWorkspace, &s.PortainerEndpointID, &s.Status, &s.ErrorMessage); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}
