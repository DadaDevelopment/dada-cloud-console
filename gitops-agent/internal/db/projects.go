package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Project holds the project catalog fields used by gitops-agent.
type Project struct {
	ID                 uuid.UUID
	Name               string
	DisplayName        string
	OwnerType          string
	DefaultEnvironment string
	Quotas             json.RawMessage
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ListProjects returns all projects from the catalog.
func ListProjects(ctx context.Context, pool *pgxpool.Pool) ([]Project, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, display_name, owner_type, default_environment, quotas, created_at, updated_at
		FROM projects
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	var result []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(
			&p.ID, &p.Name, &p.DisplayName, &p.OwnerType, &p.DefaultEnvironment,
			&p.Quotas, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// ProjectMember links a username to a role within a project.
type ProjectMember struct {
	Username string
	Role     string
}

// ListProjectMembers returns all members of a project with their roles.
func ListProjectMembers(ctx context.Context, pool *pgxpool.Pool, projectName string) ([]ProjectMember, error) {
	rows, err := pool.Query(ctx, `
		SELECT u.username, pm.role
		FROM project_members pm
		JOIN users u ON u.id = pm.user_id
		JOIN projects p ON p.id = pm.project_id
		WHERE p.name = $1
		ORDER BY u.username
	`, projectName)
	if err != nil {
		return nil, fmt.Errorf("query project members for %s: %w", projectName, err)
	}
	defer rows.Close()

	var result []ProjectMember
	for rows.Next() {
		var m ProjectMember
		if err := rows.Scan(&m.Username, &m.Role); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// UpsertProject stores a project catalog row, preferring values from git.
func UpsertProject(ctx context.Context, pool *pgxpool.Pool,
	name, displayName, ownerType, defaultEnvironment string,
	quotas json.RawMessage,
) error {
	if quotas == nil {
		quotas = json.RawMessage(`{}`)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO projects
			(name, display_name, owner_type, default_environment, quotas)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (name) DO UPDATE
		SET display_name         = EXCLUDED.display_name,
		    owner_type           = EXCLUDED.owner_type,
		    default_environment   = EXCLUDED.default_environment,
		    quotas                = EXCLUDED.quotas,
		    updated_at            = NOW()
	`, name, displayName, ownerType, defaultEnvironment, quotas)
	if err != nil {
		return fmt.Errorf("upsert project: %w", err)
	}
	return nil
}

// AddPlatformAdminsToProject grants platform-admin membership to every user
// who already holds the platform-admin role in at least one other project.
func AddPlatformAdminsToProject(ctx context.Context, pool *pgxpool.Pool, projectName string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		SELECT p.id, admins.user_id, 'platform-admin'
		FROM projects p
		JOIN (
			SELECT DISTINCT user_id
			FROM project_members
			WHERE role = 'platform-admin'
		) admins ON true
		WHERE p.name = $1
		ON CONFLICT (project_id, user_id) DO NOTHING
	`, projectName)
	if err != nil {
		return fmt.Errorf("add platform admins to project %s: %w", projectName, err)
	}
	return nil
}

// UpsertEnvironmentPolicy updates limit_range and resource_quota for the
// environment identified by its k8s namespace. No-ops if namespace is unknown.
func UpsertEnvironmentPolicy(ctx context.Context, pool *pgxpool.Pool, namespace string, limitRange, resourceQuota json.RawMessage) error {
	if limitRange == nil {
		limitRange = json.RawMessage(`{}`)
	}
	if resourceQuota == nil {
		resourceQuota = json.RawMessage(`{}`)
	}
	_, err := pool.Exec(ctx, `
		UPDATE environments
		SET limit_range    = $2,
		    resource_quota = $3,
		    updated_at     = NOW()
		WHERE namespace = $1
	`, namespace, limitRange, resourceQuota)
	if err != nil {
		return fmt.Errorf("upsert environment policy for namespace %s: %w", namespace, err)
	}
	return nil
}

// UpsertEnvironment creates or updates an environment for the given project.
func UpsertEnvironment(ctx context.Context, pool *pgxpool.Pool, projectName, envName, namespace, envType string) error {
	if envType == "" {
		envType = "prod"
	}
	if namespace == "" {
		namespace = projectName + "-" + envName
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO environments (project_id, name, namespace, type)
		SELECT p.id, $2, $3, $4
		FROM projects p WHERE p.name = $1
		ON CONFLICT (project_id, name) DO UPDATE
		SET namespace  = EXCLUDED.namespace,
		    type       = EXCLUDED.type,
		    updated_at = NOW()
	`, projectName, envName, namespace, envType)
	if err != nil {
		return fmt.Errorf("upsert environment %s/%s: %w", projectName, envName, err)
	}
	return nil
}
