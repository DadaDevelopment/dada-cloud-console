package telemetry

import (
	"sync"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// IngestLimiter is a per-monitoring-app token bucket. ADR-011/012 require
// per-key rate limiting at ingest to bound cardinality/abuse; this is the
// in-process guard (one limiter per app id, perMin requests with a perMin
// burst). Safe for concurrent use.
type IngestLimiter struct {
	mu      sync.Mutex
	perMin  int
	buckets map[uuid.UUID]*rate.Limiter
}

// NewIngestLimiter builds a limiter allowing perMin requests/minute per app
// (default 120 when perMin <= 0).
func NewIngestLimiter(perMin int) *IngestLimiter {
	if perMin <= 0 {
		perMin = 120
	}
	return &IngestLimiter{perMin: perMin, buckets: make(map[uuid.UUID]*rate.Limiter)}
}

// Allow reports whether the app may make one more request now.
func (l *IngestLimiter) Allow(app uuid.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim := l.buckets[app]
	if lim == nil {
		lim = rate.NewLimiter(rate.Limit(float64(l.perMin)/60.0), l.perMin)
		l.buckets[app] = lim
	}
	return lim.Allow()
}
