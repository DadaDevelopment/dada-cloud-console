package worker

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/build-agent/internal/config"
	"github.com/dada-tuda/console/build-agent/internal/db"
	"github.com/dada-tuda/console/build-agent/internal/detect"
	"github.com/dada-tuda/console/build-agent/internal/github"
	"github.com/dada-tuda/console/build-agent/internal/jenkins"
	"github.com/dada-tuda/console/build-agent/internal/metrics"
	"github.com/dada-tuda/console/build-agent/internal/queue"
	"github.com/dada-tuda/console/build-agent/internal/registry"
	"github.com/dada-tuda/console/build-agent/internal/server"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// logPoll is how often the control plane pulls new console text + build status
// from Jenkins while a build runs.
const logPoll = 2 * time.Second

// errBuildAborted is the sentinel for a Jenkins ABORTED result; it maps to the
// existing canceled state instead of failed.
var errBuildAborted = errors.New("jenkins build aborted")

// Runner drives one build through the state machine by orchestrating Jenkins
// (trigger → poll → log-bridge) and confirming outputs against Nexus. It owns
// the queue draining loop. Jenkins owns the actual build + push; this is the
// pure-pull control plane (ADR-010).
type Runner struct {
	pool       *pgxpool.Pool
	cfg        *config.Config
	scheduler  *queue.Scheduler
	jenkins    *jenkins.Client
	registry   registry.Registry
	github     github.App
	publishLog func(buildID, line string)
}

// capturedArtifact is one Android output parsed from a console marker.
type capturedArtifact struct {
	typ         string // apk | aab
	nexusURL    string
	size        int64
	sha256      string
	versionCode int
}

// buildOutcome is what the log bridge parsed out of the Jenkins console. The
// presence of imageURI vs artifacts (not the requested framework — which may be
// "auto") decides the web-deploy vs android-artifact path.
type buildOutcome struct {
	imageURI  string             // web: ==> image: <host>/<proj>/<app>@sha256:<digest>
	artifacts []capturedArtifact // android: ==> artifact: <type> <url> <size> <sha256> <versionCode>
}

// NewRunner wires a Runner with production dependencies: a Jenkins REST client
// and a read-only Nexus client. No Kubernetes — Jenkins runs the build.
func NewRunner(pool *pgxpool.Pool, cfg *config.Config, publishLog func(buildID, line string)) *Runner {
	return &Runner{
		pool:       pool,
		cfg:        cfg,
		scheduler:  queue.New(cfg.MaxConcurrent),
		jenkins:    jenkins.New(cfg.JenkinsURL, cfg.JenkinsUser, cfg.JenkinsToken),
		registry:   registry.NewNexus(cfg.NexusDockerHost, cfg.NexusUser, cfg.NexusToken),
		github:     github.New(cfg.GitHubAppID, cfg.GitHubAppKey),
		publishLog: publishLog,
	}
}

// OnPush implements the webhook nudge: drain the queue right away.
func (r *Runner) OnPush(ctx context.Context) { r.DrainQueue(ctx) }

// reapGrace pads BuildTimeout so the reaper never races a live build's own
// timeout or a brief two-pod overlap during a rolling deploy.
const reapGrace = 2 * time.Minute

// ReapStuck fails orphaned in-flight builds (agent died mid-build). Safe to call
// on every poll tick: the query only touches rows older than BuildTimeout+grace.
func (r *Runner) ReapStuck(ctx context.Context) {
	ids, err := db.ReapStuckBuilds(ctx, r.pool, r.cfg.BuildTimeout+reapGrace)
	if err != nil {
		log.Error().Err(err).Msg("reap stuck builds")
		return
	}
	for _, id := range ids {
		log.Warn().Str("build", id.String()).Msg("orphaned build reaped (failed)")
	}
}

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

	out, err := r.execute(ctx, b, repo, &llog)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, errBuildAborted) {
			llog.Info().Msg("build canceled")
			r.emit(ctx, b.ID, "build canceled")
			// supersede already set canceled; if aborted from Jenkins, set it now.
			if errors.Is(err, errBuildAborted) {
				_, _ = db.MarkCanceled(ctx, r.pool, b.ID)
			}
			return
		}
		r.failFromCurrent(ctx, b, err)
		r.postStatus(ctx, repo, b, "failure", "build failed")
		metrics.BuildTotal.WithLabelValues("failed").Inc()
		return
	}

	// pushing → success (pins image_uri + finished_at). Android has no image.
	if ok, ferr := db.FinishSuccess(ctx, r.pool, b.ID, out.imageURI); ferr != nil || !ok {
		r.fail(ctx, b, db.StatusPushing, fmt.Errorf("finalize success: %w", ferr))
		return
	}

	if out.imageURI != "" {
		// Web → deploy handoff (the ONLY re-entry into the declarative path).
		// First build of a not-yet-existing app enqueues CreateApp; later builds
		// enqueue DeployImageVersion. No placeholder image is ever deployed.
		opID, herr := db.HandoffDeploy(ctx, r.pool, b, repo, out.imageURI, db.DefaultDomainOpts{
			Enabled: r.cfg.DefaultDomainEnabled,
			Base:    r.cfg.DefaultDomainBase,
		})
		if herr != nil {
			llog.Error().Err(herr).Msg("deploy handoff failed (build succeeded, deploy not enqueued)")
		} else {
			llog.Info().Str("operation", opID.String()).Msg("deploy operation enqueued")
		}
	} else {
		// Android → record artifacts; no deploy.
		for _, a := range out.artifacts {
			if aerr := db.InsertArtifact(ctx, r.pool, db.BuildArtifact{
				BuildID:     b.ID,
				Type:        a.typ,
				NexusURL:    a.nexusURL,
				Size:        a.size,
				VersionCode: a.versionCode,
				SHA256:      a.sha256,
			}); aerr != nil {
				llog.Error().Err(aerr).Str("type", a.typ).Msg("record artifact failed")
			}
		}
	}

	r.postStatus(ctx, repo, b, "success", "build succeeded")
	metrics.BuildTotal.WithLabelValues("success").Inc()
	metrics.BuildDuration.WithLabelValues("total").Observe(time.Since(start).Seconds())
	llog.Info().Str("image", out.imageURI).Int("artifacts", len(out.artifacts)).Msg("build succeeded")
}

// execute drives the Jenkins job: detecting (resolve framework + git creds) →
// building (trigger + resolve number + log bridge) → pushing (confirm Nexus).
// It returns what the build produced.
func (r *Runner) execute(ctx context.Context, b *db.Build, repo *db.Repo, llog *zerologLogger) (buildOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.BuildTimeout)
	defer cancel()

	// --- detecting: framework param + authenticated clone URL ---
	framework := detect.Resolve(repo.FrameworkOverride)

	token, cloneURL, err := r.gitCreds(ctx, repo, b)
	if err != nil {
		return buildOutcome{}, fmt.Errorf("git creds: %w", err)
	}

	// detecting → building
	if ok, err := db.Transition(ctx, r.pool, b.ID, db.StatusDetecting, db.StatusBuilding); err != nil || !ok {
		return buildOutcome{}, fmt.Errorf("transition detecting→building: %w", err)
	}

	// --- building: trigger the job, resolve its build number ---
	params := map[string]string{
		"repo":         cloneURL,
		"branch":       b.Branch,
		"framework":    string(framework),
		"buildType":    "debug",
		"env":          b.EnvironmentID.String(),
		"project_slug": repo.ProjectSlug,
		"app_name":     repo.AppName,
	}

	// Build-time framework detection: hand the Jenkins job concrete install/build/
	// start/output so it can template a Dockerfile for repos that carry none.
	// GitHub-only (detection hits the GitHub API) and best-effort — on failure the
	// pipeline falls back to a repo Dockerfile.
	if repo.Provider == "github" {
		if det, derr := server.DetectForBuild(ctx, token, repo.RepoFullName, repo.RootDir); derr != nil {
			llog.Warn().Err(derr).Msg("build-time framework detect failed; pipeline falls back to repo Dockerfile")
		} else {
			params["detected_framework"] = det.Framework
			params["package_manager"] = det.PackageManager
			params["install_cmd"] = det.InstallCommand
			params["build_cmd"] = det.BuildCommand
			params["start_cmd"] = det.StartCommand
			params["output_dir"] = det.OutputDir
			if det.Port > 0 {
				params["app_port"] = strconv.Itoa(det.Port)
			}
			llog.Info().Str("framework", det.Framework).Msg("build-time framework detected")
		}
	}
	queueID, err := r.jenkins.TriggerBuild(ctx, r.cfg.JenkinsJob, params)
	if err != nil {
		return buildOutcome{}, fmt.Errorf("trigger jenkins build: %w", err)
	}
	r.emit(ctx, b.ID, fmt.Sprintf("queued in jenkins (item %d)", queueID))

	number, err := r.waitForBuildNumber(ctx, queueID)
	if err != nil {
		return buildOutcome{}, err
	}
	r.emit(ctx, b.ID, fmt.Sprintf("jenkins build #%d started", number))

	// --- log bridge: stream console + parse markers until the build ends ---
	out, result, err := r.bridge(ctx, b, number)
	if err != nil {
		return buildOutcome{}, err
	}
	switch result {
	case "SUCCESS":
	case "ABORTED":
		return buildOutcome{}, errBuildAborted
	default: // FAILURE or anything non-success
		return buildOutcome{}, fmt.Errorf("jenkins build #%d result %s", number, result)
	}

	// building → pushing (Jenkins already pushed; this marks the boundary).
	if ok, err := db.Transition(ctx, r.pool, b.ID, db.StatusBuilding, db.StatusPushing); err != nil || !ok {
		return buildOutcome{}, fmt.Errorf("transition building→pushing: %w", err)
	}

	// --- confirm what the markers claimed actually exists in Nexus ---
	if err := r.confirm(ctx, repo, &out); err != nil {
		return buildOutcome{}, err
	}
	return out, nil
}

// waitForBuildNumber polls the queue item until Jenkins assigns an executor.
func (r *Runner) waitForBuildNumber(ctx context.Context, queueID int64) (int, error) {
	for {
		number, started, err := r.jenkins.ResolveBuildNumber(ctx, queueID)
		if err != nil {
			return 0, fmt.Errorf("resolve build number: %w", err)
		}
		if started {
			return number, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(logPoll):
		}
	}
}

// bridge streams the console (progressiveText, incremental offset) into the WS
// hub + builds_logs and parses output markers, until the build is no longer
// building. Returns the parsed outcome and the final Jenkins result string.
func (r *Runner) bridge(ctx context.Context, b *db.Build, number int) (buildOutcome, string, error) {
	var (
		out     buildOutcome
		offset  int64
		pending string
	)
	for {
		text, next, _, err := r.jenkins.ProgressiveText(ctx, r.cfg.JenkinsJob, number, offset)
		if err != nil {
			return out, "", fmt.Errorf("stream console: %w", err)
		}
		if next != offset {
			offset = next
			pending += text
			pending = r.flushLines(ctx, b, pending, &out, false)
		}

		bi, err := r.jenkins.GetBuild(ctx, r.cfg.JenkinsJob, number)
		if err != nil {
			return out, "", fmt.Errorf("poll build: %w", err)
		}
		if !bi.Building {
			// Jenkins can report building=false before progressiveText has
			// exposed the console tail — and the ==> image: marker lives in that
			// tail. Draining once races the flush and loses the marker (build
			// shows SUCCESS in Jenkins but the runner sees "no markers"). Keep
			// draining until the console is fully caught up (no more data and no
			// new bytes), bounded so a stuck stream can't hang the build.
			drainUntil := time.Now().Add(30 * time.Second)
			for {
				text, next, more, perr := r.jenkins.ProgressiveText(ctx, r.cfg.JenkinsJob, number, offset)
				if perr != nil {
					break
				}
				if next != offset {
					offset = next
					pending += text
					pending = r.flushLines(ctx, b, pending, &out, false)
				}
				if !more && next == offset {
					break
				}
				if time.Now().After(drainUntil) {
					break
				}
				select {
				case <-ctx.Done():
					return out, "", ctx.Err()
				case <-time.After(500 * time.Millisecond):
				}
			}
			r.flushLines(ctx, b, pending, &out, true)
			// Belt-and-suspenders: building=false can be observed a beat before
			// Jenkins flushes the final console lines, so even the bounded drain
			// above can race the ==> image: marker (it lands in the same ~0.5s as
			// the build-complete signal). A completed build's full console always
			// contains the marker — if streaming missed it, re-read the whole log
			// once and parse markers only (no re-emit, so the live UI is unchanged).
			if out.imageURI == "" && len(out.artifacts) == 0 {
				r.parseFullConsole(ctx, number, &out)
			}
			return out, bi.Result, nil
		}

		select {
		case <-ctx.Done():
			return out, "", ctx.Err()
		case <-time.After(logPoll):
		}
	}
}

// parseFullConsole re-reads the entire Jenkins console (from offset 0, following
// progressiveText pagination) and runs parseMarker over every line. It does NOT
// emit — it is a safety net to recover output markers the live stream raced past
// when the build finished. Bounded so a misbehaving stream can't loop forever.
func (r *Runner) parseFullConsole(ctx context.Context, number int, out *buildOutcome) {
	var (
		sb     strings.Builder
		offset int64
	)
	for i := 0; i < 2000; i++ {
		text, next, more, err := r.jenkins.ProgressiveText(ctx, r.cfg.JenkinsJob, number, offset)
		if err != nil {
			return
		}
		sb.WriteString(text)
		if next == offset && !more {
			break
		}
		offset = next
		if !more {
			break
		}
	}
	for _, line := range strings.Split(sb.String(), "\n") {
		parseMarker(strings.TrimRight(line, "\r"), out)
	}
}

// flushLines splits pending console text on newlines, emits each complete line
// (WS + builds_logs) and parses markers. When final is true the trailing
// fragment is emitted too. Returns the unflushed remainder (empty when final).
func (r *Runner) flushLines(ctx context.Context, b *db.Build, pending string, out *buildOutcome, final bool) string {
	for {
		i := strings.IndexByte(pending, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(pending[:i], "\r")
		pending = pending[i+1:]
		if clean, ok := sanitizeLogLine(line); ok {
			r.emit(ctx, b.ID, clean)
		}
		parseMarker(line, out)
	}
	if final && pending != "" {
		line := strings.TrimRight(pending, "\r")
		if clean, ok := sanitizeLogLine(line); ok {
			r.emit(ctx, b.ID, clean)
		}
		parseMarker(line, out)
		return ""
	}
	return pending
}

// parseMarker extracts the structured output markers the jenkins-lib pipeline
// emits (the integration contract):
//
//	==> image: <host>/<proj>/<app>@sha256:<digest>
//	==> artifact: <apk|aab> <nexus-raw-url> <size_bytes> <sha256> <versionCode>
func parseMarker(line string, out *buildOutcome) {
	s := stripLogTimestamp(strings.TrimSpace(line))
	const imgP = "==> image:"
	const artP = "==> artifact:"
	switch {
	case strings.HasPrefix(s, imgP):
		out.imageURI = strings.TrimSpace(s[len(imgP):])
	case strings.HasPrefix(s, artP):
		f := strings.Fields(strings.TrimSpace(s[len(artP):]))
		if len(f) < 5 {
			return
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		vc, _ := strconv.Atoi(f[4])
		out.artifacts = append(out.artifacts, capturedArtifact{
			typ:         f[0],
			nexusURL:    f[1],
			size:        size,
			sha256:      f[3],
			versionCode: vc,
		})
	}
}

// confirm verifies that what the console claimed really exists in Nexus before
// the control plane records it. Web: Docker v2 manifest by digest. Android: raw
// HEAD per artifact (and trust-but-verify the size from Content-Length).
func (r *Runner) confirm(ctx context.Context, repo *db.Repo, out *buildOutcome) error {
	switch {
	case out.imageURI != "":
		digest := imageDigest(out.imageURI)
		if digest == "" {
			return fmt.Errorf("image marker has no digest: %q", out.imageURI)
		}
		ok, err := r.registry.VerifyImageDigest(ctx, repo.ProjectSlug, repo.AppName, digest)
		if err != nil {
			return fmt.Errorf("verify image in nexus: %w", err)
		}
		if !ok {
			return fmt.Errorf("image %s not found in nexus", out.imageURI)
		}
		return nil
	case len(out.artifacts) > 0:
		for i := range out.artifacts {
			a := &out.artifacts[i]
			size, ok, err := r.registry.HeadRawArtifact(ctx, a.nexusURL)
			if err != nil {
				return fmt.Errorf("head artifact %s: %w", a.nexusURL, err)
			}
			if !ok {
				return fmt.Errorf("artifact %s not found in nexus", a.nexusURL)
			}
			if size > 0 {
				a.size = size
			}
		}
		return nil
	default:
		return fmt.Errorf("build produced no image or artifact markers")
	}
}

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

var logTsRe = regexp.MustCompile(`^\[\d[^\]]*\]\s+`)

func stripLogTimestamp(s string) string {
	return logTsRe.ReplaceAllString(s, "")
}

var (
	consoleNoteRe = regexp.MustCompile("\x1b\\[8m.*?\x1b\\[0m")
	ansiRe        = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")
	credURLRe     = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*:[/][/])[^/\s:@]+:[^/\s@]+@`)
	ghTokenRe     = regexp.MustCompile(`gh[posru]_[A-Za-z0-9]{20,}`)
)

var jenkinsNoisePrefixes = []string{
	"[Pipeline] ",
	"[Checks API] ",
	"Started by ",
	"Loading library ",
	"Attempting to resolve ",
	"Found match: ",
	"The recommended git tool",
	"using GIT_SSH",
	"using credential",
	"Fetching ",
	"Created Pod:",
	"[PodInfo] ",
	"Container [",
	"Pod [",
	"> git ",
	"> /usr/bin/git ",
	"Start of Pipeline",
	"End of Pipeline",
}

func sanitizeLogLine(raw string) (string, bool) {
	s := consoleNoteRe.ReplaceAllString(raw, "")
	s = ansiRe.ReplaceAllString(s, "")
	s = strings.TrimRight(s, "\r")
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", false
	}
	body := stripLogTimestamp(trimmed)
	if body == "" {
		return "", false
	}
	if strings.HasPrefix(body, "//") {
		return "", false
	}
	for _, p := range jenkinsNoisePrefixes {
		if strings.HasPrefix(body, p) {
			return "", false
		}
	}
	s = credURLRe.ReplaceAllString(s, "$1***@")
	s = ghTokenRe.ReplaceAllString(s, "***")
	return s, true
}

func imageDigest(uri string) string {
	i := strings.LastIndex(uri, "@")
	if i < 0 {
		return ""
	}
	d := uri[i+1:]
	if !digestRe.MatchString(d) {
		return ""
	}
	return d
}

// gitCreds returns a clone token and the authenticated clone URL. GitHub uses a
// per-build App installation token; GitLab uses the decrypted stored PAT. For a
// fork-unsafe build no token is injected (the clone stays anonymous).
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
		if b.ForkUnsafe {
			return tok, repo.CloneURL, nil
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
		if b.ForkUnsafe {
			return tok, repo.CloneURL, nil
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

// zerologLogger aliases the zerolog Logger so the run/execute signatures stay
// compact without importing the package name at call sites.
type zerologLogger = zerolog.Logger
