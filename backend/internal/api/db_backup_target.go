package api

import (
	"context"
	"log"
	"strings"
)

// backupStatefulSetFor answers which Postgres instance a database's backup and
// restore must run against.
//
// The blueprint derives PGHOST (and its admin secret) from the ActionSet's
// target StatefulSet, so this single name decides which instance is read. A
// sharded fleet makes the old answer - one instance from config - actively
// dangerous rather than merely wrong: a database that has moved to another
// shard still has its abandoned pre-move copy sitting on the old instance, so a
// backup aimed there succeeds and silently captures stale data. Resolving the
// target from the same placement the router uses is what keeps "backed up" and
// "the data clients read" the same database.
//
// Falls back to the configured default whenever placement cannot be resolved:
// an unknown shard, a shard with no registry row, or a host that does not look
// like a Kubernetes name. Reading the default instance is what happened before
// shards existed, so an unresolvable database degrades to the old behaviour
// instead of losing its backup entirely.
func (h *Handler) backupStatefulSetFor(ctx context.Context, datname string) string {
	fallback := h.cfg.DBBackupStatefulSet
	if datname == "" {
		return fallback
	}

	var host string
	err := h.pool.QueryRow(ctx, `
		WITH placement AS (
			SELECT COALESCE(
				(SELECT m.target_shard
				   FROM db_moves m
				  WHERE m.datname = $1 AND m.phase IN ('cutover', 'done')
				  ORDER BY m.updated_at DESC
				  LIMIT 1),
				(SELECT NULLIF(rs.summary_json->>'shard', '')
				   FROM resource_snapshots rs
				  WHERE rs.kind = 'ServiceDatabaseV2'
				    AND COALESCE(rs.summary_json->'spec'->>'database', rs.name) = $1
				  LIMIT 1),
				$2
			) AS shard
		)
		SELECT s.host FROM db_shards s JOIN placement p ON p.shard = s.name
	`, datname, dbRouterDefaultShard).Scan(&host)
	if err != nil {
		log.Printf("db-backup: resolve target shard for %q: %v (falling back to %s)", datname, err, fallback)
		return fallback
	}

	name := statefulSetNameFromHost(host)
	if name == "" {
		log.Printf("db-backup: shard host %q for %q is not a service name (falling back to %s)", host, datname, fallback)
		return fallback
	}
	return name
}

// statefulSetNameFromHost peels the workload name off a shard's service host.
// Every shard is a Bitnami postgresql release whose Service, StatefulSet and
// admin Secret share one name, which is what lets the blueprint find the
// password by the same handle it finds the host, and lets the registry stay the
// single place a shard's address is written down.
//
// Returns "" for anything that is not a plain DNS label, so a hand-edited
// registry row cannot steer a backup at an arbitrary object.
func statefulSetNameFromHost(host string) string {
	label, _, _ := strings.Cut(strings.TrimSpace(host), ".")
	if label == "" || len(label) > 63 {
		return ""
	}
	for i, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i != 0 && i != len(label)-1:
		default:
			return ""
		}
	}
	return label
}
