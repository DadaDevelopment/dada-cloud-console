package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// TestMeasureSlowOriginWin quantifies what the cache-aside layer buys when the
// origin is slow -- the "бывает долго" tail-latency case an OpenCost/Prometheus
// aggregation hits under load. It is a measurement, not a pass/fail assertion of
// a latency budget (which would be flaky): it fails only if the cache does not
// actually collapse repeated slow calls into fast hits.
func TestMeasureSlowOriginWin(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	c := New(mr.Addr())
	defer c.Close()

	const originLatency = 200 * time.Millisecond
	compute := func() (payload, error) {
		time.Sleep(originLatency)
		return payload{N: 1, S: "cost"}, nil
	}

	t0 := time.Now()
	if _, err := Fetch(context.Background(), c, "cost:allocs:30d", time.Minute, compute); err != nil {
		t.Fatal(err)
	}
	miss := time.Since(t0)

	const hits = 50
	t1 := time.Now()
	for i := 0; i < hits; i++ {
		if _, err := Fetch(context.Background(), c, "cost:allocs:30d", time.Minute, compute); err != nil {
			t.Fatal(err)
		}
	}
	avgHit := time.Since(t1) / hits

	t.Logf("slow origin = %v", originLatency)
	t.Logf("cold (miss, computes) = %v", miss.Round(time.Millisecond))
	t.Logf("warm (hit, avg of %d)  = %v", hits, avgHit)
	if avgHit > 0 {
		t.Logf("speedup on repeat = %.0fx", float64(miss)/float64(avgHit))
	}

	if avgHit >= miss/10 {
		t.Fatalf("cache did not collapse the slow origin: miss=%v avgHit=%v", miss, avgHit)
	}
}
