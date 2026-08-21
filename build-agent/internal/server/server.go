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
	"sync"
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
		mux.HandleFunc("GET /github/installations/{id}/branches", s.handleInstallationBranches)
		// account resolve — the backend's install-callback has the DB but no App
		// key, so it asks the agent who an installation belongs to.
		mux.HandleFunc("GET /github/installations/{id}/account", s.handleInstallationAccount)
		// list all App installations -- the connect wizard binds an existing
		// (already-installed) org instead of forcing a reinstall.
		mux.HandleFunc("GET /github/app/installations", s.handleAppInstallations)
		// exchange a user OAuth code → the installations that user can access.
		// The backend proxies here so the OAuth client secret stays in the agent.
		mux.HandleFunc("POST /github/oauth/exchange", s.handleOAuthExchange)
		mux.HandleFunc("GET /github/search/repos", s.handleSearchRepos)
	}
	// Framework detection is best-effort here (no clone in the agent process — a
	// clone-based Nixpacks detect belongs in the build Job). Always 200 so the
	// wizard falls back to manual framework selection rather than erroring.
	mux.HandleFunc("GET /github/installations/{id}/detect", s.handleDetect)
	// Token-based detection for repos connected by URL, no installation involved.
	mux.HandleFunc("POST /github/detect", s.handleDetectByToken)

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

// pullRequestEvent is the subset of a GitHub pull_request webhook payload
// build-agent still consumes now that previews are gone: the PR number and its
// repository, enough to find and tear down an environment a PR opened back when
// the feature existed.
//
// The head/base/label fields the creation path read are deliberately absent. A
// struct that cannot describe a fork PR or an opt-in label is a struct nobody
// can quietly restore a preview build from.
type pullRequestEvent struct {
	Action     string `json:"action"`
	Number     int    `json:"number"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// githubWebhook is the "nudge" trigger. It verifies the HMAC (per-repo secret
// when configured, else the global app secret), idempotently enqueues a build
// for every git_repos linked to the pushed repo+branch, then nudges the queue
// and returns 200 fast. pull_request events are routed to
// handlePullRequestWebhook for preview deployments; every other event is
// accepted but not acted on.
//
// A push that enqueues nothing is logged explicitly, with how many git_repos
// rows matched the pushed repo. That case used to be invisible: GitHub shows a
// green delivery, the console shows no build, and telling "no app is linked to
// this repo" apart from "the platform dropped it" meant reading git_repos by
// hand. It cost the owner an afternoon on keksmd/family-tree, whose only row
// had been rewritten to upload/<app> by an archive upload, so linked_apps was
// zero and the loop below never ran.
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
	if event == "installation" {
		s.handleInstallationEvent(r.Context(), body, r.Header.Get("X-Hub-Signature-256"))
		w.WriteHeader(http.StatusOK)
		return
	}
	if event == "pull_request" {
		s.handlePullRequestWebhook(w, r, body)
		return
	}
	if event != "push" {
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

	enqueued, linked := 0, 0
	if s.pool != nil && ev.Repository.FullName != "" {
		repos, rerr := db.ResolveReposByFullName(r.Context(), s.pool, ev.Repository.FullName)
		if rerr != nil {
			log.Error().Err(rerr).Msg("webhook: resolve repos")
			http.Error(w, "resolve error", http.StatusInternalServerError)
			return
		}
		linked = len(repos)
		sig := r.Header.Get("X-Hub-Signature-256")
		for _, repo := range repos {
			if !s.verifyWebhook(repo.WebhookSecret, body, sig) {
				log.Warn().Str("repo", repo.RepoFullName).Msg("webhook: invalid signature")
				continue
			}
			if repo.ProductionBranch != branch || !repo.AutoDeploy {
				log.Info().Str("repo", repo.RepoFullName).Str("app", repo.AppName).
					Str("branch", branch).Str("production_branch", repo.ProductionBranch).
					Bool("auto_deploy", repo.AutoDeploy).
					Msg("webhook: push ignored, not this app's deploy branch")
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

	if enqueued == 0 {
		log.Info().Str("repo", ev.Repository.FullName).Str("branch", branch).
			Int("linked_apps", linked).Msg("webhook: push enqueued no build")
	}

	if enqueued > 0 && s.nudger != nil {
		go s.nudger.OnPush(context.Background())
	}

	w.WriteHeader(http.StatusOK)
}

// handlePullRequestWebhook is teardown-only. Preview environments are no longer
// a product feature: a pull request never creates one, so opened, reopened,
// synchronize and every label delivery are accepted and dropped on the floor.
//
// The feature died because of what it cost the people who never asked for it.
// One PR nobody merged kept a second full copy of the app running for the whole
// TTL, a third one after the next PR, and the platform ate the hardware while
// the customer's own plan quota counted the copies as apps they had deployed.
// Opt-in-by-label narrowed that to whoever added the label; removing the
// creation path closes it.
//
// "closed" still runs, because environments opened before the removal are still
// out there. Closing the PR that created one must keep tearing it down rather
// than leaving it to sit until a reaper notices. A PR with no preview
// environment is a silent 200 - idempotent, like the push path.
func (s *Server) handlePullRequestWebhook(w http.ResponseWriter, r *http.Request, body []byte) {
	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if ev.Action != "closed" || s.pool == nil || ev.Repository.FullName == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	ctx := r.Context()
	repos, rerr := db.ResolveReposByFullName(ctx, s.pool, ev.Repository.FullName)
	if rerr != nil {
		log.Error().Err(rerr).Msg("pull_request webhook: resolve repos")
		http.Error(w, "resolve error", http.StatusInternalServerError)
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	for _, repo := range repos {
		if !s.verifyWebhook(repo.WebhookSecret, body, sig) {
			log.Warn().Str("repo", repo.RepoFullName).Msg("pull_request webhook: invalid signature")
			continue
		}
		s.closePreviewEnv(ctx, repo, ev.Number)
	}
	w.WriteHeader(http.StatusOK)
}

// closePreviewEnv tears down a PR's preview environment by enqueueing the
// DeletePreviewEnv operation. A PR with no preview environment is a silent
// no-op so repeated "closed" deliveries stay idempotent.
func (s *Server) closePreviewEnv(ctx context.Context, repo *db.Repo, prNumber int) {
	previewEnv, err := db.FindPreviewEnvByPR(ctx, s.pool, repo.ID, prNumber)
	if err != nil {
		log.Error().Err(err).Str("repo", repo.RepoFullName).Int("pr", prNumber).Msg("pull_request webhook: find preview env on close")
		return
	}
	if previewEnv == nil {
		return
	}
	if _, err := db.InsertDeletePreviewEnvOp(ctx, s.pool, db.SystemUserID, repo.ProjectID, previewEnv.ID, previewEnv.Namespace); err != nil {
		log.Error().Err(err).Str("repo", repo.RepoFullName).Int("pr", prNumber).Msg("pull_request webhook: enqueue DeletePreviewEnv")
	}
}

// handleInstallationEvent prunes stale installation rows when GitHub reports the
// App was uninstalled (action=deleted). Verified against the global App webhook
// secret; other actions are ignored. Best-effort: errors are logged, not fatal.
func (s *Server) handleInstallationEvent(ctx context.Context, body []byte, sig string) {
	if s.pool == nil {
		return
	}
	if !s.verifyWebhook(s.cfg.GitHubWebhookSecret, body, sig) {
		log.Warn().Msg("installation webhook: invalid signature")
		return
	}
	var ev struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		log.Error().Err(err).Msg("installation webhook: bad payload")
		return
	}
	if ev.Action != "deleted" || ev.Installation.ID == 0 {
		return
	}
	n, err := db.DeleteInstallationsByNumericID(ctx, s.pool, ev.Installation.ID)
	if err != nil {
		log.Error().Err(err).Int64("installation", ev.Installation.ID).Msg("installation webhook: prune")
		return
	}
	log.Info().Int64("installation", ev.Installation.ID).Int64("rows", n).Msg("installation deleted, pruned rows")
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

// handleInstallationBranches lists the branches of one repo visible to a GitHub
// App installation. GET /github/installations/{id}/branches?repo=owner/name →
// {"branches": [...]}.
func (s *Server) handleInstallationBranches(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad installation id", http.StatusBadRequest)
		return
	}
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		http.Error(w, "missing repo query param", http.StatusBadRequest)
		return
	}
	branches, err := s.gh.ListBranches(r.Context(), id, repo)
	if err != nil {
		log.Error().Err(err).Int64("installation", id).Str("repo", repo).Msg("list installation branches")
		http.Error(w, "failed to list branches", http.StatusBadGateway)
		return
	}
	if branches == nil {
		branches = []github.RemoteBranch{}
	}
	writeJSON(w, map[string]any{"branches": branches})
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

// handleSearchRepos searches public GitHub repositories by free text.
// GET /github/search/repos?q=n8n&limit=8 → {"repositories":[...]}.
//
// It lives in the agent because the agent is where the App key lives, and the
// search endpoint's rate limit triples with a token. The backend caches the
// answer before it ever gets here — this handler is deliberately not the place
// that thinks about budgets, it is the place that has the credential.
func (s *Server) handleSearchRepos(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		http.Error(w, "q is required", http.StatusBadRequest)
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
			return
		}
		limit = n
	}

	hits, err := s.gh.SearchRepos(r.Context(), q, limit)
	if err != nil {
		log.Warn().Err(err).Str("q", q).Msg("github repo search failed")
		http.Error(w, "failed to search repositories", http.StatusBadGateway)
		return
	}
	if hits == nil {
		hits = []github.SearchHit{}
	}
	writeJSON(w, map[string]any{"repositories": hits})
}

// handleOAuthExchange swaps a user OAuth code for the installations that user can
// access. POST /github/oauth/exchange {"code":"..."} → {"login","installations":[...]}.
// The OAuth client secret stays in the agent; the backend never sees it.
func (s *Server) handleOAuthExchange(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil || s.cfg.GitHubClientID == "" || s.cfg.GitHubClientSecret == "" {
		http.Error(w, "github oauth not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || req.Code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	res, err := github.ExchangeUserCode(r.Context(), s.cfg.GitHubClientID, s.cfg.GitHubClientSecret, req.Code)
	if err != nil {
		log.Error().Err(err).Msg("oauth exchange")
		http.Error(w, "oauth exchange failed", http.StatusBadGateway)
		return
	}
	if res.Installations == nil {
		res.Installations = []github.InstallationAccount{}
	}
	writeJSON(w, res)
}

// frameworkDetection mirrors the backend/frontend FrameworkDetection shape.
type frameworkDetection struct {
	Framework      *string  `json:"framework"`
	PackageManager *string  `json:"package_manager"`
	BuildCommand   *string  `json:"build_command"`
	InstallCommand *string  `json:"install_command"`
	StartCommand   *string  `json:"start_command"`
	OutputDir      *string  `json:"output_dir"`
	Port           *int     `json:"port"`
	CIWorkflows    []string `json:"ci_workflows,omitempty"`
}

type githubContent struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

var githubHTTPClient = http.DefaultClient

// githubAPISem bounds concurrent GitHub contents calls so the parallel framework
// scan cannot open an unbounded number of sockets or trip secondary rate limits.
// It is held only around a single request, never across recursion, so it cannot
// deadlock the fan-out.
var githubAPISem = make(chan struct{}, 8)

type packageManagerSpec struct {
	name    string
	version string
}

func parsePackageManager(raw string) packageManagerSpec {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return packageManagerSpec{}
	}
	name := raw
	version := ""
	if cut := strings.IndexByte(raw, '@'); cut > 0 {
		name = raw[:cut]
		version = raw[cut+1:]
	}
	if name == "" {
		return packageManagerSpec{}
	}
	return packageManagerSpec{name: name, version: version}
}

func packageManagerFromFiles(byName map[string]githubContent) packageManagerSpec {
	switch {
	case hasAnyFile(byName, "bun.lockb", "bun.lock"):
		return packageManagerSpec{name: "bun"}
	case hasAnyFile(byName, "pnpm-lock.yaml", "pnpm-workspace.yaml"):
		return packageManagerSpec{name: "pnpm"}
	case hasAnyFile(byName, "yarn.lock", ".yarnrc.yml"):
		return packageManagerSpec{name: "yarn"}
	case hasAnyFile(byName, "package-lock.json", "npm-shrinkwrap.json"):
		return packageManagerSpec{name: "npm"}
	default:
		return packageManagerSpec{}
	}
}

func nodePackageManagerHint(byName map[string]githubContent, inherited packageManagerSpec) packageManagerSpec {
	switch {
	case hasAnyFile(byName, "bun.lockb", "bun.lock"):
		return packageManagerSpec{name: "bun"}
	case hasAnyFile(byName, "pnpm-lock.yaml", "pnpm-workspace.yaml"):
		return packageManagerSpec{name: "pnpm"}
	case hasAnyFile(byName, "yarn.lock", ".yarnrc.yml"):
		return packageManagerSpec{name: "yarn"}
	case hasAnyFile(byName, "package-lock.json", "npm-shrinkwrap.json"):
		return packageManagerSpec{name: "npm"}
	default:
		if _, ok := findFile(byName, "package.json"); ok {
			if !inherited.empty() {
				return inherited
			}
			return packageManagerSpec{name: "npm"}
		}
		return inherited
	}
}

func nodePackageManagerFromPackageJSON(ctx context.Context, token, owner, repo, filePath string) (packageManagerSpec, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, filePath)
	if err != nil {
		return packageManagerSpec{}, false
	}
	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
		return packageManagerSpec{}, false
	}
	pm := parsePackageManager(pkg.PackageManager)
	if pm.empty() {
		return packageManagerSpec{}, false
	}
	return pm, true
}

func (pm packageManagerSpec) empty() bool {
	return pm.name == ""
}

func (pm packageManagerSpec) label() string {
	if pm.name == "" {
		return ""
	}
	if pm.version != "" {
		return pm.name + "@" + pm.version
	}
	return pm.name
}

func (pm packageManagerSpec) run(script string) string {
	if pm.name == "" {
		pm.name = "npm"
	}
	switch pm.name {
	case "npm":
		return "npm run " + script
	case "pnpm":
		return "pnpm run " + script
	case "yarn":
		return "yarn run " + script
	case "bun":
		return "bun run " + script
	default:
		return "npm run " + script
	}
}

func (pm packageManagerSpec) exec(binary string, args ...string) string {
	if pm.name == "" {
		pm.name = "npm"
	}
	switch pm.name {
	case "npm":
		return "npm exec -- " + strings.Join(append([]string{binary}, args...), " ")
	case "pnpm":
		return "pnpm exec " + strings.Join(append([]string{binary}, args...), " ")
	case "yarn":
		return "yarn exec " + strings.Join(append([]string{binary}, args...), " ")
	case "bun":
		return "bunx " + strings.Join(append([]string{binary}, args...), " ")
	default:
		return "npm exec -- " + strings.Join(append([]string{binary}, args...), " ")
	}
}

func (pm packageManagerSpec) install(hasLock bool) string {
	if pm.name == "" {
		pm.name = "npm"
	}
	switch pm.name {
	case "npm":
		if hasLock {
			return "npm ci"
		}
		return "npm install"
	case "pnpm":
		if hasLock {
			return "pnpm install --frozen-lockfile"
		}
		return "pnpm install"
	case "yarn":
		if strings.HasPrefix(pm.version, "1.") || pm.version == "" {
			if hasLock {
				return "yarn install --frozen-lockfile"
			}
			return "yarn install"
		}
		if hasLock {
			return "yarn install --immutable"
		}
		return "yarn install"
	case "bun":
		if hasLock {
			return "bun install --frozen-lockfile"
		}
		return "bun install"
	default:
		if hasLock {
			return "npm ci"
		}
		return "npm install"
	}
}

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
		log.Warn().Err(err).Int64("installation", id).Str("repo", repo).Str("root_dir", rootDir).Msg("framework detect failed")
		http.Error(w, "framework detection failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, det)
}

// detectFramework resolves an installation token and inspects the repo tree.
// installationID 0 means "no installation" (the one-click public-repo deploy
// path): detect anonymously, exactly like the anonymous clone the build job
// already performs for such repos. githubAPI omits an empty Authorization
// header, so this hits GitHub's unauthenticated API — public repos only, and
// subject to the lower anonymous rate limit.
func (s *Server) detectFramework(ctx context.Context, installationID int64, repoFullName, rootDir string) (frameworkDetection, error) {
	token := ""
	if installationID != 0 {
		var err error
		token, err = s.gh.InstallToken(ctx, installationID)
		if err != nil {
			return frameworkDetection{}, err
		}
	}
	return detectWithToken(ctx, token, repoFullName, rootDir)
}

// detectByTokenRequest is the body for handleDetectByToken: a repo plus a
// caller-supplied token instead of a GitHub App installation id.
type detectByTokenRequest struct {
	RepoFullName string `json:"repo_full_name"`
	RootDir      string `json:"root_dir"`
	Token        string `json:"token"`
}

// handleDetectByToken mirrors handleDetect but runs detection with a
// caller-supplied personal access token rather than a GitHub App installation
// — the connect-by-URL flow, where the console backend never obtained an
// installation for the repo. Token travels in the POST body, not a query
// string, so it does not end up in access logs. Does not depend on s.gh: no
// App key is needed when the caller brings its own token, so this is
// registered unconditionally.
func (s *Server) handleDetectByToken(w http.ResponseWriter, r *http.Request) {
	var req detectByTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	repo := strings.TrimSpace(req.RepoFullName)
	if repo == "" {
		http.Error(w, "repo_full_name is required", http.StatusBadRequest)
		return
	}
	rootDir := strings.TrimSpace(req.RootDir)
	if rootDir == "." {
		rootDir = ""
	}
	det, err := detectWithToken(r.Context(), req.Token, repo, rootDir)
	if err != nil {
		log.Warn().Err(err).Str("repo", repo).Str("root_dir", rootDir).Msg("token framework detect failed")
		http.Error(w, "framework detection failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, det)
}

// detectWithToken is the token-based detection core shared by the HTTP import
// wizard (handleDetect, handleDetectByToken) and the build-time path
// (DetectForBuild). It assumes rootDir is already normalized ("" for repo
// root).
func detectWithToken(ctx context.Context, token, repoFullName, rootDir string) (frameworkDetection, error) {
	parts := strings.SplitN(repoFullName, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return frameworkDetection{}, fmt.Errorf("bad repo full name: %q", repoFullName)
	}
	owner, repo := parts[0], parts[1]

	ci := listCIWorkflows(ctx, token, owner, repo)

	cands, err := scanFrameworkCandidates(ctx, token, owner, repo, rootDir, 0, 2, packageManagerSpec{})
	if err != nil {
		return frameworkDetection{}, err
	}
	if best, ok := bestFrameworkCandidate(cands); ok {
		det := best.detection
		if port, ok := resolveExplicitPort(ctx, token, owner, repo, rootDir, derefString(det.Framework)); ok {
			det.Port = ptrInt(port)
		}
		det.CIWorkflows = ci
		return det, nil
	}
	return frameworkDetection{CIWorkflows: ci}, nil
}

// listCIWorkflows returns the GitHub Actions workflow file names under
// .github/workflows for the repo. Workflows always live at the repo root, so
// this ignores a monorepo rootDir. A missing directory yields nil (not an
// error): most repos simply have no workflows, and detection must not fail on
// that. The wizard uses a non-empty result to offer a deploy-from-CI path.
func listCIWorkflows(ctx context.Context, token, owner, repo string) []string {
	entries, err := githubListDir(ctx, token, owner, repo, ".github/workflows")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Type != "file" {
			continue
		}
		if strings.HasSuffix(e.Name, ".yml") || strings.HasSuffix(e.Name, ".yaml") {
			out = append(out, e.Name)
		}
	}
	return out
}

// resolveExplicitPort finds the port the app actually binds by reading the
// repo's own sources, in order of authority, so the static per-framework
// default is only ever a last-resort fallback (returns false here, leaving the
// caller's default in place):
//
//  1. Dockerfile EXPOSE  -- the deploy contract when the repo ships a Dockerfile.
//  2. framework config   -- Spring server.port, Python uvicorn/flask/gunicorn.
//  3. .env PORT          -- the conventional Node/Twelve-Factor override.
func resolveExplicitPort(ctx context.Context, token, owner, repo, rootDir, framework string) (int, bool) {
	if p, ok := dockerfileExposePort(ctx, token, owner, repo, rootDir); ok {
		return p, true
	}
	switch strings.ToLower(framework) {
	case "spring-maven", "spring-gradle", "maven", "gradle":
		if p, ok := springServerPort(ctx, token, owner, repo, rootDir); ok {
			return p, true
		}
	case "fastapi", "django", "flask", "python":
		if p, ok := pythonAppPort(ctx, token, owner, repo, rootDir); ok {
			return p, true
		}
	}
	if p, ok := envFilePort(ctx, token, owner, repo, rootDir); ok {
		return p, true
	}
	return 0, false
}

func tryReadFile(ctx context.Context, token, owner, repo, filePath string) (string, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, filePath)
	if err != nil {
		return "", false
	}
	return raw, true
}

// springServerPort reads server.port from the conventional Spring config
// locations (repo root and src/main/resources), preferring the first hit.
func springServerPort(ctx context.Context, token, owner, repo, rootDir string) (int, bool) {
	names := []string{"application.properties", "application.yml", "application.yaml"}
	for _, dir := range []string{rootDir, joinRepoPath(rootDir, "src/main/resources")} {
		for _, name := range names {
			raw, ok := tryReadFile(ctx, token, owner, repo, joinRepoPath(dir, name))
			if !ok {
				continue
			}
			isYAML := strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
			if p, ok := parseSpringServerPort(raw, isYAML); ok {
				return p, true
			}
		}
	}
	return 0, false
}

// parseSpringServerPort handles the flat "server.port=8081" form (properties and
// flattened yaml) plus the nested yaml block form.
func parseSpringServerPort(content string, isYAML bool) (int, bool) {
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if p, ok := portAfterKeyword(t, "server.port"); ok {
			return p, true
		}
	}
	if isYAML {
		return yamlNestedServerPort(content)
	}
	return 0, false
}

func yamlNestedServerPort(content string) (int, bool) {
	inServer := false
	serverIndent := -1
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if inServer {
			if indent <= serverIndent {
				inServer = false
			} else if strings.HasPrefix(t, "port:") {
				if p, ok := portAfterSeparators(t[len("port"):]); ok {
					return p, true
				}
			}
		}
		if !inServer && strings.HasPrefix(t, "server:") {
			inServer = true
			serverIndent = indent
		}
	}
	return 0, false
}

// pythonAppPort scans the usual entrypoint modules for an explicit bind port
// (uvicorn.run(port=), --port, app.run(port=), gunicorn/-b host:port,
// manage.py runserver host:port).
func pythonAppPort(ctx context.Context, token, owner, repo, rootDir string) (int, bool) {
	for _, name := range []string{"main.py", "app.py", "asgi.py", "server.py", "run.py", "__main__.py", "wsgi.py", "manage.py"} {
		raw, ok := tryReadFile(ctx, token, owner, repo, joinRepoPath(rootDir, name))
		if !ok {
			continue
		}
		if p, ok := parsePythonPort(raw); ok {
			return p, true
		}
	}
	return 0, false
}

func parsePythonPort(content string) (int, bool) {
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if p, ok := portAfterKeyword(t, "port"); ok {
			return p, true
		}
		for _, kw := range []string{"--bind", "-b ", "runserver"} {
			if idx := strings.Index(t, kw); idx >= 0 {
				if p, ok := firstColonPort(t[idx+len(kw):]); ok {
					return p, true
				}
			}
		}
	}
	return 0, false
}

// envFilePort reads a PORT assignment from a root .env file.
func envFilePort(ctx context.Context, token, owner, repo, rootDir string) (int, bool) {
	raw, ok := tryReadFile(ctx, token, owner, repo, joinRepoPath(rootDir, ".env"))
	if !ok {
		return 0, false
	}
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		t = strings.TrimPrefix(t, "export ")
		if strings.HasPrefix(t, "PORT=") || strings.HasPrefix(t, "PORT ") {
			if p, ok := portAfterSeparators(t[len("PORT"):]); ok {
				return p, true
			}
		}
	}
	return 0, false
}

// portAfterKeyword finds keyword (not preceded by a word byte, so "report" does
// not match "port") and reads the port that follows past any separators.
func portAfterKeyword(line, keyword string) (int, bool) {
	from := 0
	for {
		idx := strings.Index(line[from:], keyword)
		if idx < 0 {
			return 0, false
		}
		idx += from
		if idx == 0 || !isWordByte(line[idx-1]) {
			if p, ok := portAfterSeparators(line[idx+len(keyword):]); ok {
				return p, true
			}
		}
		from = idx + len(keyword)
	}
}

// firstColonPort reads the port from the first ':'-prefixed digit run in s (the
// host:port form of a gunicorn --bind / manage.py runserver argument).
func firstColonPort(s string) (int, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			if p, ok := portAfterSeparators(s[i:]); ok {
				return p, true
			}
		}
	}
	return 0, false
}

// portAfterSeparators skips separator characters then reads a valid port
// (1..65535) from the immediately following digit run.
func portAfterSeparators(s string) (int, bool) {
	i := 0
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '=', ':', '"', '\'', '(':
			i++
			continue
		}
		break
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i {
		return 0, false
	}
	n, err := strconv.Atoi(s[i:j])
	if err != nil || n <= 0 || n > 65535 {
		return 0, false
	}
	return n, true
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// dockerfileExposePort returns the first port declared by an EXPOSE directive in
// the root Dockerfile, if the repo carries one.
//
// When a repo ships its own Dockerfile that Dockerfile is what the build runs,
// so its EXPOSE is the authoritative listen port and must win over the static
// per-framework default guess. Without this the wizard mislabels the port for
// any app whose Dockerfile exposes something other than the framework default
// (e.g. a Streamlit image with EXPOSE 8501 was being reported as 8080).
func dockerfileExposePort(ctx context.Context, token, owner, repo, rootDir string) (int, bool) {
	entries, err := githubListDir(ctx, token, owner, repo, rootDir)
	if err != nil {
		return 0, false
	}
	entry, ok := findFile(mapFromEntries(entries), "Dockerfile")
	if !ok {
		return 0, false
	}
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return 0, false
	}
	return parseDockerfileExpose(raw)
}

// parseDockerfileExpose extracts the first numeric port from the first EXPOSE
// directive in a Dockerfile. Protocol suffixes ("8080/tcp") are stripped and
// non-literal ports ("EXPOSE ${PORT}") are ignored so callers keep the framework
// default rather than a bogus value.
func parseDockerfileExpose(dockerfile string) (int, bool) {
	for _, line := range strings.Split(dockerfile, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "EXPOSE") {
			continue
		}
		for _, tok := range fields[1:] {
			if slash := strings.IndexByte(tok, '/'); slash >= 0 {
				tok = tok[:slash]
			}
			if port, err := strconv.Atoi(tok); err == nil && port > 0 && port <= 65535 {
				return port, true
			}
		}
	}
	return 0, false
}

// BuildDetection is the dereferenced, build-time view of frameworkDetection. The
// worker passes these to the Jenkins job as plain parameters so the pipeline can
// template a Dockerfile for repos that carry none.
type BuildDetection struct {
	Framework      string
	PackageManager string
	InstallCommand string
	BuildCommand   string
	StartCommand   string
	OutputDir      string
	Port           int
}

// DetectForBuild runs framework detection with an installation token the runner
// already holds (no HTTP round-trip). Best-effort: callers treat an error as "no
// detection" and let the pipeline fall back to a repo Dockerfile.
func DetectForBuild(ctx context.Context, token, repoFullName, rootDir string) (BuildDetection, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "." {
		rootDir = ""
	}
	det, err := detectWithToken(ctx, token, repoFullName, rootDir)
	if err != nil {
		return BuildDetection{}, err
	}
	return BuildDetection{
		Framework:      derefString(det.Framework),
		PackageManager: derefString(det.PackageManager),
		InstallCommand: derefString(det.InstallCommand),
		BuildCommand:   derefString(det.BuildCommand),
		StartCommand:   derefString(det.StartCommand),
		OutputDir:      derefString(det.OutputDir),
		Port:           derefInt(det.Port),
	}, nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func scanFrameworkCandidates(ctx context.Context, token, owner, repo, dir string, depth, maxDepth int, inheritedNodePM packageManagerSpec) ([]frameworkCandidate, error) {
	entries, err := githubListDir(ctx, token, owner, repo, dir)
	if err != nil {
		return nil, err
	}

	byName := mapFromEntries(entries)
	localNodePM := nodePackageManagerHint(byName, inheritedNodePM)
	if entry, ok := findFile(byName, "package.json"); ok {
		if pm, ok := nodePackageManagerFromPackageJSON(ctx, token, owner, repo, entry.Path); ok {
			localNodePM = pm
		}
	}

	cands := detectCandidatesInDir(ctx, token, owner, repo, dir, depth, entries, localNodePM)
	if depth >= maxDepth || len(cands) > 0 {
		return cands, nil
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, entry := range entries {
		if entry.Type != "dir" || skipScanDir(entry.Name) {
			continue
		}
		childDir := joinRepoPath(dir, entry.Name)
		wg.Add(1)
		go func(childDir string) {
			defer wg.Done()
			childCands, err := scanFrameworkCandidates(ctx, token, owner, repo, childDir, depth+1, maxDepth, localNodePM)
			if err != nil {
				log.Warn().Err(err).Str("repo", owner+"/"+repo).Str("dir", childDir).Msg("skip subtree during framework detect: subdirectory listing failed, root candidates preserved")
				return
			}
			mu.Lock()
			cands = append(cands, childCands...)
			mu.Unlock()
		}(childDir)
	}
	wg.Wait()
	return cands, nil
}

// skipScanDir excludes directories that never hold an application's framework
// root, keeping the recursive scan (and its GitHub API round-trips) small enough
// to finish inside the import wizard's request timeout. Hidden directories and
// common dependency/build/output directories are skipped.
func skipScanDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "dist", "build", "out", "target",
		"coverage", "__pycache__", "venv", "env", "testdata", "tmp", "bin", "obj":
		return true
	}
	return false
}

func detectCandidatesInDir(ctx context.Context, token, owner, repo, dir string, depth int, entries []githubContent, inheritedNodePM packageManagerSpec) []frameworkCandidate {
	byName := make(map[string]githubContent, len(entries))
	for _, entry := range entries {
		byName[strings.ToLower(entry.Name)] = entry
	}
	pmHint := nodePackageManagerHint(byName, inheritedNodePM)

	cands := make([]frameworkCandidate, 0, 8)

	if entry, ok := findFile(byName, "package.json"); ok {
		if cand, ok := detectNodeFramework(ctx, token, owner, repo, entry, byName, pmHint, depth); ok {
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
	if entry, ok := findFile(byName, "build.sbt"); ok {
		if cand, ok := detectSbtFramework(ctx, token, owner, repo, entry, depth); ok {
			cands = append(cands, cand)
		}
	}
	if entry, ok := findFile(byName, "pom.xml"); ok {
		if cand, ok := detectMavenFramework(ctx, token, owner, repo, entry, depth); ok {
			cands = append(cands, cand)
		}
	}
	if cand, ok := detectConfigOnlyFramework(byName, pmHint, depth); ok {
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
	if entry, ok := barePythonSource(entries); ok {
		cands = append(cands, frameworkCandidate{
			detection: detectionWithStrings("python", "", "", ""),
			score:     2,
			depth:     depth,
			path:      entry.Path,
		})
	}
	if entry, ok := findFile(byName, "index.html"); ok {
		cands = append(cands, frameworkCandidate{
			detection: detectionWithStrings("static", "", "", "."),
			score:     3,
			depth:     depth,
			path:      entry.Path,
		})
	}
	return cands
}

// barePythonSource reports a .py file sitting directly in this directory,
// which is the shape of a script-style Python project: a few modules, an
// entrypoint, no packaging metadata at all.
//
// Without this rule such a repo detects nothing, the build-agent hands Jenkins
// an empty detected_framework, and dadaBuildPipeline aborts with
// "framework <empty> has no template and repo ships no Dockerfile" — even
// though its python
// branch already builds a manifest-less repo (its install step ends in "no
// python manifest - skipping install" and its start step falls back to
// main.py, bot.py, app.py, then any *.py). The archive path learned the same
// rule in sourcedetect.rootLevelPythonSources; this is its git-side twin.
//
// The score is deliberately the lowest of any python rule, so a real manifest
// anywhere in the tree always wins and a stray helper script never turns a
// Node or Go repo into a Python build.
func barePythonSource(entries []githubContent) (githubContent, bool) {
	for _, entry := range entries {
		if entry.Type != "file" {
			continue
		}
		if strings.HasSuffix(strings.ToLower(entry.Name), ".py") {
			return entry, true
		}
	}
	return githubContent{}, false
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

func detectConfigOnlyFramework(byName map[string]githubContent, pmHint packageManagerSpec, depth int) (frameworkCandidate, bool) {
	pm := pmHint
	if pm.empty() {
		pm = packageManagerFromFiles(byName)
	}
	if pm.empty() {
		pm = packageManagerSpec{name: "npm"}
	}

	switch {
	case hasAnyFile(byName, "next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs"):
		det := detectionWithStrings("nextjs", pm.exec("next", "build"), pm.install(hasAnyFile(byName, "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb")), ".next")
		det.PackageManager = ptrString(pm.label())
		det.StartCommand = ptrString(pm.exec("next", "start"))
		return frameworkCandidate{detection: det, score: 120, depth: depth}, true
	case hasAnyFile(byName, "nuxt.config.js", "nuxt.config.mjs", "nuxt.config.ts", "nuxt.config.cjs"):
		det := detectionWithStrings("nuxt", pm.exec("nuxi", "build"), pm.install(hasAnyFile(byName, "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb")), ".output")
		det.PackageManager = ptrString(pm.label())
		det.StartCommand = ptrString(pm.exec("nuxi", "preview"))
		return frameworkCandidate{detection: det, score: 115, depth: depth}, true
	case hasAnyFile(byName, "svelte.config.js", "svelte.config.mjs", "svelte.config.ts", "svelte.config.cjs"):
		det := detectionWithStrings("sveltekit", pm.exec("svelte-kit", "build"), pm.install(hasAnyFile(byName, "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb")), "build")
		det.PackageManager = ptrString(pm.label())
		det.StartCommand = ptrString(pm.exec("vite", "preview"))
		return frameworkCandidate{detection: det, score: 112, depth: depth}, true
	case hasAnyFile(byName, "vite.config.js", "vite.config.mjs", "vite.config.ts", "vite.config.cjs"):
		det := detectionWithStrings("vite", pm.exec("vite", "build"), pm.install(hasAnyFile(byName, "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb")), "dist")
		det.PackageManager = ptrString(pm.label())
		det.StartCommand = ptrString(pm.exec("vite", "preview"))
		return frameworkCandidate{detection: det, score: 90, depth: depth}, true
	default:
		return frameworkCandidate{}, false
	}
}

func detectNodeFramework(ctx context.Context, token, owner, repo string, entry githubContent, byName map[string]githubContent, pmHint packageManagerSpec, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	var pkg struct {
		PackageManager  string            `json:"packageManager"`
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

	pm := parsePackageManager(pkg.PackageManager)
	if pm.empty() {
		pm = pmHint
	}
	if pm.empty() {
		pm = packageManagerFromFiles(byName)
	}
	if pm.empty() {
		pm = packageManagerSpec{name: "npm"}
	}

	hasLock := hasAnyFile(byName, "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb")
	build := ""
	install := pm.install(hasLock)
	start := ""
	output := ""
	score := 70
	framework := "node"

	switch {
	case hasDep("next") || hasAnyFile(byName, "next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs"):
		framework = "nextjs"
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, framework)
		output = ".next"
		score = 125
	case hasAnyDep("nuxt", "@nuxt/devtools"), hasAnyFile(byName, "nuxt.config.js", "nuxt.config.mjs", "nuxt.config.ts", "nuxt.config.cjs"):
		framework = "nuxt"
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, framework)
		output = ".output"
		score = 120
	case hasAnyDep("@sveltejs/kit"), hasAnyFile(byName, "svelte.config.js", "svelte.config.mjs", "svelte.config.ts", "svelte.config.cjs"):
		framework = "sveltekit"
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, framework)
		output = "build"
		score = 118
	case hasAnyDep("@remix-run/node", "@remix-run/react", "@remix-run/dev"):
		framework = "remix"
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, framework)
		output = "build"
		score = 110
	case hasAnyDep("@nestjs/core", "@nestjs/common", "@nestjs/platform-express", "@nestjs/platform-fastify"):
		framework = "nestjs"
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, framework)
		output = "dist"
		score = 109
	case hasAnyDep("fastify"):
		framework = "fastify"
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, framework)
		output = "dist"
		score = 106
	case hasAnyDep("express"):
		framework = "express"
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, framework)
		output = "dist"
		score = 104
	case hasAnyDep("react", "react-dom", "@vitejs/plugin-react") || hasAnyFile(byName, "vite.config.js", "vite.config.mjs", "vite.config.ts", "vite.config.cjs") && hasAnyDep("react", "react-dom", "@vitejs/plugin-react"):
		framework = "react"
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, framework)
		output = "dist"
		score = 105
	case hasDep("vite") || hasAnyFile(byName, "vite.config.js", "vite.config.mjs", "vite.config.ts", "vite.config.cjs"):
		framework = "vite"
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, framework)
		output = "dist"
		score = 95
	case hasAnyDep("react", "react-dom") && hasScript("build"):
		framework = "react"
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, framework)
		output = "dist"
		score = 100
	case hasScript("build") || hasScript("start") || hasScript("preview"):
		build = nodeBuildCommand(pm, pkg.Scripts, framework)
		start = nodeStartCommand(pm, pkg.Scripts, "node")
		score = 60
	default:
		if hasScript("start") || hasScript("preview") {
			start = nodeStartCommand(pm, pkg.Scripts, "node")
			score = 55
		}
	}

	if framework == "node" && build == "" && start == "" && !hasScript("build") && !hasScript("start") && !hasScript("preview") && !hasAnyDep("react", "react-dom", "vite", "next", "nuxt", "@sveltejs/kit", "@remix-run/node", "@remix-run/react", "@vitejs/plugin-react", "@nestjs/core", "@nestjs/common", "fastify", "express") {
		return frameworkCandidate{}, false
	}

	det := detectionWithStrings(framework, build, install, output)
	if pm.label() != "" {
		det.PackageManager = ptrString(pm.label())
	}
	if start != "" {
		det.StartCommand = ptrString(start)
	}
	if build == "" && hasScript("build") {
		det.BuildCommand = ptrString(pm.run("build"))
	}
	if strings.Contains(effectiveStartBody(pkg.Scripts, start), "vite preview") {
		det.Port = ptrInt(4173)
	}
	return frameworkCandidate{detection: det, score: score, depth: depth, path: entry.Path}, true
}

// effectiveStartBody returns the command that actually runs at container start:
// the explicit start (then preview) script body when the repo declares one,
// otherwise the fallback command the detector synthesized. Framework port
// defaults are a static guess; the reported port must match the process the
// start command really launches. A Vite app served via `vite preview` binds
// 4173, not the 3000 that "react"/"sveltekit" default to.
func effectiveStartBody(scripts map[string]string, fallback string) string {
	if v, ok := scripts["start"]; ok {
		return v
	}
	if v, ok := scripts["preview"]; ok {
		return v
	}
	return fallback
}

func nodeBuildCommand(pm packageManagerSpec, scripts map[string]string, framework string) string {
	if _, ok := scripts["build"]; ok {
		return pm.run("build")
	}
	switch framework {
	case "nextjs":
		return pm.exec("next", "build")
	case "nuxt":
		return pm.exec("nuxi", "build")
	case "sveltekit":
		return pm.exec("svelte-kit", "build")
	case "remix":
		return pm.exec("remix", "build")
	case "vite", "react":
		return pm.exec("vite", "build")
	case "nestjs":
		return pm.exec("nest", "build")
	default:
		return ""
	}
}

func nodeStartCommand(pm packageManagerSpec, scripts map[string]string, framework string) string {
	if _, ok := scripts["start"]; ok {
		return pm.run("start")
	}
	if _, ok := scripts["preview"]; ok {
		return pm.run("preview")
	}
	switch framework {
	case "nextjs":
		return pm.exec("next", "start")
	case "nuxt":
		return pm.exec("nuxi", "preview")
	case "sveltekit":
		return pm.exec("vite", "preview")
	case "vite", "react":
		return pm.exec("vite", "preview")
	case "nestjs":
		return "node dist/main.js"
	default:
		return ""
	}
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
	switch {
	case hasAnyFile(byName, "poetry.lock"):
		if cand.detection.PackageManager == nil || *cand.detection.PackageManager != "poetry" {
			cand.detection.PackageManager = ptrString("poetry")
		}
		if cand.detection.InstallCommand == nil || *cand.detection.InstallCommand == "" {
			cand.detection.InstallCommand = ptrString("poetry install")
		}
	case hasAnyFile(byName, "uv.lock"):
		if cand.detection.PackageManager == nil || *cand.detection.PackageManager != "uv" {
			cand.detection.PackageManager = ptrString("uv")
		}
		if cand.detection.InstallCommand == nil || *cand.detection.InstallCommand == "" {
			cand.detection.InstallCommand = ptrString("uv sync")
		}
	case hasAnyFile(byName, "requirements.txt"):
		if cand.detection.PackageManager == nil || *cand.detection.PackageManager != "pip" {
			cand.detection.PackageManager = ptrString("pip")
		}
		if cand.detection.InstallCommand == nil || *cand.detection.InstallCommand == "" {
			cand.detection.InstallCommand = ptrString("pip install -r requirements.txt")
		}
	}
	return cand, true
}

func pythonPackageManagerFromContent(raw, path string) (packageManagerSpec, string) {
	lower := strings.ToLower(raw)
	path = strings.ToLower(path)
	switch {
	case strings.Contains(lower, "tool.poetry") || strings.Contains(lower, "poetry-core") || strings.Contains(lower, "[tool.poetry]"):
		return packageManagerSpec{name: "poetry"}, "poetry install"
	case strings.Contains(lower, "[tool.uv]") || strings.Contains(lower, "uv ="):
		return packageManagerSpec{name: "uv"}, "uv sync"
	case strings.Contains(path, "requirements.txt"):
		return packageManagerSpec{name: "pip"}, "pip install -r requirements.txt"
	case strings.Contains(path, "setup.py"):
		return packageManagerSpec{name: "pip"}, "pip install -e ."
	default:
		return packageManagerSpec{name: "pip"}, "pip install -e ."
	}
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
	default:
		framework = "python"
	}
	build := ""
	pm, install := pythonPackageManagerFromContent(raw, path)
	if pm.name == "pip" && strings.Contains(strings.ToLower(path), "requirements.txt") {
		install = "pip install -r requirements.txt"
	}
	if pm.name == "poetry" && strings.Contains(strings.ToLower(path), "pyproject.toml") {
		install = "poetry install"
	}
	if pm.name == "uv" && strings.Contains(strings.ToLower(path), "pyproject.toml") {
		install = "uv sync"
	}
	return frameworkCandidate{
		detection: func() frameworkDetection {
			det := detectionWithStrings(framework, build, install, "")
			if pm.label() != "" {
				det.PackageManager = ptrString(pm.label())
			}
			return det
		}(),
		score: score,
		depth: depth,
		path:  path,
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
	pm := "gradle"
	start := ""

	switch {
	case strings.Contains(lower, "org.springframework.boot") || strings.Contains(lower, "spring-boot") || strings.Contains(lower, "org.springframework"):
		framework = "spring-gradle"
		score = 115
		start = "java -jar build/libs/*.jar"
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
		detection: func() frameworkDetection {
			det := detectionWithStrings(framework, build, install, output)
			det.PackageManager = ptrString(pm)
			if start != "" {
				det.StartCommand = ptrString(start)
			}
			return det
		}(),
		score: score,
		depth: depth,
		path:  path,
	}, true
}

func detectSbtFramework(ctx context.Context, token, owner, repo string, entry githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	lower := strings.ToLower(raw)
	if !strings.Contains(lower, "scala") && !strings.Contains(lower, "gitbucket") && !strings.Contains(lower, "sbt.version") {
		return frameworkCandidate{}, false
	}
	det := detectionWithStrings("scala", "sbt package", "sbt update", "target")
	det.PackageManager = ptrString("sbt")
	return frameworkCandidate{
		detection: det,
		score:     118,
		depth:     depth,
		path:      entry.Path,
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
	pm := "maven"
	start := ""
	if strings.Contains(lower, "org.springframework.boot") || strings.Contains(lower, "spring-boot") || strings.Contains(lower, "org.springframework") {
		framework = "spring-maven"
		score = 112
		start = "java -jar target/*.jar"
	}
	return frameworkCandidate{
		detection: func() frameworkDetection {
			det := detectionWithStrings(framework, build, install, output)
			det.PackageManager = ptrString(pm)
			if start != "" {
				det.StartCommand = ptrString(start)
			}
			return det
		}(),
		score: score,
		depth: depth,
		path:  entry.Path,
	}, true
}

func ptrString(s string) *string {
	return &s
}

func nodePackageManagerFromLockfileName(name string) packageManagerSpec {
	switch strings.ToLower(name) {
	case "bun.lock", "bun.lockb":
		return packageManagerSpec{name: "bun"}
	case "pnpm-lock.yaml":
		return packageManagerSpec{name: "pnpm"}
	case "yarn.lock":
		return packageManagerSpec{name: "yarn"}
	case "package-lock.json":
		return packageManagerSpec{name: "npm"}
	default:
		return packageManagerSpec{}
	}
}

func detectNodeFrameworkFromLockfile(raw string, pm packageManagerSpec) (frameworkCandidate, bool) {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "\"next\""), strings.Contains(lower, "next@"):
		fw, build, output := "nextjs", pm.exec("next", "build"), ".next"
		det := detectionWithStrings(fw, build, pm.install(true), output)
		det.PackageManager = ptrString(pm.label())
		det.StartCommand = ptrString(pm.exec("next", "start"))
		return frameworkCandidate{detection: det, score: 100, depth: 0}, true
	case strings.Contains(lower, "\"nuxt\""), strings.Contains(lower, "nuxt@"):
		fw, build, output := "nuxt", pm.exec("nuxi", "build"), ".output"
		det := detectionWithStrings(fw, build, pm.install(true), output)
		det.PackageManager = ptrString(pm.label())
		det.StartCommand = ptrString(pm.exec("nuxi", "preview"))
		return frameworkCandidate{detection: det, score: 98, depth: 0}, true
	case strings.Contains(lower, "@sveltejs/kit"):
		fw, build := "sveltekit", pm.exec("svelte-kit", "build")
		det := detectionWithStrings(fw, build, pm.install(true), "build")
		det.PackageManager = ptrString(pm.label())
		det.StartCommand = ptrString(pm.exec("vite", "preview"))
		return frameworkCandidate{detection: det, score: 96, depth: 0}, true
	case strings.Contains(lower, "@vitejs/plugin-react"), strings.Contains(lower, "react-dom"), strings.Contains(lower, "\"react\""):
		fw, build, output := "react", pm.exec("vite", "build"), "dist"
		det := detectionWithStrings(fw, build, pm.install(true), output)
		det.PackageManager = ptrString(pm.label())
		det.StartCommand = ptrString(pm.exec("vite", "preview"))
		return frameworkCandidate{detection: det, score: 90, depth: 0}, true
	case strings.Contains(lower, "\"vite\""):
		fw, build, output := "vite", pm.exec("vite", "build"), "dist"
		det := detectionWithStrings(fw, build, pm.install(true), output)
		det.PackageManager = ptrString(pm.label())
		det.StartCommand = ptrString(pm.exec("vite", "preview"))
		return frameworkCandidate{detection: det, score: 85, depth: 0}, true
	default:
		return frameworkCandidate{}, false
	}
}

func detectNodeFrameworkFromLockfileEntry(ctx context.Context, token, owner, repo string, entry githubContent, depth int) (frameworkCandidate, bool) {
	raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
	if err != nil {
		return frameworkCandidate{}, false
	}
	pm := nodePackageManagerFromLockfileName(entry.Name)
	if pm.empty() {
		pm = packageManagerSpec{name: "npm"}
	}
	cand, ok := detectNodeFrameworkFromLockfile(raw, pm)
	if !ok {
		return frameworkCandidate{}, false
	}
	cand.depth = depth
	cand.path = entry.Path
	return cand, true
}

func detectPythonLockfileFramework(raw string, path string, depth int) (frameworkCandidate, bool) {
	lower := strings.ToLower(raw)
	pm := packageManagerSpec{name: "pip"}
	install := "pip install -r requirements.txt"
	switch {
	case strings.Contains(strings.ToLower(path), "poetry.lock"):
		pm = packageManagerSpec{name: "poetry"}
		install = "poetry install"
	case strings.Contains(strings.ToLower(path), "uv.lock"):
		pm = packageManagerSpec{name: "uv"}
		install = "uv sync"
	}
	switch {
	case strings.Contains(lower, "fastapi"):
		fw := "fastapi"
		det := detectionWithStrings(fw, "", install, "")
		det.PackageManager = ptrString(pm.label())
		return frameworkCandidate{detection: det, score: 100, depth: depth, path: path}, true
	case strings.Contains(lower, "django"):
		fw := "django"
		det := detectionWithStrings(fw, "", install, "")
		det.PackageManager = ptrString(pm.label())
		return frameworkCandidate{detection: det, score: 98, depth: depth, path: path}, true
	case strings.Contains(lower, "flask"):
		fw := "flask"
		det := detectionWithStrings(fw, "", install, "")
		det.PackageManager = ptrString(pm.label())
		return frameworkCandidate{detection: det, score: 96, depth: depth, path: path}, true
	case strings.Contains(lower, "poetry"):
		fw := "python"
		det := detectionWithStrings(fw, "", install, "")
		det.PackageManager = ptrString(pm.label())
		return frameworkCandidate{detection: det, score: 94, depth: depth, path: path}, true
	case strings.Contains(lower, "uv"):
		fw := "python"
		det := detectionWithStrings(fw, "", install, "")
		det.PackageManager = ptrString(pm.label())
		return frameworkCandidate{detection: det, score: 93, depth: depth, path: path}, true
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
	byName := mapFromEntries(entries)
	pmHint := nodePackageManagerHint(byName, packageManagerSpec{})
	return append(detectCandidatesInDir(ctx, token, owner, repo, dir, depth, entries, pmHint), detectCandidatesFromLockfiles(ctx, token, owner, repo, byName, depth)...), nil
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
		if p := frameworkDefaultPort(framework); p != nil {
			d.Port = p
		}
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

func frameworkDefaultPort(framework string) *int {
	switch strings.ToLower(framework) {
	case "nextjs", "nuxt", "sveltekit", "remix", "react", "nestjs", "node", "express", "fastify", "javascript", "web":
		return ptrInt(3000)
	case "vite":
		return ptrInt(4173)
	case "fastapi", "django", "python":
		return ptrInt(8000)
	case "flask":
		return ptrInt(5000)
	case "streamlit":
		return ptrInt(8501)
	case "spring", "spring-maven", "spring-gradle", "maven", "gradle", "scala", "sbt", "go", "dockerfile":
		return ptrInt(8080)
	case "static":
		return ptrInt(80)
	default:
		return nil
	}
}

func ptrInt(v int) *int { return &v }

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
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	githubAPISem <- struct{}{}
	defer func() { <-githubAPISem }()
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

// verifyWebhook checks the HMAC against the App-level webhook secret first.
//
// A GitHub App delivers every installation's events to one webhook URL, signed
// with the single secret configured in the App settings -- never the per-repo
// secret. So the App-level secret (BUILD_GITHUB_WEBHOOK_SECRET) takes priority.
// The per-repo secret remains a fallback for future per-repo hook providers
// (e.g. GitLab) where each repo registers its own hook. Absent any configured
// secret, the signature is not enforced (dev convenience).
func (s *Server) verifyWebhook(repoSecret string, body []byte, sig string) bool {
	secret := ""
	if s.cfg != nil {
		secret = s.cfg.GitHubWebhookSecret
	}
	if secret == "" {
		secret = repoSecret
	}
	if secret == "" {
		return true
	}
	return github.VerifySignature(secret, body, sig)
}

// Hub exposes the log hub so the runner can publish frames.
func (s *Server) Hub() *Hub { return s.hub }
