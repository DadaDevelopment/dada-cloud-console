package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// livenessProbeTimeout bounds a single app's HTTP liveness check. Chosen
// short deliberately: the probe exists to catch an app answering 5xx/404,
// not to wait out a slow one, and it runs once per app per
// LivenessProbeMinInterval so a stuck probe must never be allowed to pile up.
const livenessProbeTimeout = 5 * time.Second

// livenessProbeConcurrency bounds how many app probes run at once, so a
// platform with many hostnames does not serialize into a long per-tick tail
// (worst case: N apps * livenessProbeTimeout).
const livenessProbeConcurrency = 8

// livenessProbeResult is one HTTP check outcome for an app's primary
// hostname, shaped to drop straight into resource_snapshots.summary_json as
// http_status / http_reason / http_checked_at.
type livenessProbeResult struct {
	status    int
	reason    string
	checkedAt time.Time
}

// livenessProber issues an in-cluster HTTP probe against an app's primary
// hostname through the public ingress, carrying the hostname in the Host
// header rather than resolving it over DNS/egress. That is what makes the
// probe a check of the app rather than of the platform's flaky external
// egress: the request enters the cluster exactly where a real visitor's
// would, at baseURL, and is routed to the tenant vhost by Host alone.
type livenessProber struct {
	baseURL string
	client  *http.Client
}

// newLivenessProber builds a prober for baseURL, or returns nil when baseURL
// is empty -- the caller's signal that the feature is off and no probes
// should be sent at all.
func newLivenessProber(baseURL string) *livenessProber {
	if baseURL == "" {
		return nil
	}
	return &livenessProber{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: livenessProbeTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// probe sends one GET / to p.baseURL with hostname in the Host header and
// classifies the result. A 2xx or 3xx response (redirects are read, never
// followed) counts as alive with an empty reason; 4xx/5xx carries a
// status_<code> reason; a transport failure carries dial_error or timeout.
// http_status stays 0 whenever no response was ever received.
func (p *livenessProber) probe(ctx context.Context, hostname string) livenessProbeResult {
	now := time.Now().UTC()
	reqCtx, cancel := context.WithTimeout(ctx, livenessProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, p.baseURL+"/", nil)
	if err != nil {
		return livenessProbeResult{reason: "probe_build_error", checkedAt: now}
	}
	req.Host = hostname

	resp, err := p.client.Do(req)
	if err != nil {
		reason := "dial_error"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "timeout"
		}
		return livenessProbeResult{reason: reason, checkedAt: now}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	reason := ""
	if resp.StatusCode >= 400 {
		reason = fmt.Sprintf("status_%d", resp.StatusCode)
	}
	return livenessProbeResult{status: resp.StatusCode, reason: reason, checkedAt: now}
}

// livenessCandidate is one app eligible for a probe this tick: it has an
// active primary hostname and either has never been probed or is past
// LivenessProbeMinInterval since its last probe.
type livenessCandidate struct {
	key      snapKey
	hostname string
}

// probeLiveness runs candidates through prober with bounded concurrency and
// returns each app's result. It never blocks longer than roughly
// livenessProbeTimeout * ceil(len(candidates)/livenessProbeConcurrency),
// which is the per-tick time budget this pass is allowed: every probe shares
// the same fixed timeout, so the bound follows directly from the worker count.
func (r *StatusReconciler) probeLiveness(ctx context.Context, prober *livenessProber, candidates []livenessCandidate) map[snapKey]livenessProbeResult {
	results := make(map[snapKey]livenessProbeResult, len(candidates))
	if len(candidates) == 0 {
		return results
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, livenessProbeConcurrency)

	for _, c := range candidates {
		c := c
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			res := prober.probe(ctx, c.hostname)
			mu.Lock()
			results[c.key] = res
			mu.Unlock()
		}()
	}
	wg.Wait()

	return results
}

// livenessDue reports whether an app whose last probe was at lastChecked is
// eligible for another one now. A zero lastChecked (never probed, or the
// previous snapshot carried no http_checked_at at all) is always due.
func livenessDue(lastChecked time.Time, minInterval time.Duration) bool {
	if lastChecked.IsZero() {
		return true
	}
	return time.Since(lastChecked) >= minInterval
}

// loadLivenessCheckTimes reads the last http_checked_at recorded for every
// App snapshot that has one. This is the rate-limit clock: reusing the
// snapshot's own field means the limit holds across process restarts and
// across the reconciler's several concurrent goroutines, unlike an in-memory
// map keyed by process lifetime.
func (r *StatusReconciler) loadLivenessCheckTimes(ctx context.Context) map[snapKey]time.Time {
	out := map[snapKey]time.Time{}
	rows, err := r.pool.Query(ctx, `
		SELECT environment_id, name, summary_json->>'http_checked_at'
		FROM resource_snapshots
		WHERE kind = 'App' AND summary_json ? 'http_checked_at'
	`)
	if err != nil {
		log.Warn().Err(err).Msg("status-reconciler: load liveness check times")
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var envID uuid.UUID
		var name, checkedAtRaw string
		if err := rows.Scan(&envID, &name, &checkedAtRaw); err != nil {
			log.Warn().Err(err).Msg("status-reconciler: scan liveness check time")
			continue
		}
		checkedAt, err := time.Parse(time.RFC3339, checkedAtRaw)
		if err != nil {
			continue
		}
		out[snapKey{env: envID, app: name}] = checkedAt
	}
	return out
}
