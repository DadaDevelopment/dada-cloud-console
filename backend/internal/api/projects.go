package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// projectSlugRe constrains a project slug to a DNS-1123-label-safe shape: it is
// used verbatim as the project name and as the namespace prefix (<slug>-<env>),
// so it must start with a letter, end alphanumeric, and stay short enough that
// the derived namespace fits in 63 chars.
var projectSlugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,38}[a-z0-9]$`)

// projectWithRole extends Project with the requesting user's role.
type projectWithRole struct {
	models.Project
	Role models.MemberRole `json:"role"`
}

// ListProjects returns all projects the authenticated user has access to.
//
// @ID          listProjects
// @Summary     List projects the caller can access
// @Description Returns every project the authenticated user is a member of, each annotated with the caller's role in that project. Read-only. Start here to discover project IDs for other calls.
// @Tags        project
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{} "object with a projects array"
// @Failure     401 {object} map[string]string
// @Router      /projects [get]
func (h *Handler) ListProjects(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	var projects []projectWithRole

	// Project visibility is derived purely from native claims (ADR-009): the
	// projects the caller has an explicit role on, plus every project owned by an
	// org where the caller is org Owner/Admin (cascade). God (/platform-admins)
	// sees every project. Multi-org: explicit grants and admin orgs both span
	// multiple tenants.
	god := isGod(claims)
	explicitIDs := claimProjectIDs(claims)
	adminOrgs := adminOrgIDs(claims)

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, name, display_name, owner_type, owner_id, COALESCE(org_id, ''),
		        default_environment, quotas, created_at, updated_at
		 FROM projects
		 WHERE $1
		    OR id = ANY($2)
		    OR org_id = ANY($3)
		 ORDER BY name`,
		god, explicitIDs, adminOrgs,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query projects")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var p projectWithRole
		if err := rows.Scan(
			&p.ID, &p.Name, &p.DisplayName, &p.OwnerType, &p.OwnerID, &p.OrgID,
			&p.DefaultEnvironment, &p.Quotas, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan project")
			return
		}
		switch {
		case god:
			p.Role = models.MemberRoleOwner
		default:
			// Effective role per project: max(orgRole(project.org), projectRole).
			org := models.MemberRole(claims.OrgRole(p.OrgID))
			if pr := claims.ProjectRole(p.ID.String()); pr != "" {
				p.Role = models.MaxRole(org, models.MemberRole(pr))
			} else {
				p.Role = org // visible via org Owner/Admin cascade
			}
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

// createProjectRequest is the body a console user sends to create a project.
type createProjectRequest struct {
	// Slug is the project name (DNS-1123-label-safe). Unique platform-wide.
	Slug string `json:"slug" binding:"required"`
	// DisplayName is the human label; defaults to Slug when empty.
	DisplayName string `json:"display_name"`
	// OrgID is the owning org. Empty → the caller's personal org (their username).
	// A non-empty value requires the caller to be Owner/Admin of that org.
	OrgID string `json:"org_id"`
	// DefaultEnvironment names the first environment; defaults to "prod".
	DefaultEnvironment string `json:"default_environment"`
}

// CreateProject creates a project owned by the caller. Self-service: by default
// the project lands in the caller's personal org (org_id = username), where the
// caller is implicitly Owner (ADR-009 follow-up). To create under a shared org
// the caller must be Owner/Admin of it. The gitops-agent db-watcher picks the new
// row up and bootstraps its git manifest.
//
// @ID          createProject
// @Summary     Create a project
// @Description Creates a project plus its default environment. Without org_id the project goes into your personal org (you are Owner). With org_id you must be Owner/Admin of that org. The slug must be DNS-1123-label-safe and is unique platform-wide.
// @Tags        project
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body     createProjectRequest true "Project to create"
// @Success     201  {object} map[string]interface{} "object with project_id, default_environment_id, org_id and role"
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Failure     403  {object} map[string]string
// @Failure     409  {object} map[string]string "slug already taken"
// @Router      /projects [post]
func (h *Handler) CreateProject(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	var req createProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	if !projectSlugRe.MatchString(slug) {
		respondError(c, http.StatusBadRequest, "invalid slug: use 3-40 chars, lowercase letters/digits/dashes, starting with a letter")
		return
	}

	// Resolve the owning org. Empty → personal org (the caller's username), where
	// the caller is implicitly Owner. Non-empty → must be Owner/Admin of that org.
	org := strings.TrimSpace(req.OrgID)
	if org == "" {
		org = claims.Username
		if org == "" {
			respondError(c, http.StatusBadRequest, "no username in token; cannot derive a personal org")
			return
		}
	} else if !isGod(claims) && !isOrgAdmin(models.MemberRole(claims.OrgRole(org))) {
		respondForbidden(c)
		return
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = slug
	}
	defaultEnv := strings.TrimSpace(req.DefaultEnvironment)
	if defaultEnv == "" {
		defaultEnv = "prod"
	}

	projectID, envID, err := h.insertProject(c.Request.Context(), claims.UserID, slug, displayName, org, defaultEnv)
	if err != nil {
		if isUniqueViolation(err) {
			respondError(c, http.StatusConflict, "a project with this slug already exists")
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to create project")
		return
	}

	role := models.MemberRole(claims.OrgRole(org))
	if isGod(claims) || role == "" {
		role = models.MemberRoleOwner
	}
	h.ensureProjectGroupsAsync(org, projectID.String(), slug, displayName, claims.Subject)

	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name)
		 VALUES ($1, $2, 'CreateProject', 'Project', $3)`,
		claims.UserID, projectID, slug,
	)
	h.notifyAuditEvent(claims, projectID, "CreateProject", slug)

	c.JSON(http.StatusCreated, gin.H{
		"project_id":             projectID,
		"default_environment_id": envID,
		"org_id":                 org,
		"role":                   role,
	})
}

// ensureProjectGroupsGap is the minimum spacing between user-service group-sync
// attempts for one project. A failed sync records only its attempt time (not
// success), so without this gap every EnsureDefaultProject/list call would spawn
// a fresh sync goroutine and stampede user-service.
const ensureProjectGroupsGap = 90 * time.Second

func (h *Handler) ensureProjectGroupsAsync(org, projectID, slug, displayName, ownerSub string) {
	if h.usersvc == nil {
		return
	}
	if _, done := h.groupsEnsured.Load(projectID); done {
		return
	}
	if last, ok := h.groupsAttempt.Load(projectID); ok {
		if t, _ := last.(time.Time); time.Since(t) < ensureProjectGroupsGap {
			return
		}
	}
	h.groupsAttempt.Store(projectID, time.Now())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := h.usersvc.EnsureProjectGroups(ctx, org, projectID, slug, displayName, ownerSub); err != nil {
			log.Printf("userservice: ensure project groups project=%s org=%s: %v", projectID, org, err)
			return
		}
		h.groupsEnsured.Store(projectID, struct{}{})
	}()
}

// insertProject creates a project row plus its default environment in one tx and
// returns the new ids. Shared by CreateProject and EnsureDefaultProject so both
// paths build identical rows (the gitops db-watcher then bootstraps the manifest).
func (h *Handler) insertProject(ctx context.Context, ownerID uuid.UUID, slug, displayName, org, defaultEnv string) (uuid.UUID, uuid.UUID, error) {
	envType := "prod"
	if defaultEnv == "dev" {
		envType = "dev"
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	projectID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO projects (id, name, display_name, org_id, owner_type, owner_id, default_environment)
		VALUES ($1, $2, $3, $4, 'team', $5, $6)
	`, projectID, slug, displayName, org, ownerID, defaultEnv); err != nil {
		return uuid.Nil, uuid.Nil, err
	}

	var envID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO environments (project_id, name, namespace, type)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, projectID, defaultEnv, slug+"-"+defaultEnv, envType).Scan(&envID); err != nil {
		return uuid.Nil, uuid.Nil, err
	}

	payloadBytes, err := json.Marshal(models.CreateProjectPayload{})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, $3, 'CreateProject', 'Project', $4, 'Created', $5)
	`, ownerID, projectID, envID, slug, payloadBytes); err != nil {
		return uuid.Nil, uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return projectID, envID, nil
}

// defaultProjectSlug derives a stable, DNS-1123-label-safe slug from a username so
// EnsureDefaultProject is idempotent per user (re-running finds the same slug and
// short-circuits on the existing row). Falls back to a hash when the username does
// not sanitize to a valid slug.
func defaultProjectSlug(username string) string {
	s := strings.ToLower(strings.TrimSpace(username))
	s = nonSlugChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if projectSlugRe.MatchString(s) {
		return s
	}
	var hash uint32 = 2166136261
	for _, r := range username {
		hash = (hash ^ uint32(r)) * 16777619
	}
	return "default-" + strconv.FormatUint(uint64(hash), 36)
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// defaultProjectDisplayName builds a human-readable display_name for an
// auto-provisioned personal project, e.g. "goleva.giftdev's project". Every
// auto-provisioned project used to share the literal "Default", which read as
// dozens of indistinguishable rows in admin-facing lists (038 backfills those).
func defaultProjectDisplayName(username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		return "Default"
	}
	return username + "'s project"
}

// EnsureDefaultProject returns the caller's default project, creating it when they
// have none. Idempotent: the console calls it on first load so the user always lands
// inside a project instead of an empty overview.
//
// @ID          ensureDefaultProject
// @Summary     Get or create the caller's default project
// @Description Returns the caller's default project, provisioning one (in your personal org, you are Owner) when you have zero projects. Idempotent — repeated calls return the same project.
// @Tags        project
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{} "object with project_id, default_environment_id, org_id and role"
// @Success     201 {object} map[string]interface{} "newly created default project"
// @Failure     401 {object} map[string]string
// @Router      /projects/default [post]
func (h *Handler) EnsureDefaultProject(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	ctx := c.Request.Context()
	god := isGod(claims)
	explicitIDs := claimProjectIDs(claims)
	adminOrgs := adminOrgIDs(claims)

	// Reuse the list-visibility predicate: any visible project means the caller
	// already has a home, so return the first one (stable by name) and stop.
	var (
		pid         uuid.UUID
		org         string
		envID       uuid.UUID
		slug        string
		displayName string
		ownerSub    string
	)
	err := h.pool.QueryRow(ctx, `
		SELECT p.id, COALESCE(p.org_id, ''),
		       (SELECT e.id FROM environments e WHERE e.project_id = p.id ORDER BY e.name LIMIT 1),
		       p.name, COALESCE(p.display_name, ''), COALESCE(u.keycloak_sub, '')
		  FROM projects p
		  LEFT JOIN users u ON u.id = p.owner_id
		 WHERE $1 OR p.id = ANY($2) OR p.org_id = ANY($3)
		 ORDER BY p.name
		 LIMIT 1
	`, god, explicitIDs, adminOrgs).Scan(&pid, &org, &envID, &slug, &displayName, &ownerSub)
	if err == nil {
		if org != "" {
			h.ensureProjectGroupsAsync(org, pid.String(), slug, displayName, ownerSub)
		}
		c.JSON(http.StatusOK, gin.H{
			"project_id":             pid,
			"default_environment_id": envID,
			"org_id":                 org,
			"role":                   effectiveCreateRole(claims, org),
		})
		return
	}
	if err != pgx.ErrNoRows {
		respondError(c, http.StatusInternalServerError, "failed to look up projects")
		return
	}

	// No visible project — provision the default in the caller's personal org.
	if claims.Username == "" {
		respondError(c, http.StatusBadRequest, "no username in token; cannot derive a personal org")
		return
	}
	personalOrg := claims.Username
	slug = defaultProjectSlug(claims.Username)
	displayName = defaultProjectDisplayName(claims.Username)
	pid, envID, err = h.insertProject(ctx, claims.UserID, slug, displayName, personalOrg, "prod")
	if err != nil {
		if isUniqueViolation(err) {
			// Slug already taken (race or pre-existing row not visible via claims):
			// fetch it so the call stays idempotent rather than 409-ing the console.
			if e := h.pool.QueryRow(ctx, `
				SELECT p.id, COALESCE(p.org_id, ''),
				       (SELECT e.id FROM environments e WHERE e.project_id = p.id ORDER BY e.name LIMIT 1)
				  FROM projects p WHERE p.name = $1
			`, slug).Scan(&pid, &org, &envID); e == nil {
				if org != "" {
					h.ensureProjectGroupsAsync(org, pid.String(), slug, displayName, claims.Subject)
				}
				c.JSON(http.StatusOK, gin.H{
					"project_id":             pid,
					"default_environment_id": envID,
					"org_id":                 org,
					"role":                   effectiveCreateRole(claims, org),
				})
				return
			}
		}
		respondError(c, http.StatusInternalServerError, "failed to create default project")
		return
	}
	h.ensureProjectGroupsAsync(personalOrg, pid.String(), slug, displayName, claims.Subject)
	c.JSON(http.StatusCreated, gin.H{
		"project_id":             pid,
		"default_environment_id": envID,
		"org_id":                 personalOrg,
		"role":                   effectiveCreateRole(claims, personalOrg),
	})
}

// effectiveCreateRole mirrors the role a creator/owner holds on a freshly made
// project: god or an unmapped personal org both resolve to Owner.
func effectiveCreateRole(claims *auth.Claims, org string) models.MemberRole {
	role := models.MemberRole(claims.OrgRole(org))
	if isGod(claims) || role == "" {
		role = models.MemberRoleOwner
	}
	return role
}

// GetProject returns a single project by ID, including environments and user role.
//
// @ID          getProject
// @Summary     Get a project with its environments
// @Description Returns one project, the caller's role, and the project's environments (each with id, name, namespace, runtime). Read-only. Use the returned environment IDs for app/database/model calls.
// @Tags        project
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "object with project, role and environments"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId} [get]
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
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	var p models.Project
	var ownerSub string
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT p.id, p.name, p.display_name, p.owner_type, p.owner_id, COALESCE(p.org_id, ''),
		        p.default_environment, p.quotas, p.created_at, p.updated_at, COALESCE(u.keycloak_sub, '')
		 FROM projects p LEFT JOIN users u ON u.id = p.owner_id WHERE p.id = $1`,
		projectID,
	).Scan(&p.ID, &p.Name, &p.DisplayName, &p.OwnerType, &p.OwnerID, &p.OrgID,
		&p.DefaultEnvironment, &p.Quotas, &p.CreatedAt, &p.UpdatedAt, &ownerSub)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to fetch project")
		return
	}

	if p.OrgID != "" {
		h.ensureProjectGroupsAsync(p.OrgID, p.ID.String(), p.Name, p.DisplayName, ownerSub)
	}

	// Fetch environments
	envRows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, name, namespace, type, runtime, app_server_id, limit_range, resource_quota,
		        is_ephemeral, pr_number, pr_head_branch, expires_at, created_at, updated_at
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
			&e.ID, &e.ProjectID, &e.Name, &e.Namespace, &e.Type, &e.Runtime, &e.AppServerID,
			&e.LimitRange, &e.ResourceQuota,
			&e.IsEphemeral, &e.PRNumber, &e.PRHeadBranch, &e.ExpiresAt, &e.CreatedAt, &e.UpdatedAt,
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
//
// @ID          listOperations
// @Summary     List recent operations in a project
// @Description Returns the 50 most recent async operations for a project (newest first), with their current status. Read-only. Use this to track the outcome of create/update/delete calls.
// @Tags        operation
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "object with an operations array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/operations [get]
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
	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
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

// SetNamespacePolicy creates a SetNamespacePolicy operation that instructs the
// gitops-agent to write clusters/beget-prod/namespace-policies/<namespace>.yaml.
//
// @ID          setNamespacePolicy
// @Summary     Set an environment's namespace LimitRange + ResourceQuota
// @Description Updates the Kubernetes LimitRange and ResourceQuota for an environment's namespace. Admin-only (platform-admin or client-admin). Asynchronous: returns 202 with an operation id; poll the operation endpoint until terminal.
// @Tags        project
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                 true "Project UUID"
// @Param       envId     path     string                 true "Environment UUID"
// @Param       body      body     map[string]interface{} true "Object with limit_range and resource_quota JSON specs"
// @Success     202       {object} map[string]interface{} "object with the accepted operation id"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/namespace-policy [put]
func (h *Handler) SetNamespacePolicy(c *gin.Context) {
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
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !isOrgAdmin(role) {
		respondForbidden(c)
		return
	}

	var body struct {
		LimitRange    json.RawMessage `json:"limit_range"`
		ResourceQuota json.RawMessage `json:"resource_quota"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	payload, _ := json.Marshal(map[string]json.RawMessage{
		"limit_range":    body.LimitRange,
		"resource_quota": body.ResourceQuota,
	})

	opID := uuid.New()
	if _, err := h.pool.Exec(c.Request.Context(), `
		INSERT INTO operations (id, actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, $3, $4, 'SetNamespacePolicy', 'NamespacePolicy', 'namespace-policy', 'pending', $5)
	`, opID, claims.UserID, projectID, envID, payload); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"operation_id": opID})
}
