package api

import (
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// projectWithRole extends Project with the requesting user's role.
type projectWithRole struct {
	models.Project
	Role models.MemberRole `json:"role"`
}

// ListProjects returns all projects the authenticated user has access to.
func (h *Handler) ListProjects(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT p.id, p.name, p.display_name, p.owner_type, p.owner_id,
		        p.default_environment, p.quotas, p.created_at, p.updated_at,
		        pm.role
		 FROM projects p
		 JOIN project_members pm ON pm.project_id = p.id
		 WHERE pm.user_id = $1
		 ORDER BY p.name`,
		claims.UserID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query projects")
		return
	}
	defer rows.Close()

	var projects []projectWithRole
	for rows.Next() {
		var p projectWithRole
		if err := rows.Scan(
			&p.ID, &p.Name, &p.DisplayName, &p.OwnerType, &p.OwnerID,
			&p.DefaultEnvironment, &p.Quotas, &p.CreatedAt, &p.UpdatedAt,
			&p.Role,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan project")
			return
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading projects")
		return
	}

	if projects == nil {
		projects = []projectWithRole{}
	}

	c.JSON(http.StatusOK, gin.H{"projects": projects})
}

// GetProject returns a single project by ID, including environments and user role.
func (h *Handler) GetProject(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	// Check membership (return 404 to avoid enumeration)
	role, err := h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	var p models.Project
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id, name, display_name, owner_type, owner_id, default_environment, quotas, created_at, updated_at
		 FROM projects WHERE id = $1`,
		projectID,
	).Scan(&p.ID, &p.Name, &p.DisplayName, &p.OwnerType, &p.OwnerID,
		&p.DefaultEnvironment, &p.Quotas, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to fetch project")
		return
	}

	// Fetch environments
	envRows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, name, namespace, type, runtime, app_server_id, created_at, updated_at
		 FROM environments WHERE project_id = $1 ORDER BY name`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query environments")
		return
	}
	defer envRows.Close()

	var envs []models.Environment
	for envRows.Next() {
		var e models.Environment
		if err := envRows.Scan(
			&e.ID, &e.ProjectID, &e.Name, &e.Namespace, &e.Type, &e.Runtime, &e.AppServerID, &e.CreatedAt, &e.UpdatedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan environment")
			return
		}
		envs = append(envs, e)
	}
	if err := envRows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading environments")
		return
	}
	if envs == nil {
		envs = []models.Environment{}
	}

	c.JSON(http.StatusOK, gin.H{
		"project":      p,
		"role":         role,
		"environments": envs,
	})
}

// GetProjectOperations returns paginated operations for a project.
func (h *Handler) GetProjectOperations(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	// Verify membership
	_, err = h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		        status, payload, validation_result, git_commit, git_path, argo_application,
		        error_code, error_message, created_at, updated_at
		 FROM operations WHERE project_id = $1 ORDER BY created_at DESC LIMIT 50`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query operations")
		return
	}
	defer rows.Close()

	var ops []models.Operation
	for rows.Next() {
		var op models.Operation
		if err := scanOperation(rows, &op); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan operation")
			return
		}
		ops = append(ops, op)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading operations")
		return
	}
	if ops == nil {
		ops = []models.Operation{}
	}

	c.JSON(http.StatusOK, gin.H{"operations": ops})
}
