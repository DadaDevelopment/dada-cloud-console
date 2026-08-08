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
	var volume gin.H
	if s.Volume != nil {
		volume = gin.H{"path": s.Volume.Path, "size": s.Volume.Size}
	}
	return gin.H{
		"slug":       s.Slug,
		"name":       s.Name,
		"tagline":    s.Tagline,
		"icon":       s.Icon(),
		"source":     s.Source(),
		"image":      s.Image,
		"volume":     volume,
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
// you" rather than as a temporary upstream problem. A build-agent that is not
// configured at all counts as the same kind of failure: the search was owed and
// did not happen, and reporting searched=true with no rows and no flag would
// blame the customer's query for our missing dependency.
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

	searchFailed := res.SearchQuery != "" && h.buildagent == nil
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

	h.recordAuditAsync(claims.UserID, auditEntry{
		ProjectID:    projectID,
		Action:       "ResolveSolution",
		ResourceKind: "Solution",
		ResourceName: query,
		Outcome:      auditOutcomeSuccess,
		Metadata: map[string]any{
			"query":         query,
			"candidates":    len(out),
			"catalog_hits":  len(res.Candidates),
			"searched":      res.SearchQuery != "",
			"search_failed": searchFailed,
		},
	})

	c.JSON(http.StatusOK, gin.H{
		"query":         query,
		"candidates":    out,
		"searched":      res.SearchQuery != "",
		"search_failed": searchFailed,
	})
}

// installSolutionRequest is one "install this" click.
//
// Everything except what the customer picked is optional: a catalog slug
// carries the verified build spec, and a bare repository falls back to the same
// server-side defaults the connect-repo endpoint applies. WithDatabase is a
// pointer so "the customer explicitly said no" is distinguishable from "the
// customer said nothing", which is the only way a catalog entry that declares
// Needs can default to yes and still be refusable.
type installSolutionRequest struct {
	Slug         string            `json:"slug"`
	Repo         string            `json:"repo"`
	AppName      string            `json:"app_name"`
	Branch       string            `json:"branch"`
	RootDir      string            `json:"root_dir"`
	Framework    string            `json:"framework"`
	Port         int               `json:"port"`
	Profile      string            `json:"profile"`
	WithDatabase *bool             `json:"with_database"`
	Params       map[string]string `json:"params"`
}

// managedDatabaseNameFor derives the database resource name and PostgreSQL
// database name for an app that asked for one.
//
// Both are derived rather than asked for because the customer installing a
// ready-made project has no opinion about either, and a name they never chose
// is a name they cannot get wrong. The resource keeps the "-db" suffix so it
// reads as the app's database in every list; the database itself reuses the app
// name, prefixed when the app name starts with a digit because validatePgName
// requires a leading letter.
func managedDatabaseNameFor(appName string) (resource, database string) {
	resource = appName + "-db"
	if len(resource) > 63 {
		resource = resource[:63]
	}
	database = appName
	if database == "" || database[0] < 'a' || database[0] > 'z' {
		database = "db-" + database
	}
	if len(database) > 63 {
		database = database[:63]
	}
	return resource, database
}

// appNameForInstall picks the app name an install lands on.
func appNameForInstall(req installSolutionRequest, repoFullName string) string {
	if req.AppName != "" {
		return req.AppName
	}
	if req.Slug != "" {
		return req.Slug
	}
	name := strings.ToLower(repoShortName(repoFullName))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// InstallSolution turns one click into a running project.
//
// The console used to assemble this itself: link the repository, then order a
// database, then trigger a build, three calls it had to keep in the right order
// and unwind by hand when the middle one failed. That is a sequence, not a
// decision, so it belongs on the server — and putting it here is what lets a
// catalog entry declare `needs: [postgres]` and have the database appear
// already bound to the app, instead of the customer reading a "now create a
// database" instruction under a project that does not work yet.
//
// It composes the existing cores rather than reimplementing them: linkGitRepo
// applies the same defaults and installation resolution the connect-repo
// endpoint applies, and createManagedDatabase generates the credential and
// seeds DATABASE_URL exactly as ordering a database by hand does.
//
// Failure is reported, not unwound. If the database order fails after the
// repository is linked, the link stays and the response says so: the link is
// the part the customer can see and reuse, and silently deleting it would turn
// a recoverable half-install into a mystery.
//
// @ID          installSolution
// @Summary     Install a ready-made project
// @Description Links the repository, orders any managed database the project declares it needs (bound to the app, with the connection string injected), and queues the first build — the sequence the console used to run as three calls. Accepts a catalog slug or any public repository. Requires write access.
// @Tags        solutions
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                  true "Project UUID"
// @Param       envId     path     string                  true "Environment UUID"
// @Param       body      body     installSolutionRequest  true "What to install"
// @Success     202       {object} map[string]interface{} "object with the app name, the queued build and any database operation"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/solutions/install [post]
func (h *Handler) InstallSolution(c *gin.Context) {
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
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	appAudit := ""
	audit := func(outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "InstallSolution",
			ResourceKind:  "Solution",
			ResourceName:  appAudit,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		respondError(c, status, msg)
	}

	var req installSolutionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rejectErr(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}

	link := connectGitRepoRequest{
		Provider:         "github",
		ProductionBranch: req.Branch,
		RootDir:          req.RootDir,
		Port:             req.Port,
		Profile:          req.Profile,
		AutoDeploy:       true,
	}
	needsDatabase := false

	if req.Slug != "" {
		s, found := solutions.Lookup(req.Slug)
		if !found {
			rejectErr(http.StatusNotFound, "unknown_solution", "no such ready-made project")
			return
		}
		if s.IsImage() {
			h.installImageSolution(c, claims, projectID, envID, s, req)
			return
		}
		link.RepoFullName = s.Repo
		if link.ProductionBranch == "" {
			link.ProductionBranch = s.Branch
		}
		if link.RootDir == "" {
			link.RootDir = s.RootDir
		}
		if link.Port == 0 {
			link.Port = s.Port
		}
		if link.Profile == "" {
			link.Profile = s.Profile
		}
		link.FrameworkOverride = s.Framework
		for _, need := range s.Needs {
			if need == "postgres" {
				needsDatabase = true
			}
		}
	} else {
		if req.Repo == "" {
			rejectErr(http.StatusBadRequest, "missing_repo", "slug or repo is required")
			return
		}
		full, perr := solutions.ParseRepoURL(req.Repo)
		if perr != nil {
			rejectErr(http.StatusBadRequest, "invalid_repo", perr.Error())
			return
		}
		link.RepoFullName = full
		link.FrameworkOverride = req.Framework
	}

	if req.WithDatabase != nil {
		needsDatabase = *req.WithDatabase
	}

	link.AppName = appNameForInstall(req, link.RepoFullName)
	appAudit = link.AppName
	if err := validateKubeName(link.AppName); err != nil {
		rejectErr(http.StatusBadRequest, "invalid_app_name", err.Error())
		return
	}

	repo, fault := h.linkGitRepo(c.Request.Context(), claims.UserID, projectID, envID, &link)
	if fault != nil {
		rejectErr(fault.Status, fault.Reason, fault.Message)
		return
	}

	var dbOperation any
	if needsDatabase {
		resource, database := managedDatabaseNameFor(link.AppName)
		res, dbFault := h.createManagedDatabase(c.Request.Context(), claims.UserID, projectID, envID, createServiceDatabaseRequest{
			Name:     resource,
			Database: database,
			AppRef:   link.AppName,
		})
		if dbFault != nil {
			audit(auditOutcomeFailure, map[string]any{
				"reason":      dbFault.Reason,
				"status":      dbFault.Status,
				"stage":       "database",
				"repo_linked": true,
				"app":         link.AppName,
			})
			respondError(c, dbFault.Status, dbFault.Message)
			return
		}
		dbOperation = res.Operation
	}

	var b build
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO builds
		   (git_repo_id, environment_id, app_name, commit_sha, branch, triggered_by, trigger, status)
		 VALUES ($1, $2, $3, $4, $5, $6, 'manual', 'queued')
		 RETURNING `+buildSelectCols,
		repo.ID, envID, link.AppName, placeholderCommitSHA(), link.ProductionBranch, claims.UserID,
	)
	if err := scanBuild(row, &b); err != nil {
		audit(auditOutcomeFailure, map[string]any{
			"reason":      "queue_failed",
			"stage":       "build",
			"repo_linked": true,
			"app":         link.AppName,
		})
		respondError(c, http.StatusInternalServerError, "failed to queue build")
		return
	}

	audit(auditOutcomeSuccess, map[string]any{
		"slug":     req.Slug,
		"repo":     link.RepoFullName,
		"branch":   link.ProductionBranch,
		"app":      link.AppName,
		"database": needsDatabase,
		"build_id": b.ID.String(),
	})
	h.notifyAuditEvent(claims, projectID, "InstallSolution", link.AppName)

	c.JSON(http.StatusAccepted, gin.H{
		"app_name":  link.AppName,
		"repo":      *repo,
		"build":     b,
		"database":  dbOperation,
		"installed": true,
	})
}

// installImageSolution installs a catalog entry that ships a published image.
//
// The build track cannot carry a volume, so every project that keeps state on
// disk was unreachable from the catalog. This path skips the pipeline and
// creates the ordinary image app a customer would create by hand — same quota
// gate, same storage ceiling, same name-uniqueness rule, because it goes through
// createAppOp rather than around it.
//
// Order matters. Parameters and the managed database land in env_vars BEFORE the
// CreateApp operation is queued, so the container starts with its configuration
// already present instead of crash-looping until a second deploy carries it.
func (h *Handler) installImageSolution(c *gin.Context, claims *auth.Claims, projectID, envID uuid.UUID, s solutions.Solution, req installSolutionRequest) {
	appName := appNameForInstall(req, s.Repo)
	audit := func(outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "InstallSolution",
			ResourceKind:  "Solution",
			ResourceName:  appName,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(auditOutcomeFailure, map[string]any{"reason": reason, "status": status, "track": "image"})
		respondError(c, status, msg)
	}

	if err := validateKubeName(appName); err != nil {
		rejectErr(http.StatusBadRequest, "invalid_app_name", err.Error())
		return
	}

	env, err := s.ResolveParams(req.Params)
	if err != nil {
		rejectErr(http.StatusBadRequest, "invalid_params", err.Error())
		return
	}
	for key, value := range s.Env {
		if _, set := env[key]; !set {
			env[key] = value
		}
	}
	secretEnv := make(map[string]bool, len(s.Params))
	for _, p := range s.Params {
		secretEnv[p.EnvKey] = p.Kind == solutions.ParamSecret
	}
	for key, value := range env {
		if _, err := h.upsertEnvVar(c.Request.Context(), envID, appName, key, value, secretEnv[key], "runtime", claims.UserID.String()); err != nil {
			rejectErr(http.StatusInternalServerError, "env_failed", "failed to store the project's settings")
			return
		}
	}

	needsDatabase := false
	for _, need := range s.Needs {
		if need == "postgres" {
			needsDatabase = true
		}
	}
	if req.WithDatabase != nil {
		needsDatabase = *req.WithDatabase
	}

	var dbOperation any
	if needsDatabase {
		resource, database := managedDatabaseNameFor(appName)
		res, dbFault := h.createManagedDatabase(c.Request.Context(), claims.UserID, projectID, envID, createServiceDatabaseRequest{
			Name:     resource,
			Database: database,
			AppRef:   appName,
		})
		if dbFault != nil {
			audit(auditOutcomeFailure, map[string]any{
				"reason": dbFault.Reason,
				"status": dbFault.Status,
				"stage":  "database",
				"track":  "image",
				"app":    appName,
			})
			respondError(c, dbFault.Status, dbFault.Message)
			return
		}
		dbOperation = res.Operation
	}

	create := createAppRequest{
		Name:     appName,
		Image:    s.Image,
		Port:     s.Port,
		Replicas: 1,
		Profile:  s.Profile,
	}
	if req.Port != 0 {
		create.Port = req.Port
	}
	if req.Profile != "" {
		create.Profile = req.Profile
	}
	if s.Volume != nil {
		create.Volume = &appVolumeReq{Path: s.Volume.Path, Size: s.Volume.Size, FSGroup: s.Volume.FSGroup}
	}

	op, defaultHostname, fault := h.createAppOp(c, claims, projectID, envID, create)
	if fault != nil {
		audit(auditOutcomeFailure, map[string]any{
			"reason": fault.Reason,
			"status": fault.Status,
			"stage":  "app",
			"track":  "image",
			"app":    appName,
		})
		if fault.Status != 0 {
			respondError(c, fault.Status, fault.Message)
		}
		return
	}

	audit(auditOutcomeSuccess, map[string]any{
		"slug":     s.Slug,
		"image":    s.Image,
		"app":      appName,
		"track":    "image",
		"database": needsDatabase,
		"volume":   s.Volume != nil,
	})
	h.notifyAuditEvent(claims, projectID, "InstallSolution", appName)

	c.JSON(http.StatusAccepted, gin.H{
		"app_name":         appName,
		"operation":        op,
		"default_hostname": defaultHostname,
		"database":         dbOperation,
		"source":           "image",
		"installed":        true,
	})
}

// repoShortName is the repository half of "owner/name".
func repoShortName(full string) string {
	if _, name, ok := strings.Cut(full, "/"); ok && name != "" {
		return name
	}
	return full
}
