// Package dbtest holds helpers shared by the real-database integration tests.
//
// It exists because tests run against the same cloud-console database as
// production: a cleanup that silently fails leaves rows behind forever. One
// such leftover -- project volume-export-test-8b671f4e with a phase-less App
// snapshot, seeded 2026-07-31 -- made /admin/overview answer 500 for every
// caller until migration 096 landed.
package dbtest

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxCascadeDepth bounds the walk below. Four levels covers the deepest real
// chain (projects -> operations -> audit_events) with room to spare, and keeps
// a self-referencing column such as environments.parent_env_id from recursing
// forever.
const maxCascadeDepth = 4

type foreignKey struct {
	child     string
	childCol  string
	parent    string
	parentCol string
}

// blockingKeysQuery lists the foreign keys that stop a DELETE.
//
// Most children of projects and environments carry ON DELETE CASCADE, so a bare
// `DELETE FROM projects` looks like it works until it does not: db_backups,
// operations, audit_events and resource_snapshots point at projects with NO
// ACTION, and audit_events, deployments, git_commits and domain_hostnames point
// at operations the same way. Postgres answers 23503, and the usual
// `_, _ = pool.Exec(...)` cleanup swallows it.
//
// The list is read from the live catalog instead of being spelled out here so
// that a migration adding the next NO ACTION reference does not quietly start
// leaking rows again.
const blockingKeysQuery = `
	SELECT c.conrelid::regclass::text,
	       ca.attname,
	       c.confrelid::regclass::text,
	       pa.attname
	FROM pg_constraint c
	JOIN LATERAL unnest(c.conkey, c.confkey) AS k(child_attnum, parent_attnum) ON true
	JOIN pg_attribute ca ON ca.attrelid = c.conrelid AND ca.attnum = k.child_attnum
	JOIN pg_attribute pa ON pa.attrelid = c.confrelid AND pa.attnum = k.parent_attnum
	WHERE c.contype = 'f'
	  AND c.confdeltype IN ('a', 'r')
	  AND c.connamespace = 'public'::regnamespace`

// DropProject removes a seeded test project together with every row that would
// block its deletion, and reports whether the project is gone.
//
// Tests normally call it from t.Cleanup and ignore the result; a caller that
// wants to assert the database was left clean can check it.
func DropProject(pool *pgxpool.Pool, projectID uuid.UUID) bool {
	return dropRow(pool, "projects", projectID)
}

// DropUser is DropProject for the throwaway actor a test seeds so that
// operations.actor_id and audit_events.actor_id have something to point at.
// Both of those references are NO ACTION, so the bare user delete every test
// used to run was refused and left the row behind.
func DropUser(pool *pgxpool.Pool, userID uuid.UUID) bool {
	return dropRow(pool, "users", userID)
}

func dropRow(pool *pgxpool.Pool, table string, id uuid.UUID) bool {
	ctx := context.Background()
	keys, err := blockingKeys(ctx, pool)
	if err != nil {
		return false
	}
	dropReferencing(ctx, pool, keys, table, "id = $1", id, 0)
	tag, err := pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, table), id)
	return err == nil && tag.RowsAffected() > 0
}

// DropProjectsByName is DropProject for tests that never learn the id because
// the project is created by the handler under test and identified only by its
// slug.
func DropProjectsByName(pool *pgxpool.Pool, name string) {
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT id FROM projects WHERE name = $1`, name)
	if err != nil {
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		DropProject(pool, id)
	}
}

func blockingKeys(ctx context.Context, pool *pgxpool.Pool) ([]foreignKey, error) {
	rows, err := pool.Query(ctx, blockingKeysQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []foreignKey
	for rows.Next() {
		var fk foreignKey
		if err := rows.Scan(&fk.child, &fk.childCol, &fk.parent, &fk.parentCol); err != nil {
			return nil, err
		}
		keys = append(keys, fk)
	}
	return keys, rows.Err()
}

// dropReferencing deletes, depth-first, everything that points at the rows of
// table selected by where, so that those rows can be deleted afterwards.
func dropReferencing(ctx context.Context, pool *pgxpool.Pool, keys []foreignKey, table, where string, arg any, depth int) {
	if depth >= maxCascadeDepth {
		return
	}
	for _, fk := range keys {
		if fk.parent != table {
			continue
		}
		childWhere := fmt.Sprintf("%s IN (SELECT %s FROM %s WHERE %s)", fk.childCol, fk.parentCol, table, where)
		if fk.child != table {
			dropReferencing(ctx, pool, keys, fk.child, childWhere, arg, depth+1)
		}
		_, _ = pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s`, fk.child, childWhere), arg)
	}
}
