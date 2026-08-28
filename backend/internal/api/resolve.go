package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// refProject is a project as an address rather than as a record: the id a tool
// call needs, next to the name a human and a log line use.
type refProject struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name,omitempty"`
	OrgID       string    `json:"org_id,omitempty"`
}

// refEnvironment is one environment of a project, in the same shape.
type refEnvironment struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Namespace string    `json:"namespace,omitempty"`
	Runtime   string    `json:"runtime,omitempty"`
}

// refApp is what a caller needs to know about an app before writing to it:
// where it is, what it runs, and which environment variables the console
// believes it owns. EnvVarKeys is deliberately keys only — values never leave
// through an addressing endpoint.
type refApp struct {
	Name        string   `json:"name"`
	Phase       string   `json:"phase,omitempty"`
	Image       string   `json:"image,omitempty"`
	URL         string   `json:"url,omitempty"`
	EnvVarKeys  []string `json:"env_var_keys"`
	ClusterEnv  []string `json:"cluster_env_keys"`
	ClusterSeen bool     `json:"cluster_env_observed"`
}

// resolveResponse answers "where is internal/prod/telemost-bot" in one call.
//
// The fields below the resolved level are the map for the next step: asking for
// a project lists its environments, asking for an environment lists its app
// names. That is what makes this endpoint a replacement for the
// listProjects -> getProject -> listApps walk rather than a fourth stop on it.
type resolveResponse struct {
	Ref          string           `json:"ref"`
	Project      *refProject      `json:"project,omitempty"`
	Environment  *refEnvironment  `json:"environment,omitempty"`
	App          *refApp          `json:"app,omitempty"`
	Environments []refEnvironment `json:"environments,omitempty"`
	Apps         []string         `json:"apps,omitempty"`
}

// ResolveRef turns a human-readable address into ids.
//
// @Summary     Resolve a project/environment/app name to ids
// @Description Turns the address people and logs actually use — "internal/prod/telemost-bot" — into the ids every other tool takes, in ONE call.
// @Description
// @Description Prefer this over the listProjects -> getProject -> listApps walk: those are three calls, two of which return whole records, and the app listing can be large enough to blow a context window. Pass `ref` as `project`, `project/env` or `project/env/app` (or pass `project`, `env` and `app` separately).
// @Description
// @Description The answer also carries the next level down: resolving a project lists its environments, resolving an environment lists its app NAMES. Resolving an app additionally returns its current image, phase, the env-var keys the console manages and the env-var keys actually present in the cluster.
// @Description
// @Description The project may be named by its slug ("agents") or by the display name the console shows ("Agent Runtime"); case, spaces, dashes and underscores are ignored. A slug match always wins over a display-name match. A project id is also a valid first segment, and an environment id a valid second one, so an address that came back from another tool can be pasted straight back in.
// @Description
// @Description An ambiguous name (the same project name visible in two orgs) is a 409 that names the candidates rather than a guess. A name that matches nothing is a 404 that lists the visible projects whose names look like it.
// @Tags        resolve
// @Produce     json
// @Param       ref query string false "Address: project, project/env or project/env/app"
// @Param       project query string false "Project name (alternative to ref)"
// @Param       env query string false "Environment name"
// @Param       app query string false "App name"
// @Success     200 {object} resolveResponse
// @Failure     404 {object} map[string]string
// @Failure     409 {object} map[string]string
// @Router      /resolve [get]
// @ID          resolveRef
func (h *Handler) ResolveRef(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectName, envName, appName := parseRef(
		c.Query("ref"), c.Query("project"), c.Query("env"), c.Query("app"))
	if projectName == "" {
		respondError(c, http.StatusBadRequest,
			"pass ref=project[/env[/app]] or the project/env/app query parameters")
		return
	}

	ctx := c.Request.Context()

	candidates, err := h.visibleProjects(ctx, claims, projectName)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve project")
		return
	}
	switch len(candidates) {
	case 0:
		near, nerr := h.nameCandidates(ctx, claims, projectName)
		if nerr != nil {
			near = nil
		}
		c.JSON(http.StatusNotFound, gin.H{
			"error":      "no such project",
			"asked":      projectName,
			"candidates": near,
		})
		return
	case 1:
	default:
		c.JSON(http.StatusConflict, gin.H{
			"error":      "project name is ambiguous",
			"candidates": candidates,
		})
		return
	}
	project := candidates[0]

	out := resolveResponse{Ref: joinRef(projectName, envName, appName), Project: &project}

	envs, err := h.projectEnvironments(ctx, project.ID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve environments")
		return
	}
	if envName == "" {
		out.Environments = envs
		c.JSON(http.StatusOK, out)
		return
	}

	var env *refEnvironment
	for i := range envs {
		if strings.EqualFold(envs[i].Name, envName) || envs[i].ID.String() == envName {
			env = &envs[i]
			break
		}
	}
	if env == nil {
		out.Environments = envs
		c.JSON(http.StatusNotFound, gin.H{
			"error":        "no such environment in this project",
			"project":      project,
			"environments": envs,
		})
		return
	}
	out.Environment = env

	if appName == "" {
		names, err := h.environmentAppNames(ctx, project.ID, env.ID)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to resolve apps")
			return
		}
		out.Apps = names
		c.JSON(http.StatusOK, out)
		return
	}

	app, err := h.resolveApp(ctx, project.ID, env.ID, env.Namespace, appName)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve app")
		return
	}
	if app == nil {
		names, nerr := h.environmentAppNames(ctx, project.ID, env.ID)
		if nerr != nil {
			names = nil
		}
		c.JSON(http.StatusNotFound, gin.H{
			"error":       "no such app in this environment",
			"project":     project,
			"environment": env,
			"apps":        names,
		})
		return
	}
	out.App = app
	c.JSON(http.StatusOK, out)
}

// parseRef reads an address from either form. A slash-separated ref wins over
// the separate parameters for any level it supplies, so mixing the two cannot
// silently address a different app than the caller wrote.
func parseRef(ref, project, env, app string) (string, string, string) {
	parts := make([]string, 0, 3)
	for _, p := range strings.Split(strings.Trim(strings.TrimSpace(ref), "/"), "/") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	get := func(i int, fallback string) string {
		if i < len(parts) {
			return parts[i]
		}
		return strings.TrimSpace(fallback)
	}
	return get(0, project), get(1, env), get(2, app)
}

func joinRef(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			break
		}
		out = append(out, p)
	}
	return strings.Join(out, "/")
}

// visibleProjects resolves the first segment of an address, which is a name in
// the general case and an id whenever the caller already holds one.
//
// A project id is an address people genuinely write: it is what the console
// URL carries, what every other tool returns, and what this project's own
// CLAUDE.md publishes for the sandbox. Matching names only meant that pasting
// the id back in answered "no such project" with no candidates — the exact
// dead end this endpoint exists to remove, and the reason listAgents on the
// sandbox id took three calls on 2026-08-27.
func (h *Handler) visibleProjects(ctx context.Context, claims *auth.Claims, ask string) ([]refProject, error) {
	if id, err := uuid.Parse(ask); err == nil {
		return h.visibleProjectByID(ctx, claims, id)
	}
	return h.visibleProjectsByName(ctx, claims, ask)
}

// visibleProjectByID reads one project through the same visibility sources as
// visibleProjectsByName, so an id never reaches further than a name would.
func (h *Handler) visibleProjectByID(ctx context.Context, claims *auth.Claims, id uuid.UUID) ([]refProject, error) {
	var p refProject
	err := h.pool.QueryRow(ctx,
		`SELECT p.id, p.name, COALESCE(p.display_name, ''), COALESCE(p.org_id, '')
		   FROM projects p
		  WHERE p.id = $1 AND ($2 OR p.id = ANY($3) OR p.org_id = ANY($4))`,
		id, isGod(claims), claimProjectIDs(claims), adminOrgIDs(claims),
	).Scan(&p.ID, &p.Name, &p.DisplayName, &p.OrgID)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []refProject{p}, nil
}

// visibleProjectsByName finds projects the caller may see whose name the
// caller could plausibly have written, using the same three visibility sources
// as ListProjects (ADR-009): an explicit project role, the org Owner/Admin
// cascade, or /platform-admins.
//
// It matches display_name as well as name, and both in a normalized form
// (case folded, punctuation and spaces removed). The console breadcrumb, the
// project switcher and every screenshot show display_name — "Agent Runtime" —
// while the address is the slug "agents". Matching only the slug meant the one
// name a caller can actually read was the one name that resolved to a 404.
//
// Matches are ranked so a literal slug always beats a display name: dozens of
// projects share the display_name "Default", and letting those collide with a
// project genuinely named "default" would turn an exact address into a 409.
//
// Within a rank it returns every match rather than the first: names are unique
// per owner, not globally, so one name can resolve to two projects in two orgs.
// The caller turns more than one into a 409 instead of picking, because picking
// is how an agent writes to the wrong tenant.
func (h *Handler) visibleProjectsByName(ctx context.Context, claims *auth.Claims, name string) ([]refProject, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT p.id, p.name, p.display_name, COALESCE(p.org_id, ''),
		        CASE
		          WHEN lower(p.name) = lower($1) THEN 1
		          WHEN lower(COALESCE(p.display_name, '')) = lower($1) THEN 2
		          WHEN `+normalizedSQL("p.name")+` = $5 THEN 3
		          ELSE 4
		        END AS rank
		   FROM projects p
		  WHERE ($2 OR p.id = ANY($3) OR p.org_id = ANY($4))
		    AND (lower(p.name) = lower($1)
		         OR lower(COALESCE(p.display_name, '')) = lower($1)
		         OR `+normalizedSQL("p.name")+` = $5
		         OR `+normalizedSQL("COALESCE(p.display_name, '')")+` = $5)
		    AND $5 <> ''
		  ORDER BY rank, p.name`,
		name, isGod(claims), claimProjectIDs(claims), adminOrgIDs(claims), normalizeName(name),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []refProject
	bestRank := 0
	for rows.Next() {
		var p refProject
		var rank int
		if err := rows.Scan(&p.ID, &p.Name, &p.DisplayName, &p.OrgID, &rank); err != nil {
			return nil, err
		}
		if bestRank == 0 {
			bestRank = rank
		}
		if rank != bestRank {
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// nameCandidates is what the caller gets instead of a bare "not found": the
// visible projects whose name or display name looks like what was asked for.
//
// A miss with no candidates is what sends an agent back to listing every
// project — the walk this endpoint exists to remove. Every other miss here
// (no such environment, no such app) already names what it could see; the
// project level was the one that did not.
func (h *Handler) nameCandidates(ctx context.Context, claims *auth.Claims, name string) ([]refProject, error) {
	norm := normalizeName(name)
	if norm == "" {
		return nil, nil
	}
	rows, err := h.pool.Query(ctx,
		`SELECT p.id, p.name, p.display_name, COALESCE(p.org_id, '')
		   FROM projects p
		  WHERE ($2 OR p.id = ANY($3) OR p.org_id = ANY($4))
		    AND (`+normalizedSQL("p.name")+` LIKE '%' || $1 || '%'
		         OR `+normalizedSQL("COALESCE(p.display_name, '')")+` LIKE '%' || $1 || '%'
		         OR $1 LIKE '%' || `+normalizedSQL("p.name")+` || '%')
		  ORDER BY length(p.name), p.name
		  LIMIT 10`,
		norm, isGod(claims), claimProjectIDs(claims), adminOrgIDs(claims),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []refProject
	for rows.Next() {
		var p refProject
		if err := rows.Scan(&p.ID, &p.Name, &p.DisplayName, &p.OrgID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// normalizedSQL folds an identifier the same way normalizeName folds the
// argument, so "Agent Runtime", "agent-runtime" and "AgentRuntime" are one key.
func normalizedSQL(column string) string {
	return "regexp_replace(lower(" + column + "), '[^a-z0-9]', '', 'g')"
}

// normalizeName is normalizedSQL in Go, for the argument side of the match.
func normalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (h *Handler) projectEnvironments(ctx context.Context, projectID uuid.UUID) ([]refEnvironment, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT id, name, COALESCE(namespace, ''), COALESCE(runtime, 'k8s')
		   FROM environments WHERE project_id = $1 ORDER BY name`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []refEnvironment{}
	for rows.Next() {
		var e refEnvironment
		if err := rows.Scan(&e.ID, &e.Name, &e.Namespace, &e.Runtime); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// environmentAppNames lists app names only. The full snapshot of one app is
// already more than a caller resolving an address needs, and the snapshot of
// every app in a busy environment is the response that did not fit in a context
// window on 2026-08-21.
func (h *Handler) environmentAppNames(ctx context.Context, projectID, envID uuid.UUID) ([]string, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT rs.name FROM resource_snapshots rs
		  WHERE rs.project_id = $1 AND rs.environment_id = $2 AND rs.kind = 'App'
		    AND `+notOrphanedSnapshot+`
		  ORDER BY rs.name`,
		projectID, envID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// resolveApp returns nil, nil when the environment has no such app.
func (h *Handler) resolveApp(ctx context.Context, projectID, envID uuid.UUID, namespace, appName string) (*refApp, error) {
	var phase string
	var summary json.RawMessage
	err := h.pool.QueryRow(ctx,
		`SELECT rs.phase, rs.summary_json FROM resource_snapshots rs
		  WHERE rs.project_id = $1 AND rs.environment_id = $2 AND rs.kind = 'App'
		    AND rs.name = $3 AND `+notOrphanedSnapshot,
		projectID, envID, appName,
	).Scan(&phase, &summary)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	app := &refApp{Name: appName, Phase: phase, EnvVarKeys: []string{}, ClusterEnv: []string{}}
	if len(summary) > 0 {
		var m map[string]any
		if json.Unmarshal(summary, &m) == nil {
			app.Image, _ = m["image"].(string)
			app.URL, _ = m["url"].(string)
		}
	}

	keys, err := h.appEnvVarKeys(ctx, envID, appName)
	if err != nil {
		return nil, err
	}
	app.EnvVarKeys = keys

	consoleKeys := make(map[string]bool, len(keys))
	for _, k := range keys {
		consoleKeys[k] = true
	}
	cluster := h.readClusterEnv(ctx, namespace, appName, consoleKeys)
	app.ClusterSeen = cluster.Observed
	for _, v := range cluster.Vars {
		app.ClusterEnv = append(app.ClusterEnv, v.Key)
	}

	return app, nil
}

func (h *Handler) appEnvVarKeys(ctx context.Context, envID uuid.UUID, appName string) ([]string, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT key FROM env_vars WHERE environment_id = $1 AND app_name = $2 ORDER BY key`,
		envID, appName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
