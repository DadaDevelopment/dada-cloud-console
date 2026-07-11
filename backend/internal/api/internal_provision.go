package api

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// requireInternalToken guards the server-to-server /internal API with a shared
// secret carried in the X-Internal-Token header. It is a constant-time compare
// so the check does not leak the token by timing. The token is never a user
// credential — only user-service (and other trusted backend services) hold it.
func requireInternalToken(token string) gin.HandlerFunc {
	want := []byte(token)
	return func(c *gin.Context) {
		got := []byte(c.GetHeader("X-Internal-Token"))
		if len(want) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid internal token"})
			return
		}
		c.Next()
	}
}

// provisionProjectRequest is the body user-service sends when it mints a project
// (ADR-009 key flow). dada-cloud does NOT generate ProjectID — it is supplied by
// user-service, which owns project identity. dada-cloud only creates the local
// resource row + default environment keyed by that id.
type provisionProjectRequest struct {
	ProjectID          uuid.UUID `json:"project_id" binding:"required"`
	OrgID              string    `json:"org_id" binding:"required"`
	Slug               string    `json:"slug" binding:"required"`
	DisplayName        string    `json:"display_name"`
	DefaultEnvironment string    `json:"default_environment"`
}

// ProvisionProject creates the dada-cloud project resource row + its default
// environment for a project minted by user-service. Idempotent on project_id: a
// repeat call (cross-service retry) returns the same row without error.
//
// POST /internal/projects  (guarded by requireInternalToken)
func (h *Handler) ProvisionProject(c *gin.Context) {
	var req provisionProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Slug
	}
	defaultEnv := req.DefaultEnvironment
	if defaultEnv == "" {
		defaultEnv = "prod"
	}
	// environments.type is constrained to ('dev','prod'); a non-prod default env
	// name still gets a valid type.
	envType := "prod"
	if defaultEnv == "dev" {
		envType = "dev"
	}

	ctx := c.Request.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to begin tx")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Insert the project resource row with the id user-service supplied. On a
	// retry the row already exists — keep it, refresh the mutable fields.
	if _, err := tx.Exec(ctx, `
		INSERT INTO projects (id, name, display_name, org_id, owner_type, default_environment)
		VALUES ($1, $2, $3, $4, 'team', $5)
		ON CONFLICT (id) DO UPDATE
		   SET display_name        = EXCLUDED.display_name,
		       org_id              = EXCLUDED.org_id,
		       default_environment = EXCLUDED.default_environment,
		       updated_at          = NOW()
	`, req.ProjectID, req.Slug, displayName, req.OrgID, defaultEnv); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create project: "+err.Error())
		return
	}

	var envID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO environments (project_id, name, namespace, type)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, name) DO UPDATE SET updated_at = NOW()
		RETURNING id
	`, req.ProjectID, defaultEnv, req.Slug+"-"+defaultEnv, envType).Scan(&envID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create default environment: "+err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to commit")
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"project_id":             req.ProjectID,
		"default_environment_id": envID,
	})
}

func (h *Handler) BackfillProjectGroups(c *gin.Context) {
	if h.usersvc == nil {
		respondError(c, http.StatusServiceUnavailable, "user-service client not configured")
		return
	}
	ctx := c.Request.Context()
	rows, err := h.pool.Query(ctx, `
		SELECT p.id, p.name, COALESCE(p.display_name, ''), COALESCE(p.org_id, ''), COALESCE(u.keycloak_sub, '')
		  FROM projects p
		  LEFT JOIN users u ON u.id = p.owner_id
		 WHERE COALESCE(p.org_id, '') <> ''
		 ORDER BY p.created_at
	`)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "query projects: "+err.Error())
		return
	}
	defer rows.Close()

	type item struct {
		id, name, display, org, sub string
	}
	var items []item
	for rows.Next() {
		var id uuid.UUID
		var name, display, org, sub string
		if err := rows.Scan(&id, &name, &display, &org, &sub); err != nil {
			respondError(c, http.StatusInternalServerError, "scan project: "+err.Error())
			return
		}
		items = append(items, item{id: id.String(), name: name, display: display, org: org, sub: sub})
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "iterate projects: "+err.Error())
		return
	}

	okCount := 0
	failCount := 0
	errs := make([]string, 0)
	for _, it := range items {
		if e := h.usersvc.EnsureProjectGroups(ctx, it.org, it.id, it.name, it.display, it.sub); e != nil {
			failCount++
			if len(errs) < 30 {
				errs = append(errs, it.id+": "+e.Error())
			}
			continue
		}
		okCount++
	}
	c.JSON(http.StatusOK, gin.H{
		"total":  len(items),
		"ok":     okCount,
		"failed": failCount,
		"errors": errs,
	})
}
