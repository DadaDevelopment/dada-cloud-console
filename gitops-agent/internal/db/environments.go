package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// K8sEnvironment is the minimal env shape the status reconciler needs: which
// namespace to read Deployments from and which project/env the snapshots belong
// to.
type K8sEnvironment struct {
	ProjectID uuid.UUID
	EnvID     uuid.UUID
	Namespace string
	Name      string
}

// ListK8sEnvironments returns every k8s-runtime environment with a namespace.
// VM-runtime envs are excluded — their app state is owned by the portainer-agent.
func ListK8sEnvironments(ctx context.Context, pool *pgxpool.Pool) ([]K8sEnvironment, error) {
	rows, err := pool.Query(ctx, `
		SELECT project_id, id, namespace, name
		FROM environments
		WHERE runtime = 'k8s' AND namespace <> ''
		ORDER BY namespace
	`)
	if err != nil {
		return nil, fmt.Errorf("list k8s environments: %w", err)
	}
	defer rows.Close()

	var envs []K8sEnvironment
	for rows.Next() {
		var e K8sEnvironment
		if err := rows.Scan(&e.ProjectID, &e.EnvID, &e.Namespace, &e.Name); err != nil {
			return nil, fmt.Errorf("scan environment: %w", err)
		}
		envs = append(envs, e)
	}
	return envs, rows.Err()
}

// EnvProject identifies an environment and its owning project.
type EnvProject struct {
	ProjectID uuid.UUID
	EnvID     uuid.UUID
}

// EnvProjects maps every environment id to its project id (all runtimes). Used to
// resolve a snapshot's existing environment back to its project during cluster
// discovery.
func EnvProjects(ctx context.Context, pool *pgxpool.Pool) (map[uuid.UUID]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `SELECT id, project_id FROM environments`)
	if err != nil {
		return nil, fmt.Errorf("env projects: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]uuid.UUID{}
	for rows.Next() {
		var env, proj uuid.UUID
		if err := rows.Scan(&env, &proj); err != nil {
			return nil, fmt.Errorf("scan env project: %w", err)
		}
		out[env] = proj
	}
	return out, rows.Err()
}

// PlatformTarget returns the project + an environment to attach cluster-only
// resources that can't be mapped to a normal project env (infra PublicApis,
// shared databases, etc). Prefers project "platform" and its prod env. ok=false
// when no such project/env exists.
func PlatformTarget(ctx context.Context, pool *pgxpool.Pool) (EnvProject, bool, error) {
	var ep EnvProject
	err := pool.QueryRow(ctx, `
		SELECT p.id, e.id
		FROM projects p JOIN environments e ON e.project_id = p.id
		WHERE p.name = 'platform'
		ORDER BY (e.name = 'prod') DESC, e.name
		LIMIT 1
	`).Scan(&ep.ProjectID, &ep.EnvID)
	if err != nil {
		return EnvProject{}, false, nil //nolint:nilerr // absence is not an error
	}
	return ep, true, nil
}

// AppSnapshotEnvs is SnapshotEnvsByKind for kind='App'.
func AppSnapshotEnvs(ctx context.Context, pool *pgxpool.Pool) (map[string][]uuid.UUID, error) {
	return SnapshotEnvsByKind(ctx, pool, "App")
}

// AppSnapshotEnvsIncludingOrphaned is the fallback counterpart to
// AppSnapshotEnvs: same lookup, same kind='App', but keeps Orphaned rows in
// the candidate set. The status reconciler consults this only after the
// exclusive map already failed to resolve a name — i.e. no live row claims
// it — so it exists to let a live workload re-attach to the one Orphaned
// snapshot that still carries its name, instead of leaving that workload
// unattributed forever. See SnapshotEnvsByKind for why Orphaned is excluded
// by default, and the reconciler's resolveEnv for the three-step order that
// keeps both directions safe.
func AppSnapshotEnvsIncludingOrphaned(ctx context.Context, pool *pgxpool.Pool) (map[string][]uuid.UUID, error) {
	return snapshotEnvsByKind(ctx, pool, "App", true)
}

// SnapshotEnvsByKind maps a snapshot name (of the given kind) to the environment
// IDs that have a snapshot with that name. Used by the status reconciler to
// resolve a workload living in a namespace that isn't its env namespace (e.g.
// AIModel InferenceServices in ml-prod, or App spec.namespace overrides) back to
// its owning environment — but only when the name is unambiguous (one env).
//
// Orphaned rows are excluded. phase='Orphaned' is a soft delete: the app is gone
// from git and has no live pod, and the row survives only so the purge has
// something to grace-period. Counting it as a claimant makes every re-homed app
// permanently ambiguous — its live workload is then attributed to no
// environment at all, so the surviving snapshot freezes at phase "Unknown" with
// no image and no namespaces, which also blanks the log search that resolves
// namespaces from it. Observed 2026-08-04: moving ten apps from project
// "platform" to "observability" left an Orphaned twin per app and blacked out
// live status for the whole monitoring estate, with the orphan GC unable to
// purge the twins because its own clone could not sync.
//
// This exclusion has a matching failure mode in the other direction, fixed by
// AppSnapshotEnvsIncludingOrphaned: an adopted/infra app (e.g. jenkins) whose
// only manifest lives outside the tenant's own namespace has no other way to
// attribute its live workload than this name lookup. If its one surviving
// snapshot row gets marked Orphaned — by any earlier hiccup, not necessarily a
// real deletion — this exclusive map now returns zero candidates for its name
// forever, the reconciler can never mark it live again, and the orphan GC
// purges the snapshot for real on the next sweep: a live app erased from the
// console because a soft-delete flag became a one-way door. Observed
// 2026-08-08/09: jenkins, nexus, portainer, neo4j and others in project
// "platform" were purged this way despite running pods the whole time.
func SnapshotEnvsByKind(ctx context.Context, pool *pgxpool.Pool, kind string) (map[string][]uuid.UUID, error) {
	return snapshotEnvsByKind(ctx, pool, kind, false)
}

func snapshotEnvsByKind(ctx context.Context, pool *pgxpool.Pool, kind string, includeOrphaned bool) (map[string][]uuid.UUID, error) {
	query := `
		SELECT name, environment_id
		FROM resource_snapshots
		WHERE kind = $1 AND environment_id IS NOT NULL AND phase <> 'Orphaned'
	`
	if includeOrphaned {
		query = `
			SELECT name, environment_id
			FROM resource_snapshots
			WHERE kind = $1 AND environment_id IS NOT NULL
		`
	}
	rows, err := pool.Query(ctx, query, kind)
	if err != nil {
		return nil, fmt.Errorf("list snapshot envs: %w", err)
	}
	defer rows.Close()

	out := map[string][]uuid.UUID{}
	for rows.Next() {
		var name string
		var envID uuid.UUID
		if err := rows.Scan(&name, &envID); err != nil {
			return nil, fmt.Errorf("scan app snapshot env: %w", err)
		}
		out[name] = append(out[name], envID)
	}
	return out, rows.Err()
}
