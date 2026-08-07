package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// dbRouterDefaultShard mirrors the ServiceDatabaseV2 XRD default: every
// database created before sharding existed lives on the shared instance and
// carries no spec.shard. It is the shard PgBouncer's "*" wildcard points at, so
// databases sitting there need no line of their own.
const dbRouterDefaultShard = "shard-1"

// dbRouterAuthDBName is the database every auth_query lookup runs against on
// the target shard. pg_authid is cluster-wide, so which database answers is
// irrelevant, but the name has to exist on every shard - "postgres" does.
const dbRouterAuthDBName = "postgres"

// shardAddr is a shard's network address as the registry knows it.
type shardAddr struct {
	Name string
	Host string
	Port int
}

// dbPlacement is one managed database and the shard it actually lives on, as
// reported by the live CR through the resource snapshot.
type dbPlacement struct {
	Datname string
	Shard   string
}

// safeRouteToken rejects anything that could break out of a PgBouncer config
// line. A datname is attacker-influenced (tenants name their databases), and
// the rendered file is included verbatim into pgbouncer.ini, so a name with a
// space, a newline or an "=" would rewrite the routing table rather than add to
// it. Postgres allows such names when quoted; the router simply does not route
// them by name and they fall through to the wildcard.
func safeRouteToken(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// renderPgBouncerRoutes builds the [databases] section the connection router
// includes. Pure, so the routing rule is testable without a cluster.
//
// Only databases that are NOT on the default shard get a line: the wildcard
// already sends everything else to the default instance, which keeps the table
// proportional to how many databases have actually been moved rather than to
// how many exist.
//
// A datname that appears on two shards is dropped, not guessed. Routing is by
// name alone, so two databases with the same name on different instances have
// no correct answer; sending both to the wildcard keeps the pre-move behaviour
// instead of silently connecting one tenant to another tenant's data. The
// dropped name is written into the file as a comment so the condition is
// visible where an operator looks.
//
// Returns an error when the default shard has no usable address: rendering a
// file without the wildcard would make the router reject every database that
// has no explicit line, which is worse than keeping the previous table.
func renderPgBouncerRoutes(shards []shardAddr, placements []dbPlacement, defaultShard string) (string, error) {
	byName := make(map[string]shardAddr, len(shards))
	for _, s := range shards {
		if s.Host == "" {
			continue
		}
		if s.Port == 0 {
			s.Port = 5432
		}
		byName[s.Name] = s
	}

	def, ok := byName[defaultShard]
	if !ok {
		return "", fmt.Errorf("default shard %q has no address in db_shards", defaultShard)
	}

	shardsFor := make(map[string]map[string]bool)
	for _, p := range placements {
		name := p.Datname
		shard := p.Shard
		if shard == "" {
			shard = defaultShard
		}
		if name == "" {
			continue
		}
		if shardsFor[name] == nil {
			shardsFor[name] = map[string]bool{}
		}
		shardsFor[name][shard] = true
	}

	var b strings.Builder
	b.WriteString("; Generated from the db_shards registry. Do not edit by hand:\n")
	b.WriteString("; the console rewrites this file whenever a database is placed or moved.\n")
	b.WriteString("[databases]\n")
	fmt.Fprintf(&b, "* = host=%s port=%d auth_dbname=%s\n", def.Host, def.Port, dbRouterAuthDBName)

	names := make([]string, 0, len(shardsFor))
	for name := range shardsFor {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		on := shardsFor[name]
		if len(on) > 1 {
			fmt.Fprintf(&b, "; %s: same name on %d shards, routed by the wildcard\n", name, len(on))
			continue
		}
		shard := ""
		for s := range on {
			shard = s
		}
		if shard == defaultShard {
			continue
		}
		addr, ok := byName[shard]
		if !ok {
			fmt.Fprintf(&b, "; %s: shard %s has no address, routed by the wildcard\n", name, shard)
			continue
		}
		if !safeRouteToken(name) {
			continue
		}
		fmt.Fprintf(&b, "%s = host=%s port=%d dbname=%s auth_dbname=%s\n",
			name, addr.Host, addr.Port, name, dbRouterAuthDBName)
	}
	return b.String(), nil
}

// routerShards reads every shard that has an address.
func (h *Handler) routerShards(ctx context.Context) ([]shardAddr, error) {
	rows, err := h.pool.Query(ctx, `SELECT name, host, port FROM db_shards ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []shardAddr
	for rows.Next() {
		var s shardAddr
		if err := rows.Scan(&s.Name, &s.Host, &s.Port); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// routerPlacements reads where every managed database currently lives. The
// shard comes from the snapshot the status reconciler writes from the live CR,
// so a database that was created before shards existed reports the default
// rather than whatever the registry would pick today.
func (h *Handler) routerPlacements(ctx context.Context) ([]dbPlacement, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT summary_json FROM resource_snapshots WHERE kind = 'ServiceDatabaseV2'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []dbPlacement
	for rows.Next() {
		var summary []byte
		if err := rows.Scan(&summary); err != nil {
			return nil, err
		}
		datname := serviceDatabaseDatname(summary)
		if datname == "" {
			continue
		}
		out = append(out, dbPlacement{Datname: datname, Shard: serviceDatabaseShard(summary)})
	}
	return out, rows.Err()
}

// DBRoutes serves the router's [databases] section over the internal API. The
// pg-router pod pulls it on boot and on a timer; a failed fetch leaves the file
// it already has in place, which is why this handler never answers with a
// partial table.
func (h *Handler) DBRoutes(c *gin.Context) {
	ctx := c.Request.Context()
	shards, err := h.routerShards(ctx)
	if err != nil {
		c.String(http.StatusInternalServerError, "read shards: %v", err)
		return
	}
	placements, err := h.routerPlacements(ctx)
	if err != nil {
		c.String(http.StatusInternalServerError, "read placements: %v", err)
		return
	}
	body, err := renderPgBouncerRoutes(shards, placements, dbRouterDefaultShard)
	if err != nil {
		c.String(http.StatusInternalServerError, "render routes: %v", err)
		return
	}
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusOK, body)
}
