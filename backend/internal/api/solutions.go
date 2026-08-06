package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/buildagent"
	"github.com/dada-tuda/console/backend/internal/cache"
	"github.com/dada-tuda/console/backend/internal/solutions"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ready-made projects: the catalog that replaces the starter templates on the
// empty-project screen.
//
// These endpoints are deliberately thin. A catalog entry is a public repository
// plus the build spec we verified for it, and deploying one runs the EXISTING
// customer path — connect the repo, build it, deploy the image — rather than a
// parallel install mechanism. So the backend's whole job here is to hand out
// the catalog and to turn whatever the customer pasted into a repository name;
// everything after that is the same code a customer's own first deploy goes
// through, which is the point (see internal/solutions).

// solutionPayload is the wire shape of one catalog entry. Rendered by hand
// rather than by marshalling solutions.Solution, so the API surface stays a
// deliberate decision rather than a side effect of adding a field.
func solutionPayload(s solutions.Solution) gin.H {
	params := make([]gin.H, 0, len(s.Params))
	for _, p := range s.Params {
		params = append(params, gin.H{
			"key":         p.Key,
			"label":       p.Label,
			"help":        p.Help,
			"kind":        string(p.Kind),
			"required":    p.Required,
			"default":     p.Default,
			"options":     p.Options,
			"placeholder": p.Placeholder,
		})
	}
	return gin.H{
		"slug":       s.Slug,
		"name":       s.Name,
		"tagline":    s.Tagline,
		"about":      s.About,
		"bullets":    s.Bullets,
		"category":   string(s.Category),
		"homepage":   s.Homepage,
		"license":    s.License,
		"repo":       s.Repo,
		"branch":     s.Branch,
		"root_dir":   s.RootDir,
		"framework":  s.Framework,
		"port":       s.Port,
		"profile":    s.Profile,
		"warning":    s.Warning,
		"first_run":  s.FirstRun,
		"build_note": s.BuildNote,
		"params":     params,
	}
}

// ListSolutions returns the ready-made project catalog.
//
// @ID          listSolutions
// @Summary     List ready-made projects
// @Description Returns the catalog of open-source projects the console can deploy in one click. Each entry is a public repository plus the build spec verified for it (branch, root directory, framework, port, profile); deploying one uses the ordinary connect-repo and build path. Read-only; the catalog is the same for every project.
// @Tags        solutions
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{} "object with a solutions array"
// @Failure     401 {object} map[string]string
// @Router      /solutions [get]
func (h *Handler) ListSolutions(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	out := make([]gin.H, 0, len(solutions.V1))
	for _, s := range solutions.V1 {
		out = append(out, solutionPayload(s))
	}
	c.JSON(http.StatusOK, gin.H{"solutions": out})
}

// GetSolution returns one catalog entry by slug.
//
// @ID          getSolution
// @Summary     Get one ready-made project
// @Description Returns a single catalog entry with its build spec and any parameters it asks for. Read-only.
// @Tags        solutions
// @Produce     json
// @Security    BearerAuth
// @Param       slug path     string true "Solution slug"
// @Success     200  {object} map[string]interface{}
// @Failure     401  {object} map[string]string
// @Failure     404  {object} map[string]string
// @Router      /solutions/{slug} [get]
func (h *Handler) GetSolution(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	s, ok := solutions.Lookup(c.Param("slug"))
	if !ok {
		respondNotFound(c)
		return
	}
	c.JSON(http.StatusOK, solutionPayload(s))
}

// ParseRepoURL turns a pasted repository link into an "owner/name" pair.
//
// It exists so the "deploy any public repository" field has ONE definition of
// what a repository link is. The browser URL, the clone URL, the SSH remote and
// a bare owner/name all end up on a clipboard, and a rule that lives in both the
// console and the backend drifts until the two disagree about what the customer
// pasted — at which point one of them deploys the wrong repository.
//
// @ID          parseRepoURL
// @Summary     Parse a pasted repository link
// @Description Accepts a GitHub browser URL, clone URL, SSH remote or a bare owner/name and returns the canonical owner/name. Rejects anything that is not a public GitHub repository rather than guessing. Pure string handling: it does not check that the repository exists.
// @Tags        solutions
// @Produce     json
// @Security    BearerAuth
// @Param       url query    string true "Pasted repository link"
// @Success     200 {object} map[string]string "object with repo_full_name"
// @Failure     400 {object} map[string]string
// @Failure     401 {object} map[string]string
// @Router      /git/parse-repo-url [get]
func (h *Handler) ParseRepoURL(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	full, err := solutions.ParseRepoURL(c.Query("url"))
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"repo_full_name": full})
}

// searchCacheTTL caches one GitHub search answer.
//
// The search endpoint allows 30 requests a minute per source IP for the entire
// cluster, and an interactive input is the fastest way ever invented to spend
// such a budget. Popular queries repeat across customers — "n8n", "postgres",
// "wordpress" — so a shared cache with a short life gives the second person to
// type a word an instant answer and costs the first one nothing. Short rather
// than long because the point of search is that it reaches things the catalog
// does not, including repositories published this morning.
const searchCacheTTL = 30 * time.Minute

// searchResultLimit is how many search rows the console shows under the input.
const searchResultLimit = 6

// candidatePayload is the wire shape of one resolver row.
func candidatePayload(c solutions.Candidate) gin.H {
	return gin.H{
		"kind":      string(c.Kind),
		"slug":      c.Slug,
		"name":      c.Name,
		"tagline":   c.Tagline,
		"icon":      c.Icon,
		"repo":      c.Repo,
		"branch":    c.Branch,
		"root_dir":  c.RootDir,
		"framework": c.Framework,
		"port":      c.Port,
		"profile":   c.Profile,
		"engine":    c.Engine,
	}
}

// ResolveSolution answers the console's single "what do you want to run?" field.
//
// One field, one ranked list, three audiences: the catalog entry for the person
// who types a product name, the managed database for the person who types
// "post", and a repository for the person who pastes a link. Anything the local
// catalog cannot answer falls through to a GitHub search, appended BELOW the
// local rows — a curated entry carries a build spec we verified, a search hit
// carries a name and a star count.
//
// Search failure is not request failure. GitHub rate-limits, GitHub has
// outages, and an App installation can be removed; in every one of those cases
// the customer still gets the catalog rows and a `search_failed` flag, because
// a suggestion list that goes blank reads as "this platform has nothing for
// you" rather than as a temporary upstream problem.
//
// @ID          resolveSolution
// @Summary     Resolve what to deploy from one input string
// @Description Turns a single typed string into a ranked list of things the console can deploy: catalog entries, managed resources, a pasted repository, and GitHub search results below them. Requires write access to the project, because searching spends a rate-limit budget shared by the whole platform.
// @Tags        solutions
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       q         query    string true  "What the customer typed"
// @Success     200       {object} map[string]interface{} "object with a candidates array"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/solutions/resolve [get]
func (h *Handler) ResolveSolution(c *gin.Context) {
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
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		respondError(c, http.StatusBadRequest, "q is required")
		return
	}
	if len(query) > 200 {
		respondError(c, http.StatusBadRequest, "q is too long")
		return
	}

	res := solutions.Resolve(query)
	out := make([]gin.H, 0, len(res.Candidates)+searchResultLimit)
	for _, cand := range res.Candidates {
		out = append(out, candidatePayload(cand))
	}

	searchFailed := false
	if res.SearchQuery != "" && h.buildagent != nil {
		hits, err := cache.Fetch(c.Request.Context(), h.cache,
			fmt.Sprintf("git:search:public:%s", strings.ToLower(res.SearchQuery)), searchCacheTTL,
			func() (*[]buildagent.SearchHit, error) {
				found, err := h.buildagent.SearchRepos(c.Request.Context(), res.SearchQuery, searchResultLimit)
				if err != nil {
					return nil, err
				}
				return &found, nil
			})
		if err != nil {
			searchFailed = true
		} else if hits != nil {
			for _, hit := range *hits {
				if solutions.IsCatalogRepo(hit.FullName) {
					continue
				}
				out = append(out, gin.H{
					"kind":      string(solutions.CandidateRepo),
					"slug":      hit.FullName,
					"name":      repoShortName(hit.FullName),
					"tagline":   hit.Description,
					"icon":      hit.AvatarURL,
					"repo":      hit.FullName,
					"branch":    hit.DefaultBranch,
					"root_dir":  ".",
					"stars":     hit.Stars,
					"license":   hit.License,
					"archived":  hit.Archived,
					"from":      "search",
					"homepage":  hit.HTMLURL,
					"framework": "",
					"port":      0,
					"profile":   "",
					"engine":    "",
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"query":         query,
		"candidates":    out,
		"searched":      res.SearchQuery != "",
		"search_failed": searchFailed,
	})
}

// repoShortName is the repository half of "owner/name".
func repoShortName(full string) string {
	if _, name, ok := strings.Cut(full, "/"); ok && name != "" {
		return name
	}
	return full
}
