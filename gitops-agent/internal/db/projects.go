package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// GetProjectByID returns a single project from the catalog, for callers that
// already know its id (e.g. the CreateProject operation handler).
func GetProjectByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (Project, error) {
	var p Project
	err := pool.QueryRow(ctx, `
		SELECT id, name, display_name, owner_type, default_environment, quotas, created_at, updated_at
		FROM projects
		WHERE id = $1
	`, id).Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.OwnerType, &p.DefaultEnvironment,
		&p.Quotas, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return Project{}, fmt.Errorf("get project %s: %w", id, err)
	}
	return p, nil
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
// ownerID is nullable: pass nil when the manifest names no owner or the named
// owner could not be resolved to a users.id. Returns whether the call inserted
// a brand-new row (true) or updated an existing one (false), via the
// `xmax = 0` Postgres idiom, so callers can decide whether a missing owner is
// worth a one-time warning versus repeating it on every poll.
func UpsertProject(ctx context.Context, pool *pgxpool.Pool,
	name, displayName, ownerType, defaultEnvironment string,
	quotas json.RawMessage, ownerID *uuid.UUID,
) (bool, error) {
	if quotas == nil {
		quotas = json.RawMessage(`{}`)
	}

	var ownerIDParam any
	if ownerID != nil {
		ownerIDParam = *ownerID
	}

	// Git-origin projects (no console creator) belong to the shared org "dada"
	// (decision: project created via git = dada org). On conflict we keep any
	// existing org_id so a console-created project's personal org is never
	// downgraded; COALESCE also heals legacy rows still NULL. owner_id follows
	// the same never-clobber rule: a manifest owner only fills a NULL slot, it
	// never overwrites a real owner already on the row (M5 lives on shared
	// prod data).
	var inserted bool
	err := pool.QueryRow(ctx, `
		INSERT INTO projects
			(name, display_name, owner_type, default_environment, quotas, org_id, owner_id)
		VALUES ($1, $2, $3, $4, $5, 'dada', $6)
		ON CONFLICT (name) DO UPDATE
		SET display_name         = EXCLUDED.display_name,
		    owner_type           = EXCLUDED.owner_type,
		    default_environment   = EXCLUDED.default_environment,
		    quotas                = EXCLUDED.quotas,
		    org_id                = COALESCE(projects.org_id, EXCLUDED.org_id),
		    owner_id              = COALESCE(projects.owner_id, EXCLUDED.owner_id),
		    updated_at            = NOW()
		RETURNING (xmax = 0)
	`, name, displayName, ownerType, defaultEnvironment, quotas, ownerIDParam).Scan(&inserted)
	if err != nil {
		return false, fmt.Errorf("upsert project: %w", err)
	}
	return inserted, nil
}

// ResolveUserIDByIdentity looks up a user's id from a manifest-supplied owner
// identity, matching either their email or their Keycloak subject (the same
// two identifiers project.yaml authors could plausibly know). Returns
// ok=false, no error when the identity is empty or matches no user — that is
// the normal case for a manifest that predates the owner field, and callers
// must treat it as "still unresolved" rather than a failure.
func ResolveUserIDByIdentity(ctx context.Context, pool *pgxpool.Pool, identity string) (uuid.UUID, bool, error) {
	if identity == "" {
		return uuid.UUID{}, false, nil
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		SELECT id FROM users WHERE email = $1 OR keycloak_sub = $1 LIMIT 1
	`, identity).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.UUID{}, false, nil
		}
		return uuid.UUID{}, false, fmt.Errorf("resolve user identity %s: %w", identity, err)
	}
	return id, true, nil
}

// AddPlatformAdminsToProject is a no-op since ADR-009 (native RBAC).
//
// Staff god-mode is no longer propagated as a per-project membership row. It is
// the hidden Keycloak group /platform-admins, decoded directly from the JWT
// groups[] claim (dada-cloud auth.Claims.IsPlatformAdmin → Owner everywhere).
// Writing a legacy 'platform-admin' member row here would (a) use a role outside
// the uniform Owner/Admin/Developer/ReadOnly vocabulary and (b) be ignored by the
// new group renderer. Kept as a no-op so callers need not change.
func AddPlatformAdminsToProject(ctx context.Context, pool *pgxpool.Pool, projectName string) error {
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
