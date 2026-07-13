package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func buildDefaultHostname(base, name, suffix string) string {
	label := name
	maxLabel := 63 - 1 - len(suffix)
	if len(label) > maxLabel {
		label = strings.TrimRight(label[:maxLabel], "-")
	}
	return fmt.Sprintf("%s-%s.%s", label, suffix, base)
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

	// Does the app already exist in this environment?
	var appExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM resource_snapshots
			WHERE environment_id = $1 AND kind = 'App' AND name = $2
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
	if appExists {
		action = "DeployImageVersion"
		payload, err = json.Marshal(deployImageVersionPayload{
			AppName:   b.AppName,
			Image:     imageURI,
			Framework: det.Framework,
			Port:      det.Port,
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
		if dd.Enabled && dd.Base != "" && !hasManagedDomain {
			if suffix, sErr := randomHostSuffix(); sErr == nil {
				defaultHostname = buildDefaultHostname(dd.Base, b.AppName, suffix)
			}
		}
		payload, err = json.Marshal(createAppPayload{
			Name:            b.AppName,
			Image:           imageURI,
			Framework:       det.Framework,
			Port:            deployPort,
			Replicas:        repo.Replicas,
			Profile:         repo.Profile,
			DefaultHostname: defaultHostname,
		})
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal %s payload: %w", action, err)
	}

	var opID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		VALUES ($1, $2, $3, $4, 'App', $5, 'Created', $6)
		RETURNING id
	`, SystemUserID, repo.ProjectID, b.EnvironmentID, action, b.AppName, payload).Scan(&opID); err != nil {
		return uuid.Nil, fmt.Errorf("insert operation: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE deployments SET operation_id = $1 WHERE id = $2`, opID, deployID); err != nil {
		return uuid.Nil, fmt.Errorf("link operation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit deploy: %w", err)
	}

	optionalDeploySideEffects(ctx, pool, b, repo, opID, action, payload, defaultHostname)
	return opID, nil
}

// optionalDeploySideEffects records the audit event and the surrogate default
// hostname AFTER the deploy transaction has committed. Each runs on its own
// statement so a failure (a missing grant, a half-configured default-domain
// feature) only loses that side effect and can never roll back the deploy that
// already succeeded. Inside the deploy tx a single failing INSERT would poison
// the whole transaction and drop the operation, leaving a successful build
// stuck NotDeployed.
func optionalDeploySideEffects(ctx context.Context, pool *pgxpool.Pool, b *Build, repo *Repo, opID uuid.UUID, action string, payload []byte, defaultHostname string) {
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		VALUES ($1, $2, $3, $4, 'App', $5, $6)
	`, SystemUserID, repo.ProjectID, opID, action, b.AppName, payload); err != nil {
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
