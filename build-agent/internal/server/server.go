// Package server hosts the build-agent HTTP surface:
// /healthz, /metrics, /webhook/github (nudge), and /ws/build (log stream).
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/build-agent/internal/config"
	"github.com/dada-tuda/console/build-agent/internal/db"
	"github.com/dada-tuda/console/build-agent/internal/github"
	"github.com/dada-tuda/console/build-agent/internal/metrics"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// PushNudger is notified when a verified webhook arrives, so the poller can
// drain the queue immediately instead of waiting for the next tick.
type PushNudger interface {
	OnPush(ctx context.Context)
}

// Server is the build-agent HTTP server.
type Server struct {
	addr        string
	pool        *pgxpool.Pool
	hub         *Hub
	nudger      PushNudger
	tokenSecret string
	cfg         *config.Config
	gh          github.App
}

// Options carries server dependencies.
type Options struct {
	Pool        *pgxpool.Pool
	Hub         *Hub
	Nudger      PushNudger
	TokenSecret string
	Config      *config.Config
	GitHub      github.App
}

// New constructs a Server.
func New(addr string, opts *Options) *Server {
	s := &Server{addr: addr}
	if opts != nil {
		s.pool = opts.Pool
		s.hub = opts.Hub
		s.nudger = opts.Nudger
		s.tokenSecret = opts.TokenSecret
		s.cfg = opts.Config
		s.gh = opts.GitHub
	}
	return s
}

// Start runs the HTTP server until ctx is canceled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/webhook/github", s.githubWebhook)

	// Connect-repo flow (called server-to-server by the console backend, which
	// resolves the installation UUID → numeric id and proxies here).
	if s.gh != nil {
		mux.HandleFunc("GET /github/installations/{id}/repos", s.handleInstallationRepos)
		// account resolve — the backend's install-callback has the DB but no App
		// key, so it asks the agent who an installation belongs to.
		mux.HandleFunc("GET /github/installations/{id}/account", s.handleInstallationAccount)
		// list all App installations — the connect wizard binds an existing
		// (already-installed) org instead of forcing a reinstall.
		mux.HandleFunc("GET /github/app/installations", s.handleAppInstallations)
	}
	// Framework detection is best-effort here (no clone in the agent process — a
	// clone-based Nixpacks detect belongs in the build Job). Always 200 so the
	// wizard falls back to manual framework selection rather than erroring.
	mux.HandleFunc("GET /github/installations/{id}/detect", s.handleDetect)

	if s.hub != nil && s.tokenSecret != "" {
		mux.HandleFunc("/ws/build", s.handleBuildWS)
		log.Info().Msg("ws/build log endpoint enabled")
	}

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Info().Str("addr", s.addr).Msg("build-agent server listening")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// pushEvent is the subset of a GitHub push webhook payload we consume.
type pushEvent struct {
	Ref        string `json:"ref"` // refs/heads/<branch>
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	HeadCommit struct {
		Message string `json:"message"`
	} `json:"head_commit"`
}

// githubWebhook is the "nudge" trigger. It verifies the HMAC (per-repo secret
// when configured, else the global app secret), idempotently enqueues a build
// for every git_repos linked to the pushed repo+branch, then nudges the queue
// and returns 200 fast.
//
// TODO(wave-3): pull_request events (open/sync/close) for preview envs +
// fork-PR safety flag (head.repo != base → fork_unsafe, inject no secrets).
func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	if event != "push" {
		// pull_request and other events are accepted but not yet acted on.
		w.WriteHeader(http.StatusOK)
		return
	}

	var ev pushEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	branch := strings.TrimPrefix(ev.Ref, "refs/heads/")
	log.Info().Str("event", event).Str("repo", ev.Repository.FullName).Str("branch", branch).Msg("webhook received")

	enqueued := 0
	if s.pool != nil && ev.Repository.FullName != "" {
		repos, rerr := db.ResolveReposByFullName(r.Context(), s.pool, ev.Repository.FullName)
		if rerr != nil {
			log.Error().Err(rerr).Msg("webhook: resolve repos")
			http.Error(w, "resolve error", http.StatusInternalServerError)
			return
		}
		sig := r.Header.Get("X-Hub-Signature-256")
		for _, repo := range repos {
			if !s.verifyWebhook(repo.WebhookSecret, body, sig) {
				log.Warn().Str("repo", repo.RepoFullName).Msg("webhook: invalid signature")
				continue
			}
			if repo.ProductionBranch != branch || !repo.AutoDeploy {
				continue
			}
			if _, err := db.InsertBuildFromWebhook(r.Context(), s.pool,
				repo.ID, repo.EnvironmentID, repo.AppName, ev.After, ev.HeadCommit.Message, branch, "push"); err != nil {
				log.Error().Err(err).Str("repo", repo.RepoFullName).Msg("webhook: enqueue build")
				continue
			}
			enqueued++
		}
	}

	if enqueued > 0 && s.nudger != nil {
		go s.nudger.OnPush(context.Background())
	}

	w.WriteHeader(http.StatusOK)
}

// handleInstallationRepos lists the repos visible to a GitHub App installation.
// GET /github/installations/{id}/repos → {"repos": [...]}.
func (s *Server) handleInstallationRepos(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad installation id", http.StatusBadRequest)
		return
	}
	repos, err := s.gh.ListRepos(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Int64("installation", id).Msg("list installation repos")
		http.Error(w, "failed to list repositories", http.StatusBadGateway)
		return
	}
	if repos == nil {
		repos = []github.RemoteRepo{}
	}
	writeJSON(w, map[string]any{"repos": repos})
}

// handleInstallationAccount resolves the org/user behind an installation.
// GET /github/installations/{id}/account → {"installation_id","account_login","account_type"}.
func (s *Server) handleInstallationAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad installation id", http.StatusBadRequest)
		return
	}
	acct, err := s.gh.GetInstallation(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Int64("installation", id).Msg("resolve installation account")
		http.Error(w, "failed to resolve installation", http.StatusBadGateway)
		return
	}
	writeJSON(w, acct)
}

// handleAppInstallations lists every installation of the App.
// GET /github/app/installations → {"installations":[{installation_id,account_login,account_type}]}.
func (s *Server) handleAppInstallations(w http.ResponseWriter, r *http.Request) {
	insts, err := s.gh.ListInstallations(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("list app installations")
		http.Error(w, "failed to list installations", http.StatusBadGateway)
		return
	}
	if insts == nil {
		insts = []github.InstallationAccount{}
	}
	writeJSON(w, map[string]any{"installations": insts})
}

// frameworkDetection mirrors the backend/frontend FrameworkDetection shape.
type frameworkDetection struct {
	Framework      *string `json:"framework"`
	BuildCommand   *string `json:"build_command"`
	InstallCommand *string `json:"install_command"`
	OutputDir      *string `json:"output_dir"`
}

type githubContent struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

var githubHTTPClient = http.DefaultClient

type frameworkCandidate struct {
	detection frameworkDetection
	score     int
	depth     int
	path      string
}

// handleDetect returns a best-effort framework detection by inspecting the
// repository tree via the GitHub API. This stays intentionally lightweight: it
// gives the import wizard a useful default without claiming to replace the
// clone-and-build detection that still happens in the real build job.
func (s *Server) handleDetect(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad installation id", http.StatusBadRequest)
		return
	}
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repo == "" {
		http.Error(w, "repo is required", http.StatusBadRequest)
		return
	}
	rootDir := strings.TrimSpace(r.URL.Query().Get("root_dir"))
	if rootDir == "" || rootDir == "." {
		rootDir = ""
	}

	det, err := s.detectFramework(r.Context(), id, repo, rootDir)
	if err != nil {
		log.Warn().Err(err).Int64("installation", id).Str("repo", repo).Str("root_dir", rootDir).Msg("framework detect fallback")
		writeJSON(w, frameworkDetection{})
		return
	}
	writeJSON(w, det)
}

func (s *Server) detectFramework(ctx context.Context, installationID int64, repoFullName, rootDir string) (frameworkDetection, error) {
	token, err := s.gh.InstallToken(ctx, installationID)
	if err != nil {
		return frameworkDetection{}, err
	}

	parts := strings.SplitN(repoFullName, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return frameworkDetection{}, fmt.Errorf("bad repo full name: %q", repoFullName)
	}
	owner, repo := parts[0], parts[1]

	cands, err := s.scanFrameworkCandidates(ctx, token, owner, repo, rootDir, 0, 2)
	if err != nil {
		return frameworkDetection{}, err
	}
	if best, ok := bestFrameworkCandidate(cands); ok {
		return best.detection, nil
	}
	return frameworkDetection{}, nil
}

func (s *Server) scanFrameworkCandidates(ctx context.Context, token, owner, repo, dir string, depth, maxDepth int) ([]frameworkCandidate, error) {
	entries, err := githubListDir(ctx, token, owner, repo, dir)
	if err != nil {
		return nil, err
	}

	cands := detectCandidatesInDir(ctx, token, owner, repo, dir, depth, entries)
	if depth >= maxDepth {
		return cands, nil
	}

	for _, entry := range entries {
		if entry.Type != "dir" {
			continue
		}
		childDir := joinRepoPath(dir, entry.Name)
		childCands, err := s.scanFrameworkCandidates(ctx, token, owner, repo, childDir, depth+1, maxDepth)
		if err != nil {
			return nil, err
		}
		cands = append(cands, childCands...)
	}
	return cands, nil
}

func detectCandidatesInDir(ctx context.Context, token, owner, repo, dir string, depth int, entries []githubContent) []frameworkCandidate {
	byName := make(map[string]githubContent, len(entries))
	for _, entry := range entries {
		byName[strings.ToLower(entry.Name)] = entry
	}

	cands := make([]frameworkCandidate, 0, 8)

	if entry, ok := findFile(byName, "package.json"); ok {
		if cand, ok := detectNodeFramework(ctx, token, owner, repo, entry, byName, depth); ok {
			cands = append(cands, cand)
		}
	}
	if entry, ok := findFile(byName, "pyproject.toml"); ok {
		if cand, ok := detectPythonFramework(ctx, token, owner, repo, entry, byName, depth); ok {
			cands = append(cands, cand)
		}
	}
	if entry, ok := findFile(byName, "requirements.txt"); ok {
		if cand, ok := detectRequirementsFramework(ctx, token, owner, repo, entry, depth); ok {
			cands = append(cands, cand)
		}
	}
	if entry, ok := findFile(byName, "setup.py"); ok {
		if cand, ok := detectSetupPyFramework(ctx, token, owner, repo, entry, depth); ok {
			cands = append(cands, cand)
		}
	}
	if entry, ok := findFile(byName, "build.gradle"); ok {
		if cand, ok := detectGradleFramework(ctx, token, owner, repo, entry, byName, depth); ok {
			cands = append(cands, cand)
		}
	}
	if entry, ok := findFile(byName, "build.gradle.kts"); ok {
		if cand, ok := detectGradleKtsFramework(ctx, token, owner, repo, entry, byName, depth); ok {
			cands = append(cands, cand)
		}
	}
	if entry, ok := findFile(byName, "pom.xml"); ok {
		if cand, ok := detectMavenFramework(ctx, token, owner, repo, entry, depth); ok {
			cands = append(cands, cand)
		}
	}
	if cand, ok := detectConfigOnlyFramework(byName, depth); ok {
		cands = append(cands, cand)
	}
	cands = append(cands, detectCandidatesFromLockfiles(ctx, token, owner, repo, byName, depth)...)
	if entry, ok := findFile(byName, "go.mod"); ok {
		cands = append(cands, frameworkCandidate{
			detection: detectionWithStrings("go", "go build ./...", "", ""),
			score:     20,
			depth:     depth,
			path:      entry.Path,
		})
	}
	if entry, ok := findFile(byName, "Dockerfile"); ok {
		cands = append(cands, frameworkCandidate{
			detection: detectionWithStrings("dockerfile", "", "", ""),
			score:     5,
			depth:     depth,
			path:      entry.Path,
		})
	}
	return cands
}

func bestFrameworkCandidate(cands []frameworkCandidate) (frameworkCandidate, bool) {
	if len(cands) == 0 {
		return frameworkCandidate{}, false
	}
	best := cands[0]
	for _, cand := range cands[1:] {
		switch {
		case cand.score > best.score:
			best = cand
		case cand.score == best.score && cand.depth < best.depth:
			best = cand
		case cand.score == best.score && cand.depth == best.depth && len(cand.path) < len(best.path):
			best = cand
		}
	}
	if best.score == 0 {
		return frameworkCandidate{}, false
	}
	return best, true
}

func findFile(byName map[string]githubContent, name string) (githubContent, bool) {
	entry, ok := byName[strings.ToLower(name)]
	return entry, ok && entry.Type != "dir"
}

func joinRepoPath(base, child string) string {
	base = strings.Trim(base, "/")
	child = strings.Trim(child, "/")
	if base == "" {
		return child
	}
	if child == "" {
		return base
	}
	return base + "/" + child
}

func hasAnyFile(byName map[string]githubContent, names ...string) bool {
	for _, name := range names {
		if _, ok := findFile(byName, name); ok {
			return true
		}
	}
	return false
}

func detectConfigOnlyFramework(byName map[string]githubContent, depth int) (frameworkCandidate, bool) {
	switch {
	case hasAnyFile(byName, "next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs"):
		fw, build, install, output := "nextjs", "npm run build", "npm ci", ".next"
		return frameworkCandidate{detection: detectionWithStrings(fw, build, install, output), score: 120, depth: depth}, true
	case hasAnyFile(byName, "nuxt.config.js", "nuxt.config.mjs", "nuxt.config.ts", "nuxt.config.cjs"):
		fw, build, install, output := "nuxt", "npm run build", "npm ci", ".output"
		return frameworkCandidate{detection: detectionWithStrings(fw, build, install, output), score: 115, depth: depth}, true
	case hasAnyFile(byName, "svelte.config.js", "svelte.config.mjs", "svelte.config.ts", "svelte.config.cjs"):
		fw, build, install := "sveltekit", "npm run build", "npm ci"
		return frameworkCandidate{detection: detectionWithStrings(fw, build, install, ""), score: 112, depth: depth}, true
	case hasAnyFile(byName, "vite.config.js", "vite.config.mjs", "vite.config.ts", "vite.config.cjs"):
		fw, build, install, output := "vite", "npm run build", "npm ci", "dist"
		return frameworkCandidate{detection: detectionWithStrings(fw, build, install, output), score: 90, depth: depth}, true
	default:
		return frameworkCandidate{}, false
	}
}

func detectNodeFramework(ctx context.Context, token, owner, repo string, entry githubContent, byName map[string]githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	var pkg struct {
		Name            string            `json:"name"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Scripts         map[string]string `json:"scripts"`
		Workspaces      any               `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
		return frameworkCandidate{}, false
	}
	hasDep := func(name string) bool {
		_, ok := pkg.Dependencies[name]
		if ok {
			return true
		}
		_, ok = pkg.DevDependencies[name]
		return ok
	}
	hasAnyDep := func(names ...string) bool {
		for _, name := range names {
			if hasDep(name) {
				return true
			}
		}
		return false
	}
	hasScript := func(name string) bool {
		_, ok := pkg.Scripts[name]
		return ok
	}

	build := ""
	install := "npm ci"
	output := ""
	score := 70
	framework := "node"

	switch {
	case hasDep("next") || hasAnyFile(byName, "next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs"):
		framework = "nextjs"
		build = "npm run build"
		output = ".next"
		score = 125
	case hasAnyDep("nuxt", "@nuxt/devtools"), hasAnyFile(byName, "nuxt.config.js", "nuxt.config.mjs", "nuxt.config.ts", "nuxt.config.cjs"):
		framework = "nuxt"
		build = "npm run build"
		output = ".output"
		score = 120
	case hasAnyDep("@sveltejs/kit"), hasAnyFile(byName, "svelte.config.js", "svelte.config.mjs", "svelte.config.ts", "svelte.config.cjs"):
		framework = "sveltekit"
		build = "npm run build"
		score = 118
	case hasAnyDep("@remix-run/node", "@remix-run/react", "@remix-run/dev"):
		framework = "remix"
		build = "npm run build"
		output = "build"
		score = 110
	case hasAnyDep("react", "react-dom", "@vitejs/plugin-react") || hasAnyFile(byName, "vite.config.js", "vite.config.mjs", "vite.config.ts", "vite.config.cjs") && hasAnyDep("react", "react-dom", "@vitejs/plugin-react"):
		framework = "react"
		build = "npm run build"
		output = "dist"
		score = 105
	case hasDep("vite") || hasAnyFile(byName, "vite.config.js", "vite.config.mjs", "vite.config.ts", "vite.config.cjs"):
		framework = "vite"
		build = "npm run build"
		output = "dist"
		score = 95
	case hasAnyDep("react", "react-dom") && hasScript("build"):
		framework = "react"
		build = "npm run build"
		output = "dist"
		score = 100
	default:
		if hasScript("build") {
			build = "npm run build"
			score = 60
		}
	}

	if framework == "node" && !hasScript("build") && !hasAnyDep("react", "react-dom", "vite", "next", "nuxt", "@sveltejs/kit", "@remix-run/node", "@remix-run/react", "@vitejs/plugin-react") {
		return frameworkCandidate{}, false
	}

	det := detectionWithStrings(framework, build, install, output)
	if build == "" && hasScript("build") {
		det.BuildCommand = ptrString("npm run build")
	}
	return frameworkCandidate{detection: det, score: score, depth: depth, path: entry.Path}, true
}

func detectRequirementsFramework(ctx context.Context, token, owner, repo string, entry githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	return detectPythonContent(raw, entry.Path, depth, 100)
}

func detectSetupPyFramework(ctx context.Context, token, owner, repo string, entry githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	return detectPythonContent(raw, entry.Path, depth, 70)
}

func detectPythonFramework(ctx context.Context, token, owner, repo string, entry githubContent, byName map[string]githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	cand, ok := detectPythonContent(raw, entry.Path, depth, 105)
	if !ok {
		return frameworkCandidate{}, false
	}
	if hasAnyFile(byName, "requirements.txt", "poetry.lock") {
		if cand.detection.InstallCommand == nil || *cand.detection.InstallCommand == "" {
			cand.detection.InstallCommand = ptrString("pip install -r requirements.txt")
		}
	}
	return cand, true
}

func detectPythonContent(raw, path string, depth, score int) (frameworkCandidate, bool) {
	lower := strings.ToLower(raw)
	framework := ""
	switch {
	case strings.Contains(lower, "fastapi"):
		framework = "fastapi"
	case strings.Contains(lower, "django"):
		framework = "django"
	case strings.Contains(lower, "flask"):
		framework = "flask"
	case strings.Contains(lower, "uvicorn"):
		framework = "python"
	default:
		if framework == "" {
			return frameworkCandidate{}, false
		}
	}
	build := ""
	install := "pip install -r requirements.txt"
	return frameworkCandidate{
		detection: detectionWithStrings(framework, build, install, ""),
		score:     score,
		depth:     depth,
		path:      path,
	}, true
}

func detectGradleFramework(ctx context.Context, token, owner, repo string, entry githubContent, byName map[string]githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	return detectGradleContent(raw, entry.Path, byName, depth)
}

func detectGradleKtsFramework(ctx context.Context, token, owner, repo string, entry githubContent, byName map[string]githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	return detectGradleContent(raw, entry.Path, byName, depth)
}

func detectGradleContent(raw, path string, byName map[string]githubContent, depth int) (frameworkCandidate, bool) {
	lower := strings.ToLower(raw)
	score := 50
	build := "./gradlew build"
	install := "./gradlew dependencies"
	output := "build/libs"
	framework := "gradle"

	switch {
	case strings.Contains(lower, "org.springframework.boot") || strings.Contains(lower, "spring-boot") || strings.Contains(lower, "org.springframework"):
		framework = "spring"
		score = 115
	case strings.Contains(lower, "id('scala')") || strings.Contains(lower, `id "scala"`) || strings.Contains(lower, "scala-library") || strings.Contains(lower, "org.scala-lang") || strings.Contains(lower, "io.github.gitbucket"):
		framework = "scala"
		score = 120
		if strings.Contains(lower, "shadowjar") || hasAnyFile(byName, "gradle") {
			build = "./gradlew shadowJar"
		}
	default:
		if strings.Contains(lower, "shadowjar") {
			build = "./gradlew shadowJar"
		}
	}

	return frameworkCandidate{
		detection: detectionWithStrings(framework, build, install, output),
		score:     score,
		depth:     depth,
		path:      path,
	}, true
}

func detectMavenFramework(ctx context.Context, token, owner, repo string, entry githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	lower := strings.ToLower(raw)
	framework := "maven"
	score := 50
	build := "mvn package"
	install := "mvn dependency:go-offline"
	output := "target"
	if strings.Contains(lower, "org.springframework.boot") || strings.Contains(lower, "spring-boot") || strings.Contains(lower, "org.springframework") {
		framework = "spring"
		score = 112
	}
	return frameworkCandidate{
		detection: detectionWithStrings(framework, build, install, output),
		score:     score,
		depth:     depth,
		path:      entry.Path,
	}, true
}

func ptrString(s string) *string {
	return &s
}

func detectNodeFrameworkFromLockfile(raw string) (frameworkCandidate, bool) {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "\"next\""), strings.Contains(lower, "next@"):
		fw, build, install, output := "nextjs", "npm run build", "npm ci", ".next"
		return frameworkCandidate{detection: detectionWithStrings(fw, build, install, output), score: 100, depth: 0}, true
	case strings.Contains(lower, "\"nuxt\""), strings.Contains(lower, "nuxt@"):
		fw, build, install, output := "nuxt", "npm run build", "npm ci", ".output"
		return frameworkCandidate{detection: detectionWithStrings(fw, build, install, output), score: 98, depth: 0}, true
	case strings.Contains(lower, "@sveltejs/kit"):
		fw, build, install := "sveltekit", "npm run build", "npm ci"
		return frameworkCandidate{detection: detectionWithStrings(fw, build, install, ""), score: 96, depth: 0}, true
	case strings.Contains(lower, "@vitejs/plugin-react"), strings.Contains(lower, "react-dom"), strings.Contains(lower, "\"react\""):
		fw, build, install, output := "react", "npm run build", "npm ci", "dist"
		return frameworkCandidate{detection: detectionWithStrings(fw, build, install, output), score: 90, depth: 0}, true
	case strings.Contains(lower, "\"vite\""):
		fw, build, install, output := "vite", "npm run build", "npm ci", "dist"
		return frameworkCandidate{detection: detectionWithStrings(fw, build, install, output), score: 85, depth: 0}, true
	default:
		return frameworkCandidate{}, false
	}
}

func detectNodeFrameworkFromLockfileEntry(ctx context.Context, token, owner, repo string, entry githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	cand, ok := detectNodeFrameworkFromLockfile(raw)
	if !ok {
		return frameworkCandidate{}, false
	}
	cand.depth = depth
	cand.path = entry.Path
	return cand, true
}

func detectPythonLockfileFramework(raw string, path string, depth int) (frameworkCandidate, bool) {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "fastapi"):
		fw, install := "fastapi", "pip install -r requirements.txt"
		return frameworkCandidate{detection: detectionWithStrings(fw, "", install, ""), score: 100, depth: depth, path: path}, true
	case strings.Contains(lower, "django"):
		fw, install := "django", "pip install -r requirements.txt"
		return frameworkCandidate{detection: detectionWithStrings(fw, "", install, ""), score: 98, depth: depth, path: path}, true
	case strings.Contains(lower, "flask"):
		fw, install := "flask", "pip install -r requirements.txt"
		return frameworkCandidate{detection: detectionWithStrings(fw, "", install, ""), score: 96, depth: depth, path: path}, true
	default:
		return frameworkCandidate{}, false
	}
}

func detectPythonLockfileFrameworkEntry(ctx context.Context, token, owner, repo string, entry githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	return detectPythonLockfileFramework(raw, entry.Path, depth)
}

func detectCandidatesFromLockfiles(ctx context.Context, token, owner, repo string, byName map[string]githubContent, depth int) []frameworkCandidate {
	cands := []frameworkCandidate{}
	lockfiles := []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml"}
	for _, name := range lockfiles {
		if entry, ok := findFile(byName, name); ok {
			if cand, ok := detectNodeFrameworkFromLockfileEntry(ctx, token, owner, repo, entry, depth); ok {
				cands = append(cands, cand)
			}
		}
	}
	for _, name := range []string{"poetry.lock", "uv.lock"} {
		if entry, ok := findFile(byName, name); ok {
			if cand, ok := detectPythonLockfileFrameworkEntry(ctx, token, owner, repo, entry, depth); ok {
				cands = append(cands, cand)
			}
		}
	}
	return cands
}

func detectPythonFrameworkByLockfiles(ctx context.Context, token, owner, repo string, byName map[string]githubContent, depth int) []frameworkCandidate {
	return detectCandidatesFromLockfiles(ctx, token, owner, repo, byName, depth)
}

func detectFrameworkByConfigAndLockfiles(ctx context.Context, token, owner, repo string, dir string, depth int) ([]frameworkCandidate, error) {
	entries, err := githubListDir(ctx, token, owner, repo, dir)
	if err != nil {
		return nil, err
	}
	return append(detectCandidatesInDir(ctx, token, owner, repo, dir, depth, entries), detectCandidatesFromLockfiles(ctx, token, owner, repo, mapFromEntries(entries), depth)...), nil
}

func mapFromEntries(entries []githubContent) map[string]githubContent {
	out := make(map[string]githubContent, len(entries))
	for _, entry := range entries {
		out[strings.ToLower(entry.Name)] = entry
	}
	return out
}

func detectionWithStrings(framework, build, install, output string) frameworkDetection {
	d := frameworkDetection{}
	if framework != "" {
		d.Framework = &framework
	}
	if build != "" {
		d.BuildCommand = &build
	}
	if install != "" {
		d.InstallCommand = &install
	}
	if output != "" {
		d.OutputDir = &output
	}
	return d
}

func githubListDir(ctx context.Context, token, owner, repo, dir string) ([]githubContent, error) {
	var out []githubContent
	if err := githubAPI(ctx, token, owner, repo, dir, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func githubReadFile(ctx context.Context, token, owner, repo, filePath string) (string, error) {
	var out githubContent
	if err := githubAPI(ctx, token, owner, repo, filePath, &out); err != nil {
		return "", err
	}
	if out.Encoding != "base64" {
		return "", fmt.Errorf("unsupported github content encoding %q", out.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("decode github content: %w", err)
	}
	return string(decoded), nil
}

func githubAPI(ctx context.Context, token, owner, repo, repoPath string, dst any) error {
	clean := strings.Trim(strings.TrimSpace(repoPath), "/")
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents", url.PathEscape(owner), url.PathEscape(repo))
	if clean != "" {
		endpoint += "/" + path.Clean(clean)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("github contents %s: status %d", clean, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode github contents: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// verifyWebhook checks the HMAC against the per-repo secret when set, falling
// back to the global app webhook secret. Absent any configured secret, the
// signature is not enforced (dev convenience).
func (s *Server) verifyWebhook(repoSecret string, body []byte, sig string) bool {
	secret := repoSecret
	if secret == "" && s.cfg != nil {
		secret = s.cfg.GitHubWebhookSecret
	}
	if secret == "" {
		return true
	}
	return github.VerifySignature(secret, body, sig)
}

// Hub exposes the log hub so the runner can publish frames.
func (s *Server) Hub() *Hub { return s.hub }
