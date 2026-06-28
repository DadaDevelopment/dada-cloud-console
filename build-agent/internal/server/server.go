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

	entries, err := githubListDir(ctx, token, owner, repo, rootDir)
	if err != nil {
		return frameworkDetection{}, err
	}
	byName := make(map[string]githubContent, len(entries))
	for _, entry := range entries {
		byName[strings.ToLower(entry.Name)] = entry
	}

	if _, ok := byName["dockerfile"]; ok {
		fw := "dockerfile"
		return frameworkDetection{Framework: &fw}, nil
	}
	if _, ok := byName["pom.xml"]; ok {
		fw, build, install := "spring-maven", "./mvnw package", "./mvnw -q -DskipTests dependency:go-offline"
		return frameworkDetection{Framework: &fw, BuildCommand: &build, InstallCommand: &install}, nil
	}
	if _, ok := byName["build.gradle"]; ok {
		fw, build, install := "spring-gradle", "./gradlew build", "./gradlew dependencies"
		return frameworkDetection{Framework: &fw, BuildCommand: &build, InstallCommand: &install}, nil
	}
	if _, ok := byName["build.gradle.kts"]; ok {
		fw, build, install := "spring-gradle", "./gradlew build", "./gradlew dependencies"
		return frameworkDetection{Framework: &fw, BuildCommand: &build, InstallCommand: &install}, nil
	}

	if entry, ok := byName["package.json"]; ok {
		raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
		if err == nil {
			return detectNodeFramework(raw), nil
		}
	}

	for _, name := range []string{"requirements.txt", "pyproject.toml", "pipfile"} {
		if entry, ok := byName[name]; ok {
			raw, err := githubReadFile(ctx, token, owner, repo, entry.Path)
			if err == nil {
				return detectPythonFramework(raw), nil
			}
		}
	}

	if _, ok := byName["index.html"]; ok {
		fw, output := "static", "dist"
		return frameworkDetection{Framework: &fw, OutputDir: &output}, nil
	}
	return frameworkDetection{}, nil
}

func detectNodeFramework(packageJSON string) frameworkDetection {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Scripts         map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(packageJSON), &pkg); err != nil {
		return frameworkDetection{}
	}
	hasDep := func(name string) bool {
		_, ok := pkg.Dependencies[name]
		if ok {
			return true
		}
		_, ok = pkg.DevDependencies[name]
		return ok
	}

	fw := "node"
	output := ""
	switch {
	case hasDep("next"):
		fw = "nextjs"
		output = ".next"
	case hasDep("@nestjs/core"):
		fw = "nestjs"
		output = "dist"
	case hasDep("@remix-run/node") || hasDep("@remix-run/react"):
		fw = "remix"
		output = "build"
	case hasDep("vite"):
		fw = "vite"
		output = "dist"
	case hasDep("express"):
		fw = "node"
	}

	install := "npm ci"
	build := ""
	if _, ok := pkg.Scripts["build"]; ok {
		build = "npm run build"
	}
	return detectionWithStrings(fw, build, install, output)
}

func detectPythonFramework(raw string) frameworkDetection {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "fastapi"):
		return detectionWithStrings("fastapi", "", "pip install -r requirements.txt", "")
	case strings.Contains(lower, "django"):
		return detectionWithStrings("django", "", "pip install -r requirements.txt", "")
	case strings.Contains(lower, "flask"):
		return detectionWithStrings("flask", "", "pip install -r requirements.txt", "")
	default:
		return frameworkDetection{}
	}
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
