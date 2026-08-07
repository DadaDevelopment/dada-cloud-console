package api

import (
	"context"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// routerCutoverPollInterval is how often a paused router is re-asked whether it
// has picked up the new routing table. The route-sync sidecar rewrites the file
// on its own timer and the reloader signals pgbouncer on another, so the
// console cannot know the exact moment; it can only watch for the answer to
// change.
const routerCutoverPollInterval = 500 * time.Millisecond

// routerCutoverTimeout bounds the window clients spend queued behind PAUSE. A
// move that cannot complete inside it is rolled back to the source shard rather
// than left half-done: a tenant whose queries are held forever is worse off
// than a tenant still running on the old instance.
const routerCutoverTimeout = 30 * time.Second

// routerConn is one pgbouncer admin console. Narrow on purpose so the cutover
// sequence can be tested without a router: the risky part is the order of
// operations across replicas, not the SQL.
type routerConn interface {
	Exec(ctx context.Context, sql string) error
	DatabaseHost(ctx context.Context, datname string) (string, error)
	Close(ctx context.Context)
}

// routerAdmin drives every pg-router replica through a placement change.
//
// It talks to pods, never to the pg-router Service: the Service load-balances,
// so a PAUSE sent through it lands on one random replica and the other keeps
// happily forwarding writes to the shard the data is being moved off. That is
// the entire reason this type resolves addresses itself.
type routerAdmin struct {
	host string
	port int

	resolve func(ctx context.Context, host string) ([]string, error)
	dial    func(ctx context.Context, addr string) (routerConn, error)
	now     func() time.Time
	poll    time.Duration
	timeout time.Duration
}

// pgxRouterConn is the real admin console: pgbouncer speaks the postgres wire
// protocol but supports only the simple query protocol, so the connection is
// pinned to it. Extended-protocol pgx defaults would fail on the first command.
type pgxRouterConn struct{ conn *pgx.Conn }

func (c *pgxRouterConn) Exec(ctx context.Context, sql string) error {
	_, err := c.conn.Exec(ctx, sql)
	return err
}

// DatabaseHost reports which host the router currently routes datname to, or ""
// when the router has no entry of its own for it and the wildcard decides.
//
// SHOW DATABASES instantiates wildcard-derived entries lazily, so a database
// nobody has connected to since the last reload may be absent even though it is
// routable. Absent is therefore reported as "" rather than as an error.
func (c *pgxRouterConn) DatabaseHost(ctx context.Context, datname string) (string, error) {
	rows, err := c.conn.Query(ctx, "SHOW DATABASES")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	nameCol, hostCol := -1, -1
	for i, f := range rows.FieldDescriptions() {
		switch string(f.Name) {
		case "name":
			nameCol = i
		case "host":
			hostCol = i
		}
	}
	if nameCol < 0 || hostCol < 0 {
		return "", fmt.Errorf("SHOW DATABASES has no name/host column")
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return "", err
		}
		if fmt.Sprint(vals[nameCol]) == datname {
			return fmt.Sprint(vals[hostCol]), nil
		}
	}
	return "", rows.Err()
}

func (c *pgxRouterConn) Close(ctx context.Context) { _ = c.conn.Close(ctx) }

// newRouterAdmin builds the admin client from config. A missing host disables
// cutovers entirely rather than half-enabling them.
func newRouterAdmin(host string, port int, user, password string) *routerAdmin {
	return &routerAdmin{
		host: host,
		port: port,
		resolve: func(ctx context.Context, host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, host)
		},
		dial: func(ctx context.Context, addr string) (routerConn, error) {
			cfg, err := pgx.ParseConfig(fmt.Sprintf(
				"postgres://%s:%s@%s/pgbouncer?sslmode=disable", user, password, addr))
			if err != nil {
				return nil, err
			}
			cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
			conn, err := pgx.ConnectConfig(ctx, cfg)
			if err != nil {
				return nil, err
			}
			return &pgxRouterConn{conn: conn}, nil
		},
		now:     time.Now,
		poll:    routerCutoverPollInterval,
		timeout: routerCutoverTimeout,
	}
}

// quoteRouterIdent quotes a database name for the admin console. Names reach
// here from tenant input, and PAUSE takes a bare identifier, so an unquoted
// name with a space or a quote would turn one command into another.
func quoteRouterIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// The RELOAD lives inside the poll loop rather than before it: the routing file
// is written by the route-sync sidecar on its own timer, so at the moment the
// console pauses traffic the new table usually has not landed in the pod yet. A
// single RELOAD up front would re-read the old file and the loop would then
// wait for a change nobody asked pgbouncer to notice.
//
// Cutover holds every client of datname still, waits for all router replicas to
// agree that the database now lives on wantHost, and lets the clients go. To a
// client this is a pause in the middle of a session, not a dropped connection:
// pgbouncer queues new queries behind PAUSE and returns them when RESUME comes,
// which is what makes moving a database look like a blip rather than an outage.
//
// The caller must have already made the new placement true - the data copied
// and the registry updated - before calling: this function only decides the
// instant at which traffic switches.
//
// Every exit path resumes. A router left paused serves nobody, so a failure to
// finish the cutover must still end with traffic flowing to the old shard.
func (a *routerAdmin) Cutover(ctx context.Context, datname, wantHost string, during func(context.Context) error) error {
	if a == nil || a.host == "" {
		return fmt.Errorf("router admin is not configured")
	}
	addrs, err := a.resolve(ctx, a.host)
	if err != nil {
		return fmt.Errorf("resolve router pods: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("router %s resolved to no pods", a.host)
	}
	sort.Strings(addrs)

	conns := make([]routerConn, 0, len(addrs))
	defer func() {
		for _, c := range conns {
			c.Close(ctx)
		}
	}()
	for _, addr := range addrs {
		c, err := a.dial(ctx, net.JoinHostPort(addr, fmt.Sprint(a.port)))
		if err != nil {
			return fmt.Errorf("dial router %s: %w", addr, err)
		}
		conns = append(conns, c)
	}

	paused := make([]routerConn, 0, len(conns))
	resumeAll := func() {
		for _, c := range paused {
			if err := c.Exec(ctx, "RESUME "+quoteRouterIdent(datname)); err != nil {
				log.Printf("db-router: RESUME %s failed after cutover: %v", datname, err)
			}
		}
	}

	for i, c := range conns {
		if err := c.Exec(ctx, "PAUSE "+quoteRouterIdent(datname)); err != nil {
			resumeAll()
			return fmt.Errorf("pause router %s: %w", addrs[i], err)
		}
		paused = append(paused, c)
	}

	if during != nil {
		if err := during(ctx); err != nil {
			resumeAll()
			return fmt.Errorf("cutover work for %s: %w", datname, err)
		}
	}

	deadline := a.now().Add(a.timeout)
	for i, c := range paused {
		for {
			if err := c.Exec(ctx, "RELOAD"); err != nil {
				resumeAll()
				return fmt.Errorf("reload router %s: %w", addrs[i], err)
			}
			host, err := c.DatabaseHost(ctx, datname)
			if err != nil {
				resumeAll()
				return fmt.Errorf("read routing on %s: %w", addrs[i], err)
			}
			if host == wantHost {
				break
			}
			if !a.now().Before(deadline) {
				resumeAll()
				return fmt.Errorf("router %s still routes %s to %q after %s, want %q",
					addrs[i], datname, host, a.timeout, wantHost)
			}
			select {
			case <-ctx.Done():
				resumeAll()
				return ctx.Err()
			case <-time.After(a.poll):
			}
		}
	}

	resumeAll()
	return nil
}
