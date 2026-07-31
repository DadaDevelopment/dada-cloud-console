package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

type payload struct {
	N int    `json:"n"`
	S string `json:"s"`
}

func TestFetchNilCachePassesThrough(t *testing.T) {
	calls := 0
	compute := func() (payload, error) { calls++; return payload{N: 1, S: "x"}, nil }

	var c *Cache
	for i := 0; i < 3; i++ {
		v, err := Fetch(context.Background(), c, "k", time.Minute, compute)
		if err != nil || v.N != 1 {
			t.Fatalf("nil cache: got %+v err %v", v, err)
		}
	}
	if calls != 3 {
		t.Fatalf("nil cache must always compute; want 3 calls got %d", calls)
	}
}

func TestFetchHitThenMiss(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	c := New(mr.Addr())
	defer c.Close()

	calls := 0
	compute := func() (payload, error) { calls++; return payload{N: 42, S: "hot"}, nil }

	v, err := Fetch(context.Background(), c, "cost:p1", time.Minute, compute)
	if err != nil || v.N != 42 {
		t.Fatalf("first fetch: %+v err %v", v, err)
	}
	if calls != 1 {
		t.Fatalf("first fetch must compute; got calls=%d", calls)
	}

	v, err = Fetch(context.Background(), c, "cost:p1", time.Minute, compute)
	if err != nil || v.N != 42 || v.S != "hot" {
		t.Fatalf("second fetch: %+v err %v", v, err)
	}
	if calls != 1 {
		t.Fatalf("second fetch must hit cache; want calls=1 got %d", calls)
	}
}

func TestStoreThenFetchHits(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	c := New(mr.Addr())
	defer c.Close()

	Store(context.Background(), c, "cost:allocs:30d", time.Minute, payload{N: 99, S: "warmed"})

	calls := 0
	v, err := Fetch(context.Background(), c, "cost:allocs:30d", time.Minute, func() (payload, error) {
		calls++
		return payload{}, nil
	})
	if err != nil || v.N != 99 || v.S != "warmed" {
		t.Fatalf("expected warmed value from Store, got %+v err %v", v, err)
	}
	if calls != 0 {
		t.Fatalf("Fetch must hit the warmed entry without computing; got calls=%d", calls)
	}
}

func TestStoreNilCacheNoPanic(t *testing.T) {
	var c *Cache
	Store(context.Background(), c, "k", time.Minute, payload{N: 1})
}

func TestFetchExpiryRecomputes(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	c := New(mr.Addr())
	defer c.Close()

	calls := 0
	compute := func() (payload, error) { calls++; return payload{N: calls}, nil }

	if _, err := Fetch(context.Background(), c, "k", 30*time.Second, compute); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(31 * time.Second)
	v, err := Fetch(context.Background(), c, "k", 30*time.Second, compute)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || v.N != 2 {
		t.Fatalf("after expiry must recompute; calls=%d v=%+v", calls, v)
	}
}

func TestFetchFailOpenOnDeadRedis(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	c := New(mr.Addr())
	defer c.Close()
	mr.Close()

	calls := 0
	compute := func() (payload, error) { calls++; return payload{N: 7}, nil }

	v, err := Fetch(context.Background(), c, "k", time.Minute, compute)
	if err != nil {
		t.Fatalf("dead redis must fall back to compute, not error: %v", err)
	}
	if v.N != 7 || calls != 1 {
		t.Fatalf("dead redis: want computed value calls=1, got v=%+v calls=%d", v, calls)
	}
}

func TestFetchPropagatesComputeError(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	c := New(mr.Addr())
	defer c.Close()

	want := errors.New("boom")
	_, err = Fetch(context.Background(), c, "k", time.Minute, func() (payload, error) {
		return payload{}, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("compute error must propagate; got %v", err)
	}
	if mr.Exists("k") {
		t.Fatal("failed compute must not populate the cache")
	}
}

// TestTryClaimOneWinnerPerInterval is the gate the cost warmer relies on: two
// replicas sharing one Redis must produce exactly one worker per interval, and
// the next interval must hand the work out again.
func TestTryClaimOneWinnerPerInterval(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	replicaA := New(mr.Addr())
	defer replicaA.Close()
	replicaB := New(mr.Addr())
	defer replicaB.Close()

	ctx := context.Background()
	if !replicaA.TryClaim(ctx, "warm", 135*time.Second) {
		t.Fatal("first claim must win")
	}
	if replicaB.TryClaim(ctx, "warm", 135*time.Second) {
		t.Fatal("second replica must lose the claim while it is held")
	}
	if replicaA.TryClaim(ctx, "warm", 135*time.Second) {
		t.Fatal("holder must not re-win its own claim within the interval")
	}

	mr.FastForward(136 * time.Second)
	if !replicaB.TryClaim(ctx, "warm", 135*time.Second) {
		t.Fatal("claim must be winnable again after it expires")
	}
}

func TestTryClaimFailsOpen(t *testing.T) {
	var nilCache *Cache
	if !nilCache.TryClaim(context.Background(), "warm", time.Minute) {
		t.Fatal("disabled cache must not gate the loop")
	}

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	c := New(mr.Addr())
	defer c.Close()
	mr.Close()
	if !c.TryClaim(context.Background(), "warm", time.Minute) {
		t.Fatal("dead redis must not stop every replica from warming")
	}
}
