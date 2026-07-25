package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UpsertSnapshot upserts a resource_snapshots row using LWW:
// the update is skipped if the existing last_synced_at is newer than syncedAt.
func UpsertSnapshot(ctx context.Context, pool *pgxpool.Pool,
	projectID uuid.UUID, environmentID *uuid.UUID,
	kind, name, phase string,
	summaryJSON json.RawMessage,
	syncedAt time.Time,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO resource_snapshots
			(project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (project_id, environment_id, kind, name) DO UPDATE
		SET phase          = EXCLUDED.phase,
		    summary_json   = EXCLUDED.summary_json,
		    last_synced_at = EXCLUDED.last_synced_at
		WHERE resource_snapshots.last_synced_at < EXCLUDED.last_synced_at
	`, projectID, environmentID, kind, name, phase, summaryJSON, syncedAt)
	if err != nil {
		return fmt.Errorf("upsert snapshot: %w", err)
	}
	return nil
}

// SnapshotHasImage reports whether a resource_snapshots row already exists for
// the given identity and carries a non-empty top-level "image" field in its
// summary_json. Used by the git watcher's syncAppFile to decide whether its
// own inherently bare payload (git_sha/git_message/app_name only, never image)
// is allowed to write: a bare git-stub sync must never downgrade a snapshot
// that already carries a real app spec, no matter how the two writers' commit
// timestamps compare.
func SnapshotHasImage(ctx context.Context, pool *pgxpool.Pool,
	projectID uuid.UUID, environmentID *uuid.UUID, kind, name string,
) (bool, error) {
	var image string
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(summary_json->>'image', '')
		FROM resource_snapshots
		WHERE project_id = $1 AND environment_id IS NOT DISTINCT FROM $2 AND kind = $3 AND name = $4
	`, projectID, environmentID, kind, name).Scan(&image)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check snapshot image: %w", err)
	}
	return image != "", nil
}

// DeleteSnapshot removes a resource_snapshots row by identity. Used to purge
// synthetic rows the console must not display (e.g. resources-only owner apps
// that carry standalone DB/S3/model CRs but are not user workloads). Returns the
// number of rows removed (0 = nothing to delete).
func DeleteSnapshot(ctx context.Context, pool *pgxpool.Pool,
	projectID uuid.UUID, environmentID *uuid.UUID, kind, name string,
) (int64, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM resource_snapshots
		WHERE project_id = $1 AND environment_id IS NOT DISTINCT FROM $2 AND kind = $3 AND name = $4
	`, projectID, environmentID, kind, name)
	if err != nil {
		return 0, fmt.Errorf("delete snapshot: %w", err)
	}
	return tag.RowsAffected(), nil
}

// GCAppSnapshot is one k8s App snapshot considered by the orphan garbage
// collector, joined to the slugs needed to resolve its git path.
type GCAppSnapshot struct {
	ID           uuid.UUID
	ProjectID    uuid.UUID
	EnvID        uuid.UUID
	ProjectSlug  string
	EnvSlug      string
	Name         string
	Phase        string
	LastSyncedAt time.Time
	OrphanedAt   *time.Time
}

// ListGCAppSnapshots returns every App snapshot in a k8s-runtime environment,
// with the project/env slugs and the orphaned_at marker (summary_json.orphaned_at)
// the two-stage GC needs. VM (compose) envs are excluded: their apps are
// DB-authoritative, not git-backed, so the git-absence orphan test never applies.
func ListGCAppSnapshots(ctx context.Context, pool *pgxpool.Pool) ([]GCAppSnapshot, error) {
	rows, err := pool.Query(ctx, `
		SELECT rs.id, rs.project_id, rs.environment_id, p.name, e.name,
		       rs.name, rs.phase, rs.last_synced_at,
		       (rs.summary_json->>'orphaned_at')::timestamptz
		FROM resource_snapshots rs
		JOIN projects p     ON p.id = rs.project_id
		JOIN environments e ON e.id = rs.environment_id
		WHERE rs.kind = 'App' AND e.runtime = 'k8s'
	`)
	if err != nil {
		return nil, fmt.Errorf("list gc app snapshots: %w", err)
	}
	defer rows.Close()

	var out []GCAppSnapshot
	for rows.Next() {
		var s GCAppSnapshot
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.EnvID, &s.ProjectSlug, &s.EnvSlug,
			&s.Name, &s.Phase, &s.LastSyncedAt, &s.OrphanedAt); err != nil {
			return nil, fmt.Errorf("scan gc app snapshot: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GCChildSnapshot is one child resource snapshot (PublicApi, Ingress,
// ServiceDatabaseV2, S3Bucket, AIModel) considered by the orphan GC, with the
// signals the sweep needs precomputed: ParentExists (a non-Orphaned App snapshot
// in the same project+env claims it via any owning-link key) and SearchTerms
// (the snapshot name plus git-native spellings like the dotted fqdn — a
// PublicApi/Ingress snapshot is named after the DASHED fqdn while git manifests
// carry the dotted form, so a name-only scan would miss a live git-backed
// domain).
type GCChildSnapshot struct {
	ID           uuid.UUID
	ProjectID    uuid.UUID
	EnvID        uuid.UUID
	ProjectSlug  string
	EnvSlug      string
	Kind         string
	Name         string
	Phase        string
	LastSyncedAt time.Time
	OrphanedAt   *time.Time
	ParentExists bool
	SearchTerms  []string
}

// ListGCChildSnapshots returns every child snapshot of the given kinds in a
// k8s-runtime environment. The parent-link predicate mirrors doDeleteApp's
// cascade WHERE clause (dbwatcher.go), including the spec.upstream.serviceName
// = "<app>-service" match that covers PublicApi rows whose app_name was
// corrupted by the pre-faf5bb5 status reconciler. A parent that is itself
// phase=Orphaned does not count: children of a soft-deleted app must start
// their own mark/purge clock instead of being kept alive by a row that is
// already on its way out.
func ListGCChildSnapshots(ctx context.Context, pool *pgxpool.Pool, kinds []string) ([]GCChildSnapshot, error) {
	rows, err := pool.Query(ctx, `
		SELECT rs.id, rs.project_id, rs.environment_id, p.name, e.name,
		       rs.kind, rs.name, rs.phase, rs.last_synced_at,
		       (rs.summary_json->>'orphaned_at')::timestamptz,
		       EXISTS (
		           SELECT 1 FROM resource_snapshots a
		           WHERE a.project_id = rs.project_id
		             AND a.environment_id = rs.environment_id
		             AND a.kind = 'App' AND a.phase <> 'Orphaned'
		             AND (
		                  rs.summary_json->>'app_ref'             = a.name
		               OR rs.summary_json->>'attached_app'        = a.name
		               OR rs.summary_json->>'app_name'            = a.name
		               OR rs.summary_json->'spec'->>'appRef'       = a.name
		               OR rs.summary_json->'spec'->>'attachedApp'  = a.name
		               OR rs.summary_json->'spec'->>'serviceName'  = a.name
		               OR rs.summary_json->'spec'->'upstream'->>'serviceName' = a.name || '-service'
		             )
		       ),
		       COALESCE(rs.summary_json->'spec'->'dns'->>'fqdn', ''),
		       COALESCE(rs.summary_json->'spec'->'rules'->0->>'host', '')
		FROM resource_snapshots rs
		JOIN projects p     ON p.id = rs.project_id
		JOIN environments e ON e.id = rs.environment_id
		WHERE rs.kind = ANY($1) AND e.runtime = 'k8s'
	`, kinds)
	if err != nil {
		return nil, fmt.Errorf("list gc child snapshots: %w", err)
	}
	defer rows.Close()

	var out []GCChildSnapshot
	for rows.Next() {
		var s GCChildSnapshot
		var fqdn, host string
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.EnvID, &s.ProjectSlug, &s.EnvSlug,
			&s.Kind, &s.Name, &s.Phase, &s.LastSyncedAt, &s.OrphanedAt,
			&s.ParentExists, &fqdn, &host); err != nil {
			return nil, fmt.Errorf("scan gc child snapshot: %w", err)
		}
		s.SearchTerms = append(s.SearchTerms, s.Name)
		if fqdn != "" {
			s.SearchTerms = append(s.SearchTerms, fqdn)
		}
		if host != "" {
			s.SearchTerms = append(s.SearchTerms, host)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// MarkSnapshotOrphaned soft-deletes a snapshot: phase=Orphaned and an orphaned_at
// stamp the purge stage measures its grace from. Idempotent — orphaned_at is only
// set when absent, so the purge clock starts once and isn't reset each tick.
func MarkSnapshotOrphaned(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, at time.Time) error {
	patch, _ := json.Marshal(map[string]any{"orphaned_at": at.UTC().Format(time.RFC3339), "live_source": "orphan-gc"})
	_, err := pool.Exec(ctx, `
		UPDATE resource_snapshots
		SET phase        = 'Orphaned',
		    summary_json = COALESCE(summary_json, '{}'::jsonb) || $2::jsonb
		WHERE id = $1
	`, id, patch)
	if err != nil {
		return fmt.Errorf("mark snapshot orphaned: %w", err)
	}
	return nil
}

// ClearSnapshotOrphan reverses a soft-delete when an app comes back (its git
// manifest or a live Deployment reappears): drops the orphaned_at stamp and
// parks phase at Pending so the next live/status pass writes the true state.
func ClearSnapshotOrphan(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE resource_snapshots
		SET phase        = 'Pending',
		    summary_json = (COALESCE(summary_json, '{}'::jsonb) - 'orphaned_at') || '{"live_source":"orphan-gc-cleared"}'::jsonb
		WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("clear snapshot orphan: %w", err)
	}
	return nil
}

// DeleteSnapshotByID physically removes one snapshot row (the GC purge stage).
func DeleteSnapshotByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	_, err := pool.Exec(ctx, `DELETE FROM resource_snapshots WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete snapshot by id: %w", err)
	}
	return nil
}

// AppSnapshotRef identifies one resource_snapshots row by id, kind, and name —
// used by MoveApp's guard (does a ServiceDatabaseV2 child exist?) and repoint
// (which rows must move to the target project/environment).
type AppSnapshotRef struct {
	ID   uuid.UUID
	Kind string
	Name string
}

// AppMoveSnapshots returns the App row itself plus every child resource_snapshots
// row owned by appName in (projectID, environmentID). It mirrors doDeleteApp's
// cascade WHERE clause (owning-app signal: top-level app_ref/attached_app, or CR
// spec.appRef/spec.attachedApp/spec.serviceName), with the App row added by name
// match. Used both as MoveApp's defense-in-depth stateful guard (does a
// ServiceDatabaseV2 entry exist?) and as the exact row set its snapshot-repoint
// step must move.
func AppMoveSnapshots(ctx context.Context, pool *pgxpool.Pool, projectID, environmentID uuid.UUID, appName string) ([]AppSnapshotRef, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, kind, name FROM resource_snapshots
		WHERE project_id = $1 AND environment_id = $2
		  AND (
		       (kind = 'App' AND name = $3)
		    OR (kind <> 'App' AND (
		           summary_json->>'app_ref'             = $3
		        OR summary_json->>'attached_app'        = $3
		        OR summary_json->>'app_name'            = $3
		        OR summary_json->'spec'->>'appRef'       = $3
		        OR summary_json->'spec'->>'attachedApp'  = $3
		        OR summary_json->'spec'->>'serviceName'  = $3
		        OR summary_json->'spec'->'upstream'->>'serviceName' = $3 || '-service'
		       ))
		  )
	`, projectID, environmentID, appName)
	if err != nil {
		return nil, fmt.Errorf("query app move snapshots: %w", err)
	}
	defer rows.Close()

	var out []AppSnapshotRef
	for rows.Next() {
		var ref AppSnapshotRef
		if err := rows.Scan(&ref.ID, &ref.Kind, &ref.Name); err != nil {
			return nil, fmt.Errorf("scan app move snapshot: %w", err)
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// UpdateLiveStatus mirrors live cluster state onto an existing snapshot of the
// given kind: it sets phase and merges the given fields into summary_json (jsonb
// concat preserves git_sha/message etc.). It only touches rows that already
// exist, so it never resurrects a resource the git/db sync removed. Returns the
// number of rows updated (0 = no matching snapshot).
func UpdateLiveStatus(ctx context.Context, pool *pgxpool.Pool,
	environmentID uuid.UUID, kind, name, phase string, summaryPatch json.RawMessage,
) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE resource_snapshots
		SET phase          = $1,
		    summary_json   = COALESCE(summary_json, '{}'::jsonb) || $2::jsonb,
		    last_synced_at = now()
		WHERE environment_id = $3 AND kind = $4 AND name = $5
	`, phase, summaryPatch, environmentID, kind, name)
	if err != nil {
		return 0, fmt.Errorf("update live status: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PrimaryHostname returns the hostname to surface on an app's card, or ""
// if the app has none. Preference order within domain_hostnames: an active
// custom domain over an active surrogate (rows whose hostname ends in
// domainBase are surrogates), then any row at all so a pending domain still
// surfaces something. When the app has no domain_hostnames row whatsoever, it
// falls back to the fqdn of a PublicApi snapshot in the same environment
// tagged with this app_name: domains that entered the platform straight
// through git (hand-recovered apps, manifests committed outside the console
// API) exist only as PublicApi snapshots, and without this fallback such an
// app never gets a url even though its domain is live.
func PrimaryHostname(ctx context.Context, pool *pgxpool.Pool,
	environmentID uuid.UUID, appName, domainBase string,
) (string, error) {
	var hostname string
	err := pool.QueryRow(ctx, `
		SELECT hostname
		FROM domain_hostnames
		WHERE environment_id = $1 AND app_name = $2
		ORDER BY
			(status = 'active') DESC,
			(right(hostname, length($3::text)) <> $3::text) DESC,
			created_at ASC
		LIMIT 1
	`, environmentID, appName, domainBase).Scan(&hostname)
	if err == nil {
		return hostname, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("primary hostname: %w", err)
	}
	err = pool.QueryRow(ctx, `
		SELECT summary_json->'spec'->'dns'->>'fqdn'
		FROM resource_snapshots
		WHERE environment_id = $1 AND kind = 'PublicApi'
		  AND summary_json->>'app_name' = $2
		  AND COALESCE(summary_json->'spec'->'dns'->>'fqdn', '') <> ''
		ORDER BY (name = $2) DESC, name ASC
		LIMIT 1
	`, environmentID, appName).Scan(&hostname)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("primary hostname publicapi fallback: %w", err)
	}
	return hostname, nil
}
