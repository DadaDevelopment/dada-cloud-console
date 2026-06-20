package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dada-tuda/console/build-agent/internal/builder"
	"github.com/dada-tuda/console/build-agent/internal/config"
	"github.com/dada-tuda/console/build-agent/internal/db"
	"github.com/dada-tuda/console/build-agent/internal/detect"
	"github.com/dada-tuda/console/build-agent/internal/github"
	"github.com/dada-tuda/console/build-agent/internal/isolation"
	"github.com/dada-tuda/console/build-agent/internal/kube"
	"github.com/dada-tuda/console/build-agent/internal/metrics"
	"github.com/dada-tuda/console/build-agent/internal/queue"
	"github.com/dada-tuda/console/build-agent/internal/registry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Runner drives one build through the state machine and owns the queue draining
// loop. It composes the builder/registry/github/isolation packages.
type Runner struct {
	pool       *pgxpool.Pool
	cfg        *config.Config
	scheduler  *queue.Scheduler
	builder    builder.Builder
	registry   registry.Registry
	isolation  isolation.Manager
	github     github.App
	harbor     *registry.Harbor // concrete, for ImageTag/CacheRef/Host helpers
	publishLog func(buildID, line string)
}

// NewRunner wires a Runner with production dependencies. When the in-cluster
// Kubernetes API is unavailable (local dev without a cluster) builder/isolation
// are left nil and a build will fail fast in its building phase — the agent
// still serves webhooks, metrics, and the queue.
func NewRunner(pool *pgxpool.Pool, cfg *config.Config, publishLog func(buildID, line string)) *Runner {
	harbor := registry.NewHarbor(cfg.HarborURL, cfg.HarborAdminUser, cfg.HarborAdminSecret)

	r := &Runner{
		pool:       pool,
		cfg:        cfg,
		scheduler:  queue.New(cfg.MaxConcurrent),
		registry:   harbor,
		harbor:     harbor,
		github:     github.New(cfg.GitHubAppID, cfg.GitHubAppKey),
		publishLog: publishLog,
	}

	cs, err := kube.NewClientset()
	if err != nil {
		log.Warn().Err(err).Msg("kube clientset unavailable; builds will fail in building phase")
		return r
	}
	r.builder = builder.NewK8sBuilder(cs)
	r.isolation = isolation.NewK8sManager(cs, isolation.Quotas{
		CPULimit: cfg.CPULimit,
		MemLimit: cfg.MemLimit,
	}, "")
	return r
}

// OnPush implements the webhook nudge: drain the queue right away.
func (r *Runner) OnPush(ctx context.Context) { r.DrainQueue(ctx) }

// DrainQueue claims and dispatches queued builds until the queue is empty or
// concurrency is saturated.
func (r *Runner) DrainQueue(ctx context.Context) {
	for {
		build, err := db.ClaimQueued(ctx, r.pool)
		if err != nil {
			log.Error().Err(err).Msg("claim queued build")
			return
		}
		if build == nil {
			return // queue empty
		}

		// Supersession: cancel older in-flight builds on the same repo+branch.
		r.supersede(ctx, build)

		buildCtx, release, ok := r.scheduler.Acquire(ctx, build.ID)
		if !ok {
			return // shutting down
		}
		metrics.BuildsInflight.Set(float64(r.scheduler.Inflight()))
		go func(b *db.Build) {
			defer release()
			defer metrics.BuildsInflight.Set(float64(r.scheduler.Inflight()))
			r.run(buildCtx, b)
		}(build)
	}
}

// supersede cancels and marks older non-terminal builds for the same repo+branch.
func (r *Runner) supersede(ctx context.Context, keep *db.Build) {
	ids, err := db.SupersededBuilds(ctx, r.pool, keep.GitRepoID, keep.Branch, keep.ID)
	if err != nil {
		log.Error().Err(err).Msg("supersede lookup")
		return
	}
	for _, id := range ids {
		r.scheduler.Cancel(id) // signal in-flight ctx
		if ok, _ := db.MarkCanceled(ctx, r.pool, id); ok {
			metrics.BuildSupersededTotal.Inc()
			log.Info().Str("build", id.String()).Str("by", keep.ID.String()).Msg("build superseded")
		}
	}
}

// run executes the build state machine for one build:
// detecting → building → pushing → success (+ failed/canceled).
func (r *Runner) run(ctx context.Context, b *db.Build) {
	start := time.Now()
	llog := log.With().Str("build", b.ID.String()).Str("sha", b.CommitSHA).Logger()
	llog.Info().Msg("build started")

	repo, err := db.LoadRepo(ctx, r.pool, b.GitRepoID)
	if err != nil {
		r.fail(ctx, b, db.StatusDetecting, fmt.Errorf("load repo: %w", err))
		return
	}

	r.postStatus(ctx, repo, b, "pending", "build started")

	imageURI, err := r.execute(ctx, b, repo, &llog)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			llog.Info().Msg("build canceled")
			r.emit(ctx, b.ID, "build canceled")
			return // status already set to canceled by supersede
		}
		r.failFromCurrent(ctx, b, err)
		r.postStatus(ctx, repo, b, "failure", "build failed")
		metrics.BuildTotal.WithLabelValues("failed").Inc()
		return
	}

	// pushing → success (pins image_uri + finished_at).
	if ok, ferr := db.FinishSuccess(ctx, r.pool, b.ID, imageURI); ferr != nil || !ok {
		r.fail(ctx, b, db.StatusPushing, fmt.Errorf("finalize success: %w", ferr))
		return
	}

	// Deploy handoff — the ONLY re-entry into the declarative path (invariant 2).
	opID, err := db.HandoffDeploy(ctx, r.pool, b, repo.ProjectID, imageURI)
	if err != nil {
		llog.Error().Err(err).Msg("deploy handoff failed (build succeeded, deploy not enqueued)")
		// Build is already success; surface but do not flip it back.
	} else {
		llog.Info().Str("operation", opID.String()).Msg("DeployImageVersion enqueued")
	}

	r.postStatus(ctx, repo, b, "success", "build succeeded")
	metrics.BuildTotal.WithLabelValues("success").Inc()
	metrics.BuildDuration.WithLabelValues("total").Observe(time.Since(start).Seconds())
	llog.Info().Str("image", imageURI).Msg("build succeeded")
}

// execute runs detecting→building→pushing and returns the pinned image URI.
func (r *Runner) execute(ctx context.Context, b *db.Build, repo *db.Repo, llog *zerologLogger) (string, error) {
	if r.builder == nil || r.isolation == nil {
		return "", fmt.Errorf("kubernetes API unavailable; cannot run build job")
	}

	// --- detecting: resolve framework + Harbor project + creds ---
	det := detect.Resolve(repo.FrameworkOverride, repo.RootDir)

	if err := r.registry.EnsureProject(ctx, repo.ProjectSlug); err != nil {
		return "", fmt.Errorf("ensure harbor project: %w", err)
	}
	robot, err := r.harbor.MintRobot(ctx, repo.ProjectSlug, "build")
	if err != nil {
		return "", fmt.Errorf("mint harbor robot: %w", err)
	}

	gitToken, cloneURL, err := r.gitCreds(ctx, repo, b)
	if err != nil {
		return "", fmt.Errorf("git creds: %w", err)
	}

	// detecting → building
	if ok, err := db.Transition(ctx, r.pool, b.ID, db.StatusDetecting, db.StatusBuilding); err != nil || !ok {
		return "", fmt.Errorf("transition detecting→building: %w", err)
	}

	// --- building: provision isolation, run the Job, stream logs ---
	ns, err := r.isolation.EnsureNamespace(ctx, b.ID.String())
	if err != nil {
		return "", fmt.Errorf("ensure namespace: %w", err)
	}
	defer func() {
		// best-effort teardown; ignore ctx cancellation timing
		tctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if terr := r.isolation.Teardown(tctx, ns); terr != nil {
			llog.Warn().Err(terr).Str("ns", ns).Msg("namespace teardown")
		}
	}()

	if err := r.isolation.ApplyNetworkPolicy(ctx, ns, r.cfg.GitEgressCIDRs); err != nil {
		return "", fmt.Errorf("apply network policy: %w", err)
	}

	const (
		gitSecret      = "build-git"
		registrySecret = "build-registry"
	)
	var gitSecretName string
	if gitToken != "" && !b.ForkUnsafe {
		askpass := "#!/bin/sh\necho " + shellQuote(gitToken) + "\n"
		if err := r.isolation.CreateSecret(ctx, ns, gitSecret, map[string][]byte{
			"askpass.sh": []byte(askpass),
			"token":      []byte(gitToken),
		}); err != nil {
			return "", fmt.Errorf("create git secret: %w", err)
		}
		gitSecretName = gitSecret
	}
	if err := r.isolation.CreateDockerConfigSecret(ctx, ns, registrySecret, r.harbor.Host(), robot.Name, robot.Secret); err != nil {
		return "", fmt.Errorf("create registry secret: %w", err)
	}

	params := builder.JobParams{
		BuildID:            b.ID.String(),
		Namespace:          ns,
		BuilderImage:       r.cfg.BuilderImage,
		RuntimeClass:       r.cfg.RuntimeClass,
		NodePoolLabel:      r.cfg.NodePoolLabel,
		CPULimit:           r.cfg.CPULimit,
		MemLimit:           r.cfg.MemLimit,
		GitURL:             cloneURL,
		GitBranch:          b.Branch,
		GitSHA:             b.CommitSHA,
		RootDir:            det.RootDir,
		Framework:          string(det.Framework),
		ImageName:          r.harbor.ImageTag(repo.ProjectSlug, repo.AppName, shortSHA(b.CommitSHA)),
		ImageTag:           r.harbor.ImageTag(repo.ProjectSlug, repo.AppName, "latest"),
		CacheRef:           r.harbor.CacheRef(repo.ProjectSlug, repo.AppName),
		TimeoutSeconds:     int(r.cfg.BuildTimeout.Seconds()),
		GitSecretName:      gitSecretName,
		RegistrySecretName: registrySecret,
	}

	res, err := r.builder.Build(ctx, params, func(line string) {
		r.emit(ctx, b.ID, line)
	})
	if err != nil {
		return "", fmt.Errorf("build job: %w", err)
	}

	// building → pushing (the Job already pushed; this marks the phase boundary).
	if ok, err := db.Transition(ctx, r.pool, b.ID, db.StatusBuilding, db.StatusPushing); err != nil || !ok {
		return "", fmt.Errorf("transition building→pushing: %w", err)
	}

	// Immutable digest-pinned URI is the source of truth for the deploy.
	imageURI := r.harbor.ImageURI(repo.ProjectSlug, repo.AppName, res.Digest)
	return imageURI, nil
}

// gitCreds returns a clone token and the authenticated clone URL. GitHub uses a
// per-build App installation token; GitLab uses the decrypted stored PAT.
func (r *Runner) gitCreds(ctx context.Context, repo *db.Repo, b *db.Build) (token, cloneURL string, err error) {
	switch repo.Provider {
	case "github":
		if repo.InstallationID == 0 {
			return "", "", fmt.Errorf("github repo missing installation id")
		}
		tok, terr := r.github.InstallToken(ctx, repo.InstallationID)
		if terr != nil {
			return "", "", terr
		}
		return tok, injectToken(repo.CloneURL, "x-access-token", tok), nil
	case "gitlab":
		if len(repo.TokenEncrypted) == 0 {
			return "", "", fmt.Errorf("gitlab repo missing token")
		}
		tok, derr := db.DecryptToken(r.cfg.EncryptionKey, repo.TokenEncrypted)
		if derr != nil {
			return "", "", fmt.Errorf("decrypt gitlab token: %w", derr)
		}
		return tok, injectToken(repo.CloneURL, "oauth2", tok), nil
	default:
		return "", "", fmt.Errorf("unknown provider %q", repo.Provider)
	}
}

// emit fans a log line out to the WS hub and persists it to builds_logs.
func (r *Runner) emit(ctx context.Context, buildID uuid.UUID, line string) {
	if r.publishLog != nil {
		r.publishLog(buildID.String(), line)
	}
	if err := db.AppendLog(ctx, r.pool, buildID, line); err != nil {
		log.Debug().Err(err).Msg("append build log")
	}
}

func (r *Runner) postStatus(ctx context.Context, repo *db.Repo, b *db.Build, state, desc string) {
	if repo.Provider != "github" || repo.InstallationID == 0 {
		return
	}
	url := fmt.Sprintf("https://console.dada-tuda.ru/projects/%s/apps/%s/builds/%s",
		repo.ProjectSlug, repo.AppName, b.ID.String())
	if err := r.github.PostStatus(ctx, repo.InstallationID, repo.RepoFullName, b.CommitSHA, state, url, desc); err != nil {
		log.Debug().Err(err).Msg("post commit status")
	}
}

// fail moves a build to failed from a known phase, recording the error message.
func (r *Runner) fail(ctx context.Context, b *db.Build, from string, cause error) {
	msg := ""
	if cause != nil {
		msg = cause.Error()
		log.Error().Err(cause).Str("build", b.ID.String()).Str("from", from).Msg("build failed")
	}
	if _, err := db.MarkFailed(ctx, r.pool, b.ID, from, msg); err != nil {
		log.Error().Err(err).Str("build", b.ID.String()).Msg("mark failed")
	}
	r.emit(ctx, b.ID, "BUILD FAILED: "+msg)
}

// failFromCurrent marks the build failed regardless of which phase it died in by
// trying each non-terminal phase (compare-and-set, only one wins).
func (r *Runner) failFromCurrent(ctx context.Context, b *db.Build, cause error) {
	msg := ""
	if cause != nil {
		msg = cause.Error()
		log.Error().Err(cause).Str("build", b.ID.String()).Msg("build failed")
	}
	for _, from := range []string{db.StatusBuilding, db.StatusPushing, db.StatusDetecting} {
		if ok, _ := db.MarkFailed(ctx, r.pool, b.ID, from, msg); ok {
			break
		}
	}
	r.emit(ctx, b.ID, "BUILD FAILED: "+msg)
}

// Supersede cancels an in-flight build (newer commit on same repo+branch).
func (r *Runner) Supersede(buildID uuid.UUID) { r.scheduler.Cancel(buildID) }

// --- small helpers ---

func shortSHA(sha string) string {
	if len(sha) >= 8 {
		return sha[:8]
	}
	return sha
}

// injectToken rewrites an https clone URL to embed credentials:
// https://github.com/org/repo.git → https://<user>:<token>@github.com/org/repo.git
func injectToken(cloneURL, user, token string) string {
	const httpsPrefix = "https://"
	if !strings.HasPrefix(cloneURL, httpsPrefix) {
		return cloneURL
	}
	rest := strings.TrimPrefix(cloneURL, httpsPrefix)
	return httpsPrefix + user + ":" + token + "@" + rest
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// zerologLogger aliases the zerolog Logger so the run/execute signatures stay
// compact without importing the package name at call sites.
type zerologLogger = zerolog.Logger
