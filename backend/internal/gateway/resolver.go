// Package gateway is the device-facing telemetry write plane (ADR-012): a
// stateless ingest service that authenticates dmon_ keys, decodes OTLP and
// bespoke-JSON payloads, injects authoritative tenant labels, and forwards
// metrics to Prometheus remote-write and logs to Elasticsearch. It shares the
// security-sensitive primitives (key verify, limiter, sanitize, OTLP decode)
// with the console via internal/telemetry, and reuses the console's
// remote-write / ES writers — no fork.
package gateway

import (
	"context"
	"errors"

	"github.com/dada-tuda/console/backend/internal/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// errKeyUnknown is returned when no monitoring_apps row matches the presented
// key. Callers translate it to 401 without leaking which step failed.
var errKeyUnknown = errors.New("unknown or invalid api key")

// keyRow is one candidate monitoring_apps row for a presented key prefix, with
// the tenant fields joined in. The hash is the salt||digest the key verifies
// against; the rest are the authoritative tenant labels.
type keyRow struct {
	appID     uuid.UUID
	hash      []byte
	scopes    []string
	owner     *uuid.UUID
	projectID uuid.UUID
	envName   string
	appName   string
}

// keyStore yields the candidate rows for an api_key_prefix. Narrow on purpose so
// tests can stub it without faking the whole pgx.Rows surface.
type keyStore interface {
	candidatesByPrefix(ctx context.Context, prefix string) ([]keyRow, error)
}

// introspector resolves a unified sk-dada- key to its principal+tenant data via
// user-service (with local caching). Narrow interface so tests can stub it;
// satisfied by *telemetry.Introspector.
type introspector interface {
	Introspect(ctx context.Context, key string) (telemetry.IntrospectionResult, error)
}

// pgKeyStore is the Postgres-backed keyStore (read-only). It only ever SELECTs
// from monitoring_apps + its joins.
type pgKeyStore struct{ pool *pgxpool.Pool }

// NewPGKeyStore builds the production keyStore over a read-only pool.
func NewPGKeyStore(pool *pgxpool.Pool) keyStore { return pgKeyStore{pool: pool} }

func (s pgKeyStore) candidatesByPrefix(ctx context.Context, prefix string) ([]keyRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT m.id, m.api_key_hash, m.scopes, p.owner_id, m.project_id, e.name, m.name
		   FROM monitoring_apps m
		   JOIN projects p     ON p.id = m.project_id
		   JOIN environments e ON e.id = m.environment_id
		  WHERE m.api_key_prefix = $1`,
		prefix,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []keyRow
	for rows.Next() {
		var r keyRow
		if err := rows.Scan(&r.appID, &r.hash, &r.scopes, &r.owner, &r.projectID, &r.envName, &r.appName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// resolved is a successfully authenticated ingest principal: the monitoring app
// id (for rate limiting), its granted scopes, and the authoritative tenant
// labels to stamp onto every series/log.
type resolved struct {
	appID  uuid.UUID
	scopes []string
	tenant telemetry.Tenant
}

func (r resolved) hasScope(want string) bool {
	for _, s := range r.scopes {
		if s == want {
			return true
		}
	}
	return false
}

// resolveKey authenticates a presented ingest key. A unified sk-dada- key is
// resolved via user-service introspection (intro); a legacy dmon_ key takes the
// ADR-012 path: index the candidates by api_key_prefix (the narrow), then
// constant-time argon2id verify to pick the real match (the decider). Either way
// the tenant labels come only from the authoritative source (introspection or
// the DB row) — never from the request payload — so the stamped label contract
// is identical across both key types.
func resolveKey(ctx context.Context, store keyStore, intro introspector, key string, maxLabels int) (resolved, error) {
	if key == "" {
		return resolved{}, errKeyUnknown
	}
	if telemetry.IsUnifiedKey(key) {
		return resolveUnifiedKey(ctx, intro, key, maxLabels)
	}
	candidates, err := store.candidatesByPrefix(ctx, telemetry.KeyLookupPrefix(key))
	if err != nil {
		return resolved{}, err
	}
	for _, r := range candidates {
		if !telemetry.VerifyKeyHash(key, r.hash) {
			continue // prefix collision or forged key — keep checking candidates
		}
		t := telemetry.Tenant{
			ProjectID:     r.projectID.String(),
			Environment:   r.envName,
			MonitoringApp: r.appName,
			MaxLabels:     maxLabels,
		}
		if r.owner != nil {
			t.OrgID = r.owner.String()
		}
		return resolved{appID: r.appID, scopes: r.scopes, tenant: t}, nil
	}
	return resolved{}, errKeyUnknown
}

// resolveUnifiedKey authenticates a unified sk-dada- key via introspection and
// builds the same resolved struct the dmon_ path returns. The tenant fields are
// mapped 1:1 from the introspection result so the labels stamped on every series
// /log are byte-identical to the legacy path. The per-app rate-limit key is the
// principal id (a UUID) when introspection returns a parseable one; otherwise a
// deterministic UUID derived from the principal id keeps rate limiting stable.
func resolveUnifiedKey(ctx context.Context, intro introspector, key string, maxLabels int) (resolved, error) {
	if intro == nil {
		return resolved{}, errKeyUnknown
	}
	ir, err := intro.Introspect(ctx, key)
	if err != nil {
		return resolved{}, err
	}
	if !ir.Valid {
		return resolved{}, errKeyUnknown
	}
	t := telemetry.Tenant{
		OrgID:         ir.OrgID,
		ProjectID:     ir.ProjectID,
		MonitoringApp: ir.MonitoringApp,
		MaxLabels:     maxLabels,
	}
	appID, err := uuid.Parse(ir.PrincipalID)
	if err != nil {
		appID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(ir.PrincipalID))
	}
	return resolved{appID: appID, scopes: ir.Scopes, tenant: t}, nil
}
