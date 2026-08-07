package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/jackc/pgx/v5/pgxpool"
)

// appURLWatchInterval is the poll period for the customer-visible public
// route. The route contract is explicit: a positive configured port must
// answer over its public HTTPS address.
const appURLWatchInterval = 5 * time.Minute

// appURLProbeTimeout bounds both the TCP dial and the response read of one
// probe, so a single wedged app (accepts the connection, then never writes
// anything) cannot stall the tick behind it.
const appURLProbeTimeout = 5 * time.Second

// appURLAlertFailureThreshold is how many consecutive failing ticks it takes
// before app_url_alerts is considered armed and loadAppAlerts starts
// surfacing it. A single failed probe is deliberately not enough: the
// classic false positive here is an app mid cold-start (pod just went
// Ready, HTTP server still binding its port), and a red banner on an app
// that fixes itself thirty seconds later is worse than a banner delayed by
// two more ticks.
const appURLAlertFailureThreshold = 3

// appURLAlertFreshWindow is how recently last_seen_at must have been
// touched for the console to still show a URL alert as current, mirroring
// appHealthAlertFreshWindow/appVolumeAlertFreshWindow. Tied to
// appURLWatchInterval with the same 3x margin as the volume watcher: an app
// still failing gets re-touched every tick and never falls out of the
// window, while a fixed app clears its banner within one missed tick
// instead of lingering.
const appURLAlertFreshWindow = 3 * appURLWatchInterval

// A transport failure is an edge failure (DNS, TLS, load balancer); a 5xx is
// a route failure. A 4xx is still reachable and therefore healthy.
const (
	urlProbeReasonEdgeUnavailable = "edge_unavailable"
	urlProbeReasonBadGateway      = "bad_gateway"
)

// appURLWatcher probes every Ready app with both a configured port and public
// URL, then records bounded public-route failures for the console and metrics.
type appURLWatcher struct {
	h *Handler
}

// StartAppURLWatcher launches the public-route watcher. It deliberately does
// not require in-cluster DNS: the customer uses the public route, so that is
// the route we must verify.
func (h *Handler) StartAppURLWatcher(ctx context.Context) {
	w := &appURLWatcher{h: h}
	log.Printf("app-url: watcher started interval=%s timeout=%s threshold=%d", appURLWatchInterval, appURLProbeTimeout, appURLAlertFailureThreshold)
	go func() {
		runWithAdvisoryLock(ctx, h.pool, lockKeyAppURLWatch, "app-url", w.tick)
		t := time.NewTicker(appURLWatchInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyAppURLWatch, "app-url", w.tick)
			}
		}
	}()
}

// urlProbeCandidate is one app worth probing: it has a public URL and a
// configured positive port.
type urlProbeCandidate struct {
	Namespace string
	AppName   string
	Port      int
	URL       string
}

// parseURLProbeCandidate decides whether one resource_snapshots row is worth
// probing, purely from its summary JSON. Pure and unit-tested without a
// database.
//
// An app with no URL or no positive port has no public-route contract and is
// ignored. No workload-type inference participates in this decision.
func parseURLProbeCandidate(namespace, appName string, summaryRaw []byte) (urlProbeCandidate, bool) {
	if len(summaryRaw) == 0 {
		return urlProbeCandidate{}, false
	}
	var m map[string]any
	if err := json.Unmarshal(summaryRaw, &m); err != nil {
		return urlProbeCandidate{}, false
	}
	urlVal, _ := m["url"].(string)
	if urlVal == "" {
		return urlProbeCandidate{}, false
	}
	portVal, ok := m["port"].(float64)
	if !ok || portVal <= 0 {
		return urlProbeCandidate{}, false
	}
	return urlProbeCandidate{Namespace: namespace, AppName: appName, Port: int(portVal), URL: urlVal}, true
}

// loadCandidates reads every Ready, publicly-addressed app across every user
// namespace, excluding any app currently covered by a fresh crash alert
// (app_health_alerts, same freshness window the console itself uses in
// loadAppAlerts): a crashlooping pod is already explained to the user by the
// crash banner, and probing it too would either double-report the same
// outage under a different label or -- worse -- report "not_http" for an app
// that is not even running long enough to bind its port.
func (w *appURLWatcher) loadCandidates(ctx context.Context) ([]urlProbeCandidate, error) {
	rows, err := w.h.pool.Query(ctx,
		`SELECT e.namespace, rs.name, rs.summary_json,
		        EXISTS (
		            SELECT 1 FROM app_health_alerts ha
		            WHERE ha.namespace = e.namespace AND ha.app_name = rs.name
		              AND COALESCE(ha.last_seen_at, ha.last_sent_at) > now() - make_interval(secs => $1)
		        )
		 FROM resource_snapshots rs
		 JOIN environments e ON e.id = rs.environment_id
		 WHERE rs.kind = 'App' AND e.runtime = 'k8s' AND e.namespace <> '' AND rs.phase = 'Ready'`,
		appHealthAlertFreshWindow.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []urlProbeCandidate
	for rows.Next() {
		var namespace, appName string
		var summaryRaw []byte
		var crashing bool
		if scanErr := rows.Scan(&namespace, &appName, &summaryRaw, &crashing); scanErr != nil {
			return nil, scanErr
		}
		if crashing {
			continue
		}
		if cand, ok := parseURLProbeCandidate(namespace, appName, summaryRaw); ok {
			out = append(out, cand)
		}
	}
	return out, rows.Err()
}

// tick probes every candidate app once and records the outcome. Every
// per-app failure (probe error, DB write error) is logged and swallowed:
// one bad app must never block the scan of the rest, and this loop must
// never crash the backend pod it runs inside. A candidate-load failure
// (namespace/snapshot query) aborts the whole tick without touching any
// counter -- that is a failure on OUR side, not evidence the app is broken,
// and must never be allowed to silently increment every app's failure
// streak toward a false alert.
func (w *appURLWatcher) tick(ctx context.Context) {
	candidates, err := w.loadCandidates(ctx)
	if err != nil {
		log.Printf("app-url: load candidates failed: %v", err)
		return
	}
	log.Printf("app-url: tick candidates=%d", len(candidates))
	for _, c := range candidates {
		probeCtx, cancel := context.WithTimeout(ctx, appURLProbeTimeout)
		healthy, reason, detail := probePublicRoute(probeCtx, c.URL, appURLProbeTimeout)
		cancel()
		w.recordProbeResult(ctx, c.Namespace, c.AppName, healthy, reason, detail)
	}
}

// probePublicRoute makes an HTTPS request through the same public path a
// visitor uses. It does not skip certificate verification and creates a fresh
// connection for each probe, so a stale keep-alive cannot hide LB instability.
func probePublicRoute(ctx context.Context, rawURL string, timeout time.Duration) (healthy bool, reason, detail string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, urlProbeReasonEdgeUnavailable, err.Error()
	}
	client := &http.Client{Timeout: timeout, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return false, urlProbeReasonEdgeUnavailable, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		return false, urlProbeReasonBadGateway, fmt.Sprintf("public route returned HTTP %d", resp.StatusCode)
	}
	return true, "", ""
}

// looksLikeHTTPResponse is the entire classification of a raw TCP read:
// does it start with the one prefix every HTTP response, of any version or
// status code, is required to have. Pure and unit-tested against fixture
// byte slices (valid status lines, garbage, empty reads) with no socket
// involved.
func looksLikeHTTPResponse(data []byte) bool {
	return bytes.HasPrefix(data, []byte("HTTP/"))
}

// notHTTPDetail renders the first line of a non-HTTP response for the
// console/detail column, bounded so a chatty binary protocol cannot grow the
// stored row without limit.
func notHTTPDetail(data []byte) string {
	if len(data) == 0 {
		return "connected but sent no HTTP response"
	}
	line := data
	if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
		line = data[:idx]
	}
	line = bytes.TrimRight(line, "\r")
	const maxLen = 120
	if len(line) > maxLen {
		line = line[:maxLen]
	}
	return fmt.Sprintf("connected but response did not start with HTTP/: %q", string(line))
}

// urlProbeState is the pure counter-and-alert-armed state machine that
// recordURLProbeFailure/clearURLProbeAlert implement in SQL against
// app_url_alerts. Kept here, isolated from the database, so the anti-flap
// contract (three consecutive failures before an alert is armed, any single
// success resets it) is unit-tested directly rather than only through the
// SQL that mirrors it. Safe against a cross-replica race because
// runWithAdvisoryLock already lets only one replica run a given tick at a
// time -- there is never a concurrent writer to race against.
type urlProbeState struct {
	ConsecutiveFailures int
}

// recordFailure returns the state after one more failing probe and whether
// that failure is the one that arms the alert (crosses
// appURLAlertFailureThreshold).
func (s urlProbeState) recordFailure() (next urlProbeState, alertArmed bool) {
	next = urlProbeState{ConsecutiveFailures: s.ConsecutiveFailures + 1}
	return next, next.ConsecutiveFailures >= appURLAlertFailureThreshold
}

// recordSuccess returns the reset state after one passing probe and whether
// an alert had been armed before this reset (so the caller knows whether it
// needs to clear a row rather than just skip writing one).
func (s urlProbeState) recordSuccess() (next urlProbeState, wasArmed bool) {
	return urlProbeState{}, s.ConsecutiveFailures >= appURLAlertFailureThreshold
}

// recordURLProbeFailure is the SQL counterpart of urlProbeState.recordFailure:
// it upserts app_url_alerts, incrementing consecutive_failures atomically and
// returning the new count. reason/detail are overwritten on every failing
// tick (not just the first) so a streak that started as no_listener and
// later becomes not_http -- or vice versa -- always shows the latest
// classification, not a stale first guess.
func recordURLProbeFailure(ctx context.Context, pool *pgxpool.Pool, namespace, appName, reason, detail string) (int, error) {
	var failures int
	err := pool.QueryRow(ctx,
		`INSERT INTO app_url_alerts (namespace, app_name, reason, detail, consecutive_failures, last_seen_at, updated_at)
		 VALUES ($1, $2, $3, $4, 1, now(), now())
		 ON CONFLICT (namespace, app_name) DO UPDATE SET
		     consecutive_failures = app_url_alerts.consecutive_failures + 1,
		     reason = $3, detail = $4, last_seen_at = now(), updated_at = now()
		 RETURNING consecutive_failures`,
		namespace, appName, reason, detail).Scan(&failures)
	if err != nil {
		return 0, err
	}
	return failures, nil
}

// clearURLProbeAlert is the SQL counterpart of urlProbeState.recordSuccess:
// a passing probe deletes the row outright rather than zeroing the counter
// in place, so a later failing streak starts clean at consecutive_failures=1
// instead of resuming from whatever was last written.
func clearURLProbeAlert(ctx context.Context, pool *pgxpool.Pool, namespace, appName string) {
	if _, err := pool.Exec(ctx,
		`DELETE FROM app_url_alerts WHERE namespace = $1 AND app_name = $2`,
		namespace, appName); err != nil {
		log.Printf("app-url: clear alert for %s/%s failed: %v", namespace, appName, err)
	}
}

// recordProbeResult persists one probe outcome: a pass clears any existing
// alert row, a failure claims the next consecutive-failure count and logs
// once, at the moment the alert actually arms, so the operator log carries
// one line per genuinely new outage rather than one line per tick for the
// lifetime of a broken app.
func (w *appURLWatcher) recordProbeResult(ctx context.Context, namespace, appName string, healthy bool, reason, detail string) {
	metrics.RecordPublicRouteProbe(healthy, reason)
	if healthy {
		clearURLProbeAlert(ctx, w.h.pool, namespace, appName)
		return
	}
	failures, err := recordURLProbeFailure(ctx, w.h.pool, namespace, appName, reason, detail)
	if err != nil {
		log.Printf("app-url: record failure for %s/%s failed: %v", namespace, appName, err)
		return
	}
	if failures == appURLAlertFailureThreshold {
		log.Printf("app-url: alert armed for %s/%s reason=%s after %d consecutive failures", namespace, appName, reason, failures)
	}
}
