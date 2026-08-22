package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// searchLimit caps each result group. The console renders the hits in a
// dropdown, so the list stays scannable rather than complete.
const searchLimit = 20

// searchProjectHit is one project matching the query.
type searchProjectHit struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	AppCount    int    `json:"app_count"`
}

// searchAppHit is one app matching the query, carrying enough context for the
// console to link straight at it: the owning project and the environment whose
// tab the app lives on.
type searchAppHit struct {
	Name               string `json:"name"`
	Phase              string `json:"phase"`
	ProjectID          string `json:"project_id"`
	ProjectName        string `json:"project_name"`
	ProjectDisplayName string `json:"project_display_name"`
	EnvironmentID      string `json:"environment_id"`
	EnvironmentName    string `json:"environment_name"`
}

// likePattern turns a raw user query into a safe ILIKE pattern: the LIKE
// metacharacters are escaped so a query of "100%" matches the literal string
// instead of everything.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// searchVisibility returns the three-part project visibility predicate shared by
// ListProjects and Search: platform staff see everything, everyone else sees the
// projects they hold a role on plus the projects of orgs they administer. Search
// reuses it verbatim so it can never widen access.
func searchVisibility(claims *auth.Claims) (bool, []uuid.UUID, []string) {
	return isGod(claims) || isPlatformAnalyst(claims), claimProjectIDs(claims), adminOrgIDs(claims)
}

// searchProjects returns visible projects whose slug or display name contains
// the query, most-populated first so live projects outrank empty leftovers.
func (h *Handler) searchProjects(ctx context.Context, god bool, ids []uuid.UUID, orgs []string, pattern string) ([]searchProjectHit, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT p.id, p.name, p.display_name, COALESCE(ac.n, 0)
		   FROM projects p
		   LEFT JOIN (
		        SELECT project_id, count(*) AS n
		          FROM resource_snapshots
		         WHERE kind = 'App' AND phase <> 'Orphaned'
		         GROUP BY project_id
		   ) ac ON ac.project_id = p.id
		  WHERE ($1 OR p.id = ANY($2) OR p.org_id = ANY($3))
		    AND (p.name ILIKE $4 ESCAPE '\' OR p.display_name ILIKE $4 ESCAPE '\')
		  ORDER BY COALESCE(ac.n, 0) DESC, p.name
		  LIMIT $5`,
		god, ids, orgs, pattern, searchLimit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hits := []searchProjectHit{}
	for rows.Next() {
		var p searchProjectHit
		if err := rows.Scan(&p.ID, &p.Name, &p.DisplayName, &p.AppCount); err != nil {
			return nil, err
		}
		hits = append(hits, p)
	}
	return hits, rows.Err()
}

// searchApps returns visible apps whose name contains the query.
//
// An app reaches the console by two roads: as a synced resource snapshot, and —
// before its first sync — as a connected git repo. Both are searched so a
// freshly connected app is findable immediately. DISTINCT ON keeps one row per
// (project, environment, name) and prefers the snapshot, which knows a phase.
func (h *Handler) searchApps(ctx context.Context, god bool, ids []uuid.UUID, orgs []string, pattern string) ([]searchAppHit, error) {
	rows, err := h.pool.Query(ctx,
		`WITH visible AS (
		     SELECT id, name, display_name
		       FROM projects
		      WHERE $1 OR id = ANY($2) OR org_id = ANY($3)
		 ),
		 candidates AS (
		     SELECT DISTINCT ON (project_id, environment_id, name)
		            project_id, environment_id, name, phase
		       FROM (
		            SELECT project_id, environment_id, name, phase::text AS phase
		              FROM resource_snapshots
		             WHERE kind = 'App' AND phase <> 'Orphaned'
		            UNION ALL
		            SELECT project_id, environment_id, app_name, NULL::text
		              FROM git_repos
		       ) u
		      WHERE name ILIKE $4 ESCAPE '\'
		      ORDER BY project_id, environment_id, name, phase NULLS LAST
		 )
		 SELECT ca.name, COALESCE(ca.phase, ''), v.id, v.name, v.display_name,
		        COALESCE(ca.environment_id::text, ''), COALESCE(e.name, '')
		   FROM candidates ca
		   JOIN visible v ON v.id = ca.project_id
		   LEFT JOIN environments e ON e.id = ca.environment_id
		  ORDER BY ca.name, v.name
		  LIMIT $5`,
		god, ids, orgs, pattern, searchLimit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hits := []searchAppHit{}
	for rows.Next() {
		var a searchAppHit
		if err := rows.Scan(&a.Name, &a.Phase, &a.ProjectID, &a.ProjectName,
			&a.ProjectDisplayName, &a.EnvironmentID, &a.EnvironmentName); err != nil {
			return nil, err
		}
		hits = append(hits, a)
	}
	return hits, rows.Err()
}

// Search finds projects and apps by name across everything the caller can see.
//
// Without it the only way to reach an app is to know its project and open that
// project's app list: a platform admin sees every project (dozens, most of them
// short-lived test leftovers with no apps at all), the switcher had no filter,
// and an app could not be looked up by name from anywhere.
//
// @ID          search
// @Summary     Search projects and apps by name
// @Description Case-insensitive substring search over the projects the caller can access and the apps inside them. Returns at most 20 hits per group. Queries shorter than 2 characters return empty groups.
// @Tags        project
// @Produce     json
// @Security    BearerAuth
// @Param       q query    string true "search term (min 2 characters)"
// @Success     200 {object} map[string]interface{} "object with projects and apps arrays"
// @Failure     401 {object} map[string]string
// @Router      /search [get]
func (h *Handler) Search(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	q := strings.TrimSpace(c.Query("q"))
	if len([]rune(q)) < 2 {
		c.JSON(http.StatusOK, gin.H{"projects": []searchProjectHit{}, "apps": []searchAppHit{}})
		return
	}

	god, ids, orgs := searchVisibility(claims)
	pattern := likePattern(q)
	ctx := c.Request.Context()

	projects, err := h.searchProjects(ctx, god, ids, orgs, pattern)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to search projects")
		return
	}
	apps, err := h.searchApps(ctx, god, ids, orgs, pattern)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to search apps")
		return
	}

	c.JSON(http.StatusOK, gin.H{"projects": projects, "apps": apps})
}
