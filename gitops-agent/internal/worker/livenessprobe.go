package worker

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// livenessProbeMaxRedirects bounds how many 3xx hops probe() will chase for
// one app before giving up and reporting the last redirect verbatim. Ingress
// controllers commonly answer plain HTTP with a same-host scheme-upgrade
// redirect to HTTPS (nginx-ingress does this by default whenever the Ingress
// carries a tls block), and following exactly that hop is what turns the
// probe from "measured the redirect" into "measured the app": the first
// prod rollout of this feature reported every app as http_status=308
// (0 real 4xx/5xx ever surfaced) because the redirect itself was treated as
// the terminal answer. A small bound keeps a misbehaving app's redirect loop
// from hanging the probe past its timeout budget.
const livenessProbeMaxRedirects = 5

// livenessProbeConcurrency bounds how many app probes run at once, so a
// platform with many hostnames does not serialize into a long per-tick tail
// (worst case: N apps * livenessProbeTimeout).
const livenessProbeConcurrency = 8

// livenessProbeBodyPeek bounds how much of a response body the probe reads
// in order to name its author (see isAppAuthoredBody). The ingress error
// page it looks for is ~150 bytes; the cap exists so an app streaming a
// large document can never turn a health probe into a download.
const livenessProbeBodyPeek = 4096

// ingressGeneratedStatuses are the codes ingress-nginx emits by itself when
// it cannot reach a backend for the route. They are exactly the codes for
// which the response body has to be consulted before deciding whether the
// last mile is dead or the app answered.
var ingressGeneratedStatuses = map[int]bool{
	502: true,
	503: true,
	504: true,
}

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
//
// TLS verification is intentionally skipped: baseURL names the ingress
// controller's in-cluster Service, whose certificate (if any) is issued for
// that Service name, never for the tenant hostname carried in the Host
// header this prober sends -- verifying against baseURL's own identity
// would reject every request. The probe already trusts the platform's own
// network path to reach the ingress Service; skipping verification here
// does not extend that trust anywhere a real visitor's TLS handshake
// relies on.
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
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// probe sends GET / to p.baseURL with hostname in the Host header, chasing
// up to livenessProbeMaxRedirects same-host 3xx hops before classifying the
// result. Following the redirect is the point: an ingress controller
// commonly answers plain HTTP with a scheme-upgrade redirect to HTTPS
// before the app ever sees the request, and stopping at that first hop
// measures the ingress's redirect, not the app -- every probed app then
// reports the same http_status regardless of whether it is actually up,
// which is a false green worse than no signal at all.
//
// A redirect is followed only when it targets the same hostname the probe
// was sent for (relative Location, or an absolute one whose host matches);
// anything else -- a genuinely different host, a missing Location, or the
// hop budget running out -- is returned as-is rather than chased further,
// so the probe can never be steered off the app it was asked to check.
//
// The final response classifies as: 2xx, or an off-host 3xx read but not
// followed, is alive with an empty reason; 4xx/5xx carries a status_<code>
// reason; a transport failure carries dial_error or timeout. Exhausting the
// hop budget is reported as redirect_loop rather than as a bare 3xx: an app
// that redirects forever serves nothing to a visitor, and recording it with
// an empty reason would recreate in miniature the false green this whole
// probe exists to remove. http_status stays 0 whenever no response was ever
// received.
func (p *livenessProber) probe(ctx context.Context, hostname string) livenessProbeResult {
	now := time.Now().UTC()

	target, err := url.Parse(p.baseURL)
	if err != nil {
		return livenessProbeResult{reason: "probe_build_error", checkedAt: now}
	}
	target.Path = "/"
	target.RawQuery = ""

	for hop := 0; hop <= livenessProbeMaxRedirects; hop++ {
		reqCtx, cancel := context.WithTimeout(ctx, livenessProbeTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target.String(), nil)
		if err != nil {
			cancel()
			return livenessProbeResult{reason: "probe_build_error", checkedAt: now}
		}
		req.Host = hostname

		resp, err := p.client.Do(req)
		cancel()
		if err != nil {
			reason := "dial_error"
			if errors.Is(err, context.DeadlineExceeded) {
				reason = "timeout"
			}
			return livenessProbeResult{reason: reason, checkedAt: now}
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, livenessProbeBodyPeek))
		resp.Body.Close()

		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			return classifyLivenessResponse(resp.StatusCode, body, now)
		}
		if hop == livenessProbeMaxRedirects {
			res := classifyLivenessResponse(resp.StatusCode, body, now)
			res.reason = "redirect_loop"
			return res
		}

		nextTarget, follow := nextRedirectTarget(target, resp.Header.Get("Location"), hostname)
		if !follow {
			return classifyLivenessResponse(resp.StatusCode, body, now)
		}
		target = nextTarget
	}

	return livenessProbeResult{reason: "probe_build_error", checkedAt: now}
}

// classifyLivenessResponse turns a terminal HTTP status into the recorded
// result: 4xx/5xx carries a status_<code> reason, anything else (2xx, or a
// 3xx the caller decided not to chase further) is alive with an empty
// reason.
//
// For the three codes an ingress controller generates on its own
// (502/503/504) the status alone cannot say WHO answered, and the two
// possible authors mean opposite things: a live app may deliberately serve
// 503 while it warms up, and a route with no backend behind it serves the
// same 503 from nginx. Both were observed live on 2026-08-15 --
// fonbet-value answered `{"application_version":...,"blockers":[...]}` with
// content-type application/json from inside a healthy pod, while a stale
// hash-domain ingress answered nginx's own error page -- so the body is
// consulted to name the author. An app-authored body is recorded as
// app_status_<code>, which downstream reads as "the last mile is alive".
// Anything else, including an empty body, keeps the plain status_<code>
// reason and stays dead: the conservative direction is to leave an unknown
// author red rather than to invent a false green.
func classifyLivenessResponse(status int, body []byte, checkedAt time.Time) livenessProbeResult {
	reason := ""
	switch {
	case ingressGeneratedStatuses[status] && isAppAuthoredBody(body):
		reason = fmt.Sprintf("app_status_%d", status)
	case status >= 400:
		reason = fmt.Sprintf("status_%d", status)
	}
	return livenessProbeResult{status: status, reason: reason, checkedAt: checkedAt}
}

// isAppAuthoredBody reports whether body was written by the application
// rather than by the ingress controller standing in front of it.
//
// ingress-nginx serves a fixed, self-identifying error page when it has no
// backend for a route: a short text/html document whose last line is
// `<hr><center>nginx</center>`. That signature is the discriminator -- it is
// present in every default-backend response and absent from anything an app
// writes itself. An empty body carries no evidence either way and is
// therefore NOT treated as app-authored.
func isAppAuthoredBody(body []byte) bool {
	if len(bytes.TrimSpace(body)) == 0 {
		return false
	}
	return !bytes.Contains(body, []byte("<center>nginx</center>"))
}

// nextRedirectTarget resolves a 3xx response's Location header against cur
// and reports whether probe() should chase it. It refuses to follow onto a
// different host than hostname -- the app's own primary hostname, i.e. what
// the probe was actually asked to check -- so a redirect can never steer
// the probe at a different app or an arbitrary external target. The
// resolved URL always keeps cur's own host:port (the in-cluster ingress
// Service): a scheme-upgrade redirect's Location names the tenant's PUBLIC
// hostname, and dialing that would send the follow-up hop out over
// external egress exactly like the DNS resolution this probe design avoids
// on the first hop.
func nextRedirectTarget(cur *url.URL, location string, hostname string) (*url.URL, bool) {
	if location == "" {
		return nil, false
	}
	loc, err := url.Parse(location)
	if err != nil {
		return nil, false
	}
	if h := loc.Hostname(); h != "" && h != hostname {
		return nil, false
	}

	resolved := cur.ResolveReference(loc)
	next := *cur
	next.Scheme = resolved.Scheme
	next.Path = resolved.Path
	next.RawQuery = resolved.RawQuery
	return &next, true
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
