package box

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestPoolClaimIsExactlyOnceUnderConcurrency is the one that must never regress:
// handing the same body to two tenants would put one customer's agent inside
// another customer's box. 100 goroutines race for 10 instances; exactly 10 win,
// each a distinct instance, and the remaining 90 get ErrPoolExhausted rather than
// blocking.
func TestPoolClaimIsExactlyOnceUnderConcurrency(t *testing.T) {
	const (
		instances = 10
		claimers  = 100
	)

	pool := NewMemoryPool()
	for i := 0; i < instances; i++ {
		pool.Add("warm-polyglot-1", "ru1", &Instance{ID: fmt.Sprintf("box-%02d", i)})
	}

	var (
		mu        sync.Mutex
		claimed   []string
		exhausted int
		other     []error
		wg        sync.WaitGroup
		start     = make(chan struct{})
	)

	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			inst, hit, err := pool.Claim(context.Background(), "warm-polyglot-1", "ru1")

			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrPoolExhausted):
				exhausted++
				if hit {
					other = append(other, errors.New("exhausted claim reported a pool hit"))
				}
			case err != nil:
				other = append(other, err)
			default:
				if !hit {
					other = append(other, errors.New("successful claim did not report a pool hit"))
				}
				claimed = append(claimed, inst.ID)
			}
		}()
	}

	close(start)
	wg.Wait()

	for _, err := range other {
		t.Errorf("unexpected claim outcome: %v", err)
	}
	if len(claimed) != instances {
		t.Errorf("claimed %d instances, want %d", len(claimed), instances)
	}
	if exhausted != claimers-instances {
		t.Errorf("%d claimers saw exhaustion, want %d", exhausted, claimers-instances)
	}

	seen := map[string]bool{}
	for _, id := range claimed {
		if seen[id] {
			t.Errorf("instance %s was claimed twice — two tenants would share one body", id)
		}
		seen[id] = true
	}
	if got := pool.Available("warm-polyglot-1", "ru1"); got != 0 {
		t.Errorf("pool reports %d available after being drained", got)
	}
}

// TestPoolExhaustionDoesNotBlock pins the choice of a typed error over a wait. A
// blocking claim would make an empty pool indistinguishable from a slow spawn, and
// the pool-miss rate — the leading indicator for the entire product claim — would
// read as zero while customers waited.
func TestPoolExhaustionDoesNotBlock(t *testing.T) {
	pool := NewMemoryPool()

	done := make(chan error, 1)
	go func() {
		_, _, err := pool.Claim(context.Background(), "warm-polyglot-1", "ru1")
		done <- err
	}()

	err := <-done // would hang here if Claim waited for capacity
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("err = %v, want ErrPoolExhausted", err)
	}
}

func TestPoolIsPartitionedByImageAndRegion(t *testing.T) {
	pool := NewMemoryPool()
	pool.Add("warm-polyglot-1", "ru1", &Instance{ID: "a"})

	if _, _, err := pool.Claim(context.Background(), "warm-polyglot-1", "ru2"); !errors.Is(err, ErrPoolExhausted) {
		t.Errorf("claiming another region must not steal from ru1: %v", err)
	}
	if _, _, err := pool.Claim(context.Background(), "warm-gpu-1", "ru1"); !errors.Is(err, ErrPoolExhausted) {
		t.Errorf("claiming another image must not hand over the wrong toolchain: %v", err)
	}
	if _, hit, err := pool.Claim(context.Background(), "warm-polyglot-1", "ru1"); err != nil || !hit {
		t.Errorf("the matching partition should still hold its instance: hit=%v err=%v", hit, err)
	}
}

func TestPoolReportsTarget(t *testing.T) {
	pool := NewMemoryPool()
	pool.SetTarget("warm-polyglot-1", "ru1", 6)
	if got := pool.Target("warm-polyglot-1", "ru1"); got != 6 {
		t.Errorf("target = %d, want 6", got)
	}
	if got := pool.Target("warm-polyglot-1", "ru2"); got != 0 {
		t.Errorf("unset target = %d, want 0", got)
	}
}

func TestPoolRespectsCancelledContext(t *testing.T) {
	pool := NewMemoryPool()
	pool.Add("warm-polyglot-1", "ru1", &Instance{ID: "a"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := pool.Claim(ctx, "warm-polyglot-1", "ru1"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if got := pool.Available("warm-polyglot-1", "ru1"); got != 1 {
		t.Errorf("a cancelled claim must not consume an instance; available = %d", got)
	}
}
