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

// CreateAppServer inserts a new terraform-provisioned app_server row in
// Provisioning status and returns its UUID.
func CreateAppServer(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, name, workspace string) (uuid.UUID, error) {
	return createAppServer(ctx, pool, projectID, name, workspace, "terraform")
}

// CreateManualAppServer inserts a new manually-connected app_server row (no
// terraform workspace) in Provisioning status and returns its UUID.
func CreateManualAppServer(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, name string) (uuid.UUID, error) {
	return createAppServer(ctx, pool, projectID, name, "", "manual")
}

func createAppServer(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, name, workspace, source string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO app_servers (project_id, name, terraform_workspace, source, status)
		 VALUES ($1, $2, $3, $4, 'Provisioning')
		 ON CONFLICT (project_id, name) DO UPDATE
		   SET terraform_workspace = EXCLUDED.terraform_workspace,
		       source = EXCLUDED.source,
		       status = 'Provisioning',
		       updated_at = NOW()
		 RETURNING id`,
		projectID, name, workspace, source,
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

// GetProjectIDByName resolves a project UUID by its slug/name (e.g. "internal").
func GetProjectIDByName(ctx context.Context, pool *pgxpool.Pool, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM projects WHERE name = $1`, name).Scan(&id)
	return id, err
}

// ListKnownVMProviderIDs returns the set of Beget provider ids already tracked by
// any non-Deleted app_server. The reverse-sync reader uses it to skip VMs the
// console already owns (created or previously adopted) — the hard dedup rule.
func ListKnownVMProviderIDs(ctx context.Context, pool *pgxpool.Pool) (map[string]struct{}, error) {
	rows, err := pool.Query(ctx,
		`SELECT vm_provider_id FROM app_servers
		 WHERE vm_provider_id IS NOT NULL AND vm_provider_id <> '' AND status <> 'Deleted'`)
	if err != nil {
		return nil, fmt.Errorf("query provider ids: %w", err)
	}
	defer rows.Close()
	set := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = struct{}{}
	}
	return set, rows.Err()
}

// ListActiveAppServerNames returns names of non-Deleted app_servers. Used as a
// race guard: an in-flight console create may not have its provider id recorded
// yet, but its name already exists, so the reader skips a same-named Beget VM.
func ListActiveAppServerNames(ctx context.Context, pool *pgxpool.Pool) (map[string]struct{}, error) {
	rows, err := pool.Query(ctx,
		`SELECT name FROM app_servers WHERE status <> 'Deleted'`)
	if err != nil {
		return nil, fmt.Errorf("query names: %w", err)
	}
	defer rows.Close()
	set := make(map[string]struct{})
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		set[n] = struct{}{}
	}
	return set, rows.Err()
}

// CreateImportedAppServer inserts (or revives) a beget-import app_server row for
// an adopted VM and returns its UUID. Status starts at Imported.
func CreateImportedAppServer(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, name, vmProviderID, vmIP string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO app_servers (project_id, name, vm_ip, vm_provider_id, source, status)
		 VALUES ($1, $2, $3, $4, 'beget-import', 'Imported')
		 ON CONFLICT (project_id, name) DO UPDATE
		   SET vm_ip = EXCLUDED.vm_ip,
		       vm_provider_id = EXCLUDED.vm_provider_id,
		       source = 'beget-import',
		       status = 'Imported',
		       updated_at = NOW()
		 RETURNING id`,
		projectID, name, vmIP, vmProviderID,
	).Scan(&id)
	return id, err
}

// SetAppServerImported finalises an adopted VM after terraform import: records
// vm_ip / vm_provider_id and sets status=Imported.
func SetAppServerImported(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, vmIP, vmProviderID string) error {
	_, err := pool.Exec(ctx,
		`UPDATE app_servers SET vm_ip=$2, vm_provider_id=$3, status='Imported', updated_at=NOW() WHERE id=$1`,
		id, vmIP, vmProviderID,
	)
	return err
}

// ComposeDeployTarget identifies where a compose stack must be deployed: the
// project/env slugs (to build the git path) and the Portainer endpoint id.
type ComposeDeployTarget struct {
	ProjectSlug string
	EnvSlug     string
	EndpointID  int
}

// GetComposeDeployTarget resolves the deploy target for a compose app from the
// operation's project + environment. It requires the environment's AppServer to
// be Ready with a registered Portainer endpoint.
func GetComposeDeployTarget(ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, environmentID *uuid.UUID) (*ComposeDeployTarget, error) {
	if environmentID == nil {
		return nil, fmt.Errorf("compose deploy requires an environment")
	}
	var (
		t        ComposeDeployTarget
		status   string
		endpoint *int
	)
	err := pool.QueryRow(ctx, `
		SELECT p.name, e.name, s.status, s.portainer_endpoint_id
		FROM projects p
		JOIN environments e ON e.project_id = p.id
		JOIN app_servers s ON s.id = e.app_server_id
		WHERE p.id = $1 AND e.id = $2
	`, projectID, *environmentID).Scan(&t.ProjectSlug, &t.EnvSlug, &status, &endpoint)
	if err != nil {
		return nil, err
	}
	if status != "Ready" {
		return nil, fmt.Errorf("app server not Ready (status=%s)", status)
	}
	if endpoint == nil {
		return nil, fmt.Errorf("app server has no portainer endpoint")
	}
	t.EndpointID = *endpoint
	return &t, nil
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
