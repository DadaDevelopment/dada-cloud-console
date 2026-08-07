package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// SystemUserID is the fixed, non-loginable actor used for agent-initiated
// operations (010_system_user.sql). build-agent enqueues DeployImageVersion as
// this actor.
var SystemUserID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

// deployImageVersionPayload mirrors backend models.DeployImageVersionPayload.
// Kept local so build-agent does not import the backend module.
type deployImageVersionPayload struct {
	AppName   string `json:"app_name"`
	Image     string `json:"image"`
	Framework string `json:"framework,omitempty"`
	Port      int    `json:"port,omitempty"`
}

// createAppPayload mirrors backend models.CreateAppPayload (k8s fields only — git
// builds target Helm environments). Used when a git-linked app does not exist yet
// and its first successful build must materialize it.
type createAppPayload struct {
	Name            string `json:"name"`
	Image           string `json:"image"`
	Framework       string `json:"framework,omitempty"`
	Port            int    `json:"port"`
	Replicas        int    `json:"replicas,omitempty"`
	Profile         string `json:"profile,omitempty"`
	DefaultHostname string `json:"default_hostname,omitempty"`
	Worker          bool   `json:"worker,omitempty"`
}

// DeployDetection carries build-time framework/port detection into the deploy
// handoff so the rendered App picks the right chart + servicePort. Zero values
// mean detection did not run; HandoffDeploy falls back to the git_repos spec.
type DeployDetection struct {
	Framework string
	Port      int
}

// DefaultDomainOpts carries the platform default-domain knobs into HandoffDeploy
// so a git-built app materialized by its first build gets the same auto
// surrogate hostname as a console-created app (mirrors backend apps.go CreateApp).
//
// A repo flagged worker gets no hostname at all, whatever these knobs say: an
// upload whose detection found no listening port is a bot or a queue consumer,
// and a domain pointed at it can only 502.
type DefaultDomainOpts struct {
	Enabled bool
	Base    string
}

func randomHostSuffix() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// hashFragment returns the first 4 hex characters of sha256(s), the same
// deterministic-suffix idiom as gitops-agent's ScopedArgoName. Two distinct
// long fragments that truncate to the same prefix still get distinct hashes,
// so capFragment never collides two different apps/branches onto one hostname.
func hashFragment(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:4]
}

// capFragment shrinks a variable hostname fragment (app name or branch) so
// that fixedLen+len(result) never exceeds 63 — the DNS-1123 label limit that
// gitops-agent's FQDNToName applies to the FULL fqdn (dots become dashes, so
// the whole hostname, not just its first dot-separated label, becomes a k8s
// resource name). fixedLen is the byte length of every other part of the
// final hostname (separators, suffix, base domain). When the untouched
// fragment already fits, it is returned unchanged so short/existing hostnames
// are byte-for-byte identical to before this cap existed. Otherwise the
// fragment is truncated and a short deterministic hash of the FULL untouched
// fragment is appended, so distinct long fragments never collide once
// truncated to the same prefix.
func capFragment(fragment string, fixedLen int) string {
	if fixedLen+len(fragment) <= 63 {
		return fragment
	}
	hash := hashFragment(fragment)
	maxFrag := 63 - fixedLen - 1 - len(hash)
	if maxFrag < 0 {
		maxFrag = 0
	}
	trimmed := fragment
	if len(trimmed) > maxFrag {
		trimmed = trimmed[:maxFrag]
	}
	trimmed = strings.TrimRight(trimmed, "-")
	if trimmed == "" {
		return hash
	}
	return trimmed + "-" + hash
}

// workerReplicas caps a worker at a single replica.
//
// Replicas default to 2 for HTTP apps, where a second pod is free availability
// behind the Service. A worker has no Service in front of it: every replica is
// an independent client of whatever it polls, so two of them are two competing
// consumers. For the headline case of the upload flow — a Telegram bot — this
// is not a degradation but a total outage: the Bot API allows exactly one
// getUpdates stream per token, so both pods sit in a permanent
// TelegramConflictError loop and the bot answers nobody. Observed live on
// m2-bot-worker: two replicas, 40+ consecutive conflicts, zero messages served;
// scaled to one and the same image logged "Connection established" immediately.
//
// Applied at CreateApp because that is where the app's shape is decided; the
// user can still scale a worker up later if their workload actually supports it.
func workerReplicas(replicas int, worker bool) int {
	if worker && replicas > 1 {
		return 1
	}
	return replicas
}

// workerPort zeroes the port of a worker app.
//
// Framework detection reports a port for any image that merely EXPOSEs one, and
// a bot image built from a generic python base does exactly that. A worker has
// no HTTP entrypoint: a non-zero port renders a Service in front of it, the
// url-watcher then probes a ClusterIP nobody listens on, and the console shows
// the owner a permanent "app has no listener" alert for an app that works.
func workerPort(port int, worker bool) int {
	if worker {
		return 0
	}
	return port
}

func buildDefaultHostname(base, name, suffix string) string {
	fixedLen := 1 + len(suffix) + 1 + len(base)
	label := capFragment(name, fixedLen)
	return fmt.Sprintf("%s-%s.%s", label, suffix, base)
}

// branchUnsafe matches every byte that is not a lowercase DNS-label character,
// used by sanitizeBranch to turn an arbitrary git branch name into a safe
// hostname label.
var branchUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitizeBranch turns a git branch name into a DNS-label-safe fragment:
// lowercase, every byte outside [a-z0-9] becomes '-', repeated '-' collapse to
// one, and leading/trailing '-' are trimmed. A branch left empty by this
// process (e.g. one made entirely of non-ASCII characters) falls back to
// "branch" so buildPreviewHostname never emits a malformed "--<hex4>" host.
func sanitizeBranch(branch string) string {
	s := strings.ToLower(branch)
	s = branchUnsafe.ReplaceAllString(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if s == "" {
		return "branch"
	}
	return s
}

// buildPreviewHostname builds the per-branch preview hostname
// "<app>-git-<branch>-<hex4>.<base>" (Vercel-style). Unlike buildDefaultHostname,
// which shrinks the app-name fragment, this shrinks the (already-sanitized)
// branch fragment via capFragment when the FULL hostname (app + "-git-" +
// branch + suffix + "." + base) would exceed 63 bytes — gitops-agent's
// FQDNToName turns the whole fqdn into a k8s resource name, so the cap must
// cover the base domain too, not just the leading dot-separated label. The
// app name and the "-git-"/suffix scaffolding are never shortened, only the
// branch fragment, which gets a short deterministic hash appended when it had
// to be truncated (see capFragment).
func buildPreviewHostname(base, name, branch, suffix string) string {
	branch = sanitizeBranch(branch)
	prefix := fmt.Sprintf("%s-git-", name)
	fixedLen := len(prefix) + 1 + len(suffix) + 1 + len(base)
	branch = capFragment(branch, fixedLen)
	return fmt.Sprintf("%s%s-%s.%s", prefix, branch, suffix, base)
}

// HandoffDeploy is the success-path deploy handoff (plan §4, invariant 2). It is
// the ONLY way build-agent re-enters the declarative path: it writes a
// deployments row + an operation, then links them. It NEVER touches
// Argo/Helm/k8s workloads — the existing gitops rails take it from here.
//
// The app is materialized by its FIRST successful build: if no App snapshot
// exists for (env, app_name) yet, enqueue CreateApp with the real image + the
// repo's intended spec (port/replicas/profile). Once the app exists, enqueue
// DeployImageVersion. No placeholder image is ever deployed.
//
// Supersession (runner.supersede) cancels older in-flight builds on the same
// repo+branch, so two concurrent first-builds racing to CreateApp the same name
// is not a practical concern.
//
// Steps 1-3 run in one tx so a crash never leaves a dangling deployment:
//  1. INSERT deployments (not yet current — the op-Ready watcher flips is_current).
//  2. INSERT operations (CreateApp or DeployImageVersion, status=Created, actor=system).
//  3. UPDATE deployments.operation_id = <op id>, then COMMIT.
//
// Optional side effects (audit event, surrogate default hostname) run AFTER the
// commit as separate best-effort statements: a failure there loses only that
// side effect and never rolls back the committed deploy.
//
// Returns the new operation id.
func HandoffDeploy(ctx context.Context, pool *pgxpool.Pool, b *Build, repo *Repo, imageURI string, det DeployDetection, dd DefaultDomainOpts) (uuid.UUID, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin deploy tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	deployTrigger := b.Trigger
	switch deployTrigger {
	case "push", "pr", "manual", "rollback", "promote":
	default:
		deployTrigger = "push"
	}

	var deployID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO deployments (environment_id, app_name, build_id, image_uri, trigger, deployed_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, b.EnvironmentID, b.AppName, b.ID, imageURI, deployTrigger, SystemUserID).Scan(&deployID); err != nil {
		return uuid.Nil, fmt.Errorf("insert deployment: %w", err)
	}

	var appExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM resource_snapshots
			WHERE environment_id = $1 AND kind = 'App' AND name = $2
			  AND summary_json ? 'image'
		)
	`, b.EnvironmentID, b.AppName).Scan(&appExists); err != nil {
		return uuid.Nil, fmt.Errorf("check app existence: %w", err)
	}

	var action string
	var payload []byte
	var defaultHostname string
	deployPort := repo.Port
	if det.Port > 0 {
		deployPort = det.Port
	}
	deployPort = workerPort(deployPort, repo.Worker)
	if appExists {
		action = "DeployImageVersion"
		payload, err = json.Marshal(deployImageVersionPayload{
			AppName:   b.AppName,
			Image:     imageURI,
			Framework: det.Framework,
			Port:      workerPort(det.Port, repo.Worker),
		})
	} else {
		action = "CreateApp"
		var hasManagedDomain bool
		_ = tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM domain_hostnames
				WHERE environment_id = $1 AND app_name = $2 AND managed = true
			)
		`, b.EnvironmentID, b.AppName).Scan(&hasManagedDomain)
		if dd.Enabled && dd.Base != "" && !hasManagedDomain && !repo.Worker {
			if suffix, sErr := randomHostSuffix(); sErr == nil {
				isEphemeral, headBranch, pErr := EnvPreviewInfo(ctx, tx, b.EnvironmentID)
				if pErr == nil && isEphemeral && headBranch != "" {
					defaultHostname = buildPreviewHostname(dd.Base, b.AppName, headBranch, suffix)
				} else {
					defaultHostname = buildDefaultHostname(dd.Base, b.AppName, suffix)
				}
			}
		}
		payload, err = json.Marshal(createAppPayload{
			Name:            b.AppName,
			Image:           imageURI,
			Framework:       det.Framework,
			Port:            deployPort,
			Replicas:        workerReplicas(repo.Replicas, repo.Worker),
			Profile:         repo.Profile,
			DefaultHostname: defaultHostname,
			Worker:          repo.Worker,
		})
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal %s payload: %w", action, err)
	}

	actor, initiator := handoffActor(b, repo)

	var opID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, $3, $4, 'App', $5, 'Created', $6)
		RETURNING id
	`, actor, repo.ProjectID, b.EnvironmentID, action, b.AppName, payload).Scan(&opID); err != nil {
		return uuid.Nil, fmt.Errorf("insert operation: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE deployments SET operation_id = $1 WHERE id = $2`, opID, deployID); err != nil {
		return uuid.Nil, fmt.Errorf("link operation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit deploy: %w", err)
	}

	optionalDeploySideEffects(ctx, pool, b, repo, opID, action, payload, defaultHostname, actor, initiator)
	return opID, nil
}

// Initiator labels record HOW a deploy started, separately from WHO owns it.
// Attribution used to carry both meanings in actor_id alone, which cost the
// funnel its most active users (see handoffActor).
const (
	initiatorManual = "manual"
	initiatorPush   = "push"
	initiatorSystem = "system"
)

// handoffActor picks who the CreateApp/DeployImageVersion operation and audit
// row should be attributed to, and how the deploy was started.
//
// A manual build (triggered_by set) is a real console click and always wins.
// Otherwise the deploy is attributed to whoever connected the repo
// (git_repos.created_by): no user is in the loop at push time, but a human
// caused this pipeline to exist, and it keeps running because they keep
// pushing. Only a repo with no created_by at all (connected before migration
// 037) is genuinely ownerless and lands on the system actor.
//
// This used to fall back to created_by for the first build only, so every
// push-triggered redeploy of an existing app was attributed to the system
// user -- which cohort analysis excludes as synthetic. The effect was that the
// most engaged users, the ones deploying continuously from git, were subtracted
// from the funnel: an account could ship for weeks and still read as "triggered
// one build, then silence". Manual and push are still separable, but through
// the initiator label instead of by discarding the owner.
func handoffActor(b *Build, repo *Repo) (uuid.UUID, string) {
	if b.TriggeredBy != nil {
		return *b.TriggeredBy, initiatorManual
	}
	if repo.CreatedBy != nil {
		return *repo.CreatedBy, initiatorPush
	}
	return SystemUserID, initiatorSystem
}

// optionalDeploySideEffects records the audit event and the surrogate default
// hostname AFTER the deploy transaction has committed. Each runs on its own
// statement so a failure (a missing grant, a half-configured default-domain
// feature) only loses that side effect and can never roll back the deploy that
// already succeeded. Inside the deploy tx a single failing INSERT would poison
// the whole transaction and drop the operation, leaving a successful build
// stuck NotDeployed.
//
// The audit row carries environment_id: this is the path every git deploy takes
// (push and manual alike), so without it the terminal step of the funnel cannot
// be attributed to an environment at all, and a deploy into a preview reads the
// same as a deploy into prod.
func optionalDeploySideEffects(ctx context.Context, pool *pgxpool.Pool, b *Build, repo *Repo, opID uuid.UUID, action string, payload []byte, defaultHostname string, actor uuid.UUID, initiator string) {
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events (actor_id, project_id, environment_id, operation_id, action, resource_kind, resource_name, metadata)
		VALUES ($1, $2, $3, $4, $5, 'App', $6, $7::jsonb || jsonb_build_object('initiator', $8::text))
	`, actor, repo.ProjectID, b.EnvironmentID, opID, action, b.AppName, payload, initiator); err != nil {
		log.Warn().Err(err).Str("app", b.AppName).Str("operation", opID.String()).Msg("deploy audit event insert failed (deploy already committed)")
	}

	if defaultHostname != "" {
		if _, err := pool.Exec(ctx, `
			INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, operation_id, managed)
			VALUES (NULL, $1, $2, $3, 'CNAME', 'pending', 'pending', $4, true)
		`, b.EnvironmentID, b.AppName, defaultHostname, opID); err != nil {
			log.Warn().Err(err).Str("app", b.AppName).Str("hostname", defaultHostname).Msg("default surrogate hostname insert failed (deploy already committed; app deploys without an auto domain)")
		}
	}
}

// LatestImageForBranch returns the image_uri of the most recent successful build
// on a repo+branch, if any. Used by the cache-warm/cache-ref decision; harmless
// when absent.
func LatestImageForBranch(ctx context.Context, pool *pgxpool.Pool, gitRepoID uuid.UUID, branch string) (string, error) {
	var uri string
	err := pool.QueryRow(ctx, `
		SELECT image_uri FROM builds
		WHERE  git_repo_id = $1 AND branch = $2 AND status = 'success' AND image_uri IS NOT NULL
		ORDER  BY created_at DESC
		LIMIT  1
	`, gitRepoID, branch).Scan(&uri)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("latest image: %w", err)
	}
	return uri, nil
}
