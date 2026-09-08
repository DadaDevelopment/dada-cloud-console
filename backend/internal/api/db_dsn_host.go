package api

import (
	"context"
	"log"
	"strings"
)

// managedDBInternalDomains are the suffixes of the platform's own database
// namespace. Every managed PostgreSQL instance lives behind one of them, and
// none of them is a name a tenant may hold a connection to: the shard a
// database sits on is placement, not an address, and it changes under a move
// (dbmove) or a rename of the source without the tenant's DSN ever being
// rewritten. A tenant that captured the shard address keeps connecting after
// the move -- successfully, to an instance that no longer holds the data
// (incident client-a/face-api 2026-08-10: live connection, empty database).
//
// The shortest form is listed too because the connection secret's endpoint is
// whatever the ServiceDatabaseV2 composition wrote, not a form this code
// controls.
var managedDBInternalDomains = []string{
	".databases.svc.cluster.local",
	".databases.svc",
	".databases",
}

// isManagedShardHost reports whether host names a platform-internal Postgres
// instance rather than the endpoint tenants are meant to hold. The router
// itself (both its in-cluster Service name and its TLS hostname) is the one
// internal name that is deliberately public, so it is excluded; so is its admin
// Service, which the console dials directly during a cutover.
func isManagedShardHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if h == "" || h == pgRouterInternalHost || h == strings.ToLower(pgRouterTLSHostname) {
		return false
	}
	if strings.HasPrefix(h, "pg-router.") || strings.HasPrefix(h, "pg-router-admin.") {
		return false
	}
	for _, suffix := range managedDBInternalDomains {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

// routerReachesDatname reports whether pg-router's rendered table sends datname
// to the instance that actually holds it. It mirrors renderPgBouncerRoutes'
// decisions exactly, because the question it answers is "would collapsing this
// DSN onto the router keep the client on the same data?", and any disagreement
// with the renderer answers it wrong.
//
// Unknown answers are false. A database with no placement, an ambiguous name
// (the renderer sends those to the wildcard), a shard with no address, or a
// name the config grammar cannot express all mean the router cannot be trusted
// to land this client on the right instance -- and connecting to the wrong
// instance succeeds silently, which is worse than handing out an address that
// merely offends a lint.
func routerReachesDatname(shards []shardAddr, placements []dbPlacement, defaultShard, datname string) bool {
	if datname == "" || defaultShard == "" {
		return false
	}
	addressed := make(map[string]bool, len(shards))
	for _, s := range shards {
		if s.Host != "" {
			addressed[s.Name] = true
		}
	}
	if !addressed[defaultShard] {
		return false
	}

	on := map[string]bool{}
	for _, p := range placements {
		if p.Datname != datname {
			continue
		}
		shard := p.Shard
		if shard == "" {
			shard = defaultShard
		}
		on[shard] = true
	}
	if len(on) != 1 {
		return false
	}
	for shard := range on {
		if shard == defaultShard {
			return true
		}
		return addressed[shard] && safeRouteToken(shard) && safeRouteToken(datname)
	}
	return false
}

// managedDBClientHost maps the endpoint the composition published into the
// endpoint a tenant is handed. A shard address collapses onto the router; every
// other host (the router itself, an external endpoint, an in-namespace
// fallback) passes through untouched.
//
// Collapsing is conditional on the router demonstrably covering this database,
// checked against the same registry the router renders its table from. When it
// does not -- or when the registry cannot be read at all -- the raw host is
// returned and the reason is logged: an address that trips the repo's
// pre-commit hook and the db-dsn-no-shard-host admission policy is a visible
// defect, whereas a DSN silently pointed at the wrong instance is not.
func (h *Handler) managedDBClientHost(ctx context.Context, host, datname string) string {
	if !isManagedShardHost(host) {
		return host
	}
	if h == nil || h.pool == nil {
		log.Printf("db-dsn-host: no registry access, handing out shard address %q for %q", host, datname)
		return host
	}
	shards, err := h.routerShards(ctx)
	if err != nil {
		log.Printf("db-dsn-host: read shards for %q: %v; handing out shard address %q", datname, err, host)
		return host
	}
	placements, err := h.routerPlacements(ctx)
	if err != nil {
		log.Printf("db-dsn-host: read placements for %q: %v; handing out shard address %q", datname, err, host)
		return host
	}
	if overrides, err := h.routerMoveOverrides(ctx); err != nil {
		log.Printf("db-dsn-host: read move overrides for %q: %v; using snapshot placement", datname, err)
	} else {
		placements = applyMoveOverrides(placements, overrides)
	}
	if !routerReachesDatname(shards, placements, dbRouterDefaultShard, datname) {
		log.Printf("db-dsn-host: pg-router has no route to %q; handing out shard address %q", datname, host)
		return host
	}
	return pgRouterInternalHost
}

// managedDBDSN is the one way this package turns a connection secret into a
// connection string handed to anyone outside the platform. It exists so the
// shard-to-router mapping cannot be forgotten at a call site: postgresDSN alone
// renders whatever host it is given.
func (h *Handler) managedDBDSN(ctx context.Context, username, password, host, port, datname string) string {
	return postgresDSN(username, password, h.managedDBClientHost(ctx, host, datname), port, datname)
}
