package api

import (
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/solutions"

	"github.com/gin-gonic/gin"
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
