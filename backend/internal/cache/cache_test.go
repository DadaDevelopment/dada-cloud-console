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
