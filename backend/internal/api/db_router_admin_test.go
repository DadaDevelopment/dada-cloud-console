package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRouterConn is one replica: it records the admin commands it was given and
// reports the host it currently routes to, switching to newHost after the Nth
// RELOAD so a test can make a replica lag behind the routing file.
type fakeRouterConn struct {
	addr        string
	cmds        *[]string
	mu          *sync.Mutex
	host        string
	newHost     string
	reloadsLeft int
	failPause   bool
}

func (c *fakeRouterConn) record(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.cmds = append(*c.cmds, c.addr+" "+s)
}

func (c *fakeRouterConn) Exec(ctx context.Context, sql string) error {
	c.record(sql)
	switch {
	case strings.HasPrefix(sql, "PAUSE") && c.failPause:
		return fmt.Errorf("pause refused")
	case sql == "RELOAD":
		if c.reloadsLeft > 0 {
			c.reloadsLeft--
			if c.reloadsLeft == 0 && c.newHost != "" {
				c.host = c.newHost
			}
		}
	}
	return nil
}

func (c *fakeRouterConn) DatabaseHost(ctx context.Context, datname string) (string, error) {
	return c.host, nil
}

func (c *fakeRouterConn) Close(ctx context.Context) {}

func newFakeAdmin(addrs []string, mk func(addr string) *fakeRouterConn) (*routerAdmin, *[]string) {
	cmds := &[]string{}
	mu := &sync.Mutex{}
	a := &routerAdmin{
		host: "pg-router-admin.databases.svc",
		port: 5432,
		resolve: func(ctx context.Context, host string) ([]string, error) {
			return append([]string(nil), addrs...), nil
		},
		dial: func(ctx context.Context, addr string) (routerConn, error) {
			c := mk(strings.Split(addr, ":")[0])
			c.cmds, c.mu = cmds, mu
			return c, nil
		},
		now:     time.Now,
		poll:    time.Millisecond,
		timeout: 200 * time.Millisecond,
	}
	return a, cmds
}

func countCmd(cmds []string, cmd string) int {
	n := 0
	for _, l := range cmds {
		if strings.Contains(l, cmd) {
			n++
		}
	}
	return n
}

// Both replicas must be paused. Sending the sequence through the pg-router
// Service would pause a random one and leave the other forwarding writes to the
// instance the data is being moved off.
func TestRouterCutoverPausesEveryReplica(t *testing.T) {
	a, cmds := newFakeAdmin([]string{"10.0.0.1", "10.0.0.2"}, func(addr string) *fakeRouterConn {
		return &fakeRouterConn{addr: addr, host: "old.svc", newHost: "new.svc", reloadsLeft: 2}
	})
	if err := a.Cutover(context.Background(), "odds-research", "new.svc", nil); err != nil {
		t.Fatalf("cutover: %v", err)
	}
	if got := countCmd(*cmds, "PAUSE \"odds-research\""); got != 2 {
		t.Fatalf("PAUSE count = %d, want 2 (one per replica): %v", got, *cmds)
	}
	if got := countCmd(*cmds, "RESUME \"odds-research\""); got != 2 {
		t.Fatalf("RESUME count = %d, want 2: %v", got, *cmds)
	}
	first, last := (*cmds)[0], (*cmds)[len(*cmds)-1]
	if !strings.Contains(first, "PAUSE") || !strings.Contains(last, "RESUME") {
		t.Fatalf("sequence must open with PAUSE and close with RESUME: %v", *cmds)
	}
}

// A replica whose routing file never arrives must not leave clients queued
// forever: the cutover fails and everything resumes on the old shard.
func TestRouterCutoverResumesWhenRouteNeverLands(t *testing.T) {
	a, cmds := newFakeAdmin([]string{"10.0.0.1"}, func(addr string) *fakeRouterConn {
		return &fakeRouterConn{addr: addr, host: "old.svc"}
	})
	if err := a.Cutover(context.Background(), "odds-research", "new.svc", nil); err == nil {
		t.Fatal("a router that never picks up the new table must fail the cutover")
	}
	if countCmd(*cmds, "RESUME") != 1 {
		t.Fatalf("clients must be released even on failure: %v", *cmds)
	}
	if countCmd(*cmds, "RELOAD") < 2 {
		t.Fatalf("RELOAD must be retried while waiting for the file: %v", *cmds)
	}
}

// A pause that fails on the second replica must release the first one.
func TestRouterCutoverResumesAfterPartialPause(t *testing.T) {
	a, cmds := newFakeAdmin([]string{"10.0.0.1", "10.0.0.2"}, func(addr string) *fakeRouterConn {
		return &fakeRouterConn{addr: addr, host: "old.svc", failPause: addr == "10.0.0.2"}
	})
	if err := a.Cutover(context.Background(), "odds-research", "new.svc", nil); err == nil {
		t.Fatal("cutover must fail when a replica refuses PAUSE")
	}
	if countCmd(*cmds, "RESUME") != 1 {
		t.Fatalf("the replica that did pause must be resumed: %v", *cmds)
	}
}

// No pods resolved means no cutover: pausing nothing and declaring success
// would switch the registry while live traffic keeps writing to the old shard.
func TestRouterCutoverRefusesWithoutPods(t *testing.T) {
	a, cmds := newFakeAdmin(nil, func(addr string) *fakeRouterConn { return &fakeRouterConn{addr: addr} })
	if err := a.Cutover(context.Background(), "odds-research", "new.svc", nil); err == nil {
		t.Fatal("cutover with zero routers must fail")
	}
	if len(*cmds) != 0 {
		t.Fatalf("nothing may be executed: %v", *cmds)
	}
}

func TestQuoteRouterIdent(t *testing.T) {
	if got := quoteRouterIdent("odd\"s"); got != "\"odd\"\"s\"" {
		t.Fatalf("quoteRouterIdent = %s", got)
	}
}

// Work done inside the held window is the move itself: draining the last of
// the replication lag, copying sequence positions, marking the database as
// living on its new shard. If any of it fails the routing table must not be
// switched at all, and the clients waiting behind PAUSE must be let go on the
// shard that still has their data.
func TestRouterCutoverResumesWhenTheWorkFails(t *testing.T) {
	a, cmds := newFakeAdmin([]string{"10.0.0.1", "10.0.0.2"}, func(addr string) *fakeRouterConn {
		return &fakeRouterConn{addr: addr, host: "old.svc", newHost: "new.svc", reloadsLeft: 1}
	})
	err := a.Cutover(context.Background(), "odds-research", "new.svc", func(context.Context) error {
		return fmt.Errorf("lag never drained")
	})
	if err == nil {
		t.Fatal("work that fails inside the held window must fail the cutover")
	}
	if got := countCmd(*cmds, "RESUME \"odds-research\""); got != 2 {
		t.Fatalf("every paused replica must be released, got %d: %v", got, *cmds)
	}
	if got := countCmd(*cmds, "RELOAD"); got != 0 {
		t.Fatalf("routes must not be switched when the work failed: %v", *cmds)
	}
}

// The work runs while traffic is held, not before it: sequence positions copied
// before PAUSE would be stale by the time clients stop writing.
func TestRouterCutoverRunsWorkAfterEveryPause(t *testing.T) {
	a, cmds := newFakeAdmin([]string{"10.0.0.1", "10.0.0.2"}, func(addr string) *fakeRouterConn {
		return &fakeRouterConn{addr: addr, host: "old.svc", newHost: "new.svc", reloadsLeft: 1}
	})
	var pausesWhenWorkRan int
	if err := a.Cutover(context.Background(), "odds-research", "new.svc", func(context.Context) error {
		pausesWhenWorkRan = countCmd(*cmds, "PAUSE")
		return nil
	}); err != nil {
		t.Fatalf("cutover: %v", err)
	}
	if pausesWhenWorkRan != 2 {
		t.Fatalf("work ran with %d replicas paused, want 2", pausesWhenWorkRan)
	}
}
