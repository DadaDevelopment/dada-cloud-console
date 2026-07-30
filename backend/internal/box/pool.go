package box

import (
	"context"
	"sync"
)

// MemoryPool is the reference WarmPool: a fixed set of pre-warmed instances per
// image and region, handed out at most once each.
//
// Production will claim from Postgres with SELECT ... FOR UPDATE SKIP LOCKED, but
// the behaviour that matters is identical and is pinned here: a claim is
// exactly-once under concurrency, and exhaustion returns ErrPoolExhausted instead
// of blocking. A blocking claim would turn an empty pool into an invisible
// latency cliff — the caller could not tell a slow spawn from a queued one, and
// the pool-miss rate, which is the leading indicator of the whole product claim,
// would read as zero while customers waited.
type MemoryPool struct {
	mu    sync.Mutex
	free  map[poolKey][]*Instance
	want  map[poolKey]int
	taken map[string]bool
}

type poolKey struct {
	image  string
	region string
}

// NewMemoryPool builds an empty pool.
func NewMemoryPool() *MemoryPool {
	return &MemoryPool{
		free:  map[poolKey][]*Instance{},
		want:  map[poolKey]int{},
		taken: map[string]bool{},
	}
}

// Add parks a pre-warmed instance in the pool.
func (p *MemoryPool) Add(image, region string, inst *Instance) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := poolKey{image, region}
	p.free[k] = append(p.free[k], inst)
}

// SetTarget records how many free instances the pool controller aims to keep.
func (p *MemoryPool) SetTarget(image, region string, n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.want[poolKey{image, region}] = n
}

// Claim removes one instance from the pool and returns it. hit is false only when
// the error is nil and the instance came from somewhere other than the pool, which
// this implementation never does — so a false hit here always accompanies
// ErrPoolExhausted, and the caller decides whether to pay a cold start.
func (p *MemoryPool) Claim(ctx context.Context, image, region string) (*Instance, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	k := poolKey{image, region}
	for len(p.free[k]) > 0 {
		last := len(p.free[k]) - 1
		inst := p.free[k][last]
		p.free[k] = p.free[k][:last]

		// Belt and braces: the slice is the source of truth, but a double-claim
		// bug would hand the same body to two tenants, so it is worth an explicit
		// guard rather than an assumption.
		if p.taken[inst.ID] {
			continue
		}
		p.taken[inst.ID] = true
		return inst, true, nil
	}
	return nil, false, ErrPoolExhausted
}

// Available reports free pre-warmed instances.
func (p *MemoryPool) Available(image, region string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.free[poolKey{image, region}])
}

// Target reports the pool controller's goal for free instances.
func (p *MemoryPool) Target(image, region string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.want[poolKey{image, region}]
}
