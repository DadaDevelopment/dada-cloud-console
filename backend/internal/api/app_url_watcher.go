package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// appURLWatchInterval is the poll period for the URL-reality watcher: a live,
// Ready app that got handed a public HTTPS domain but never speaks HTTP on
// its declared port otherwise sits behind a silent, permanent 502 with no
// signal anywhere in the console. Two confirmed live cases: fanvk (a
// vkbottle long-poll bot on containerPort 8080 -- READY 1/1, domain
// provisioned, curl = 502) and oxygen (an MTProto proxy on containerPort
// 8443 -- READY 1/1, domain provisioned, curl times out). Neither is broken:
// both are workers that never should have been offered a public hostname in
// the first place, because servesHTTP (apps.go) only excludes a static
// denylist of known binary-protocol ports and treats everything else as "web
// app".
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

// urlProbeReasonNoListener and urlProbeReasonNotHTTP are the two ways a
// probe can fail. Any successful HTTP response -- including a 4xx or 5xx
// from the app's own code -- is healthy: the point of this watcher is
// reachability of an HTTP server, not correctness of what it serves.
const (
	urlProbeReasonNoListener = "no_listener"
	urlProbeReasonNotHTTP    = "not_http"
)

// appURLWatcher polls every live, publicly-addressed app over its
// in-cluster Service DNS name and records consecutive HTTP-reachability
// failures in app_url_alerts.
//
// The probe deliberately targets <app>-service.<namespace>.svc.cluster.local
// (the same in-cluster Service name preview_gate.go's servicePort resolves
// against), never the public hostname. The public path runs through the
// Beget load balancer, which drops roughly a third of connections
// (project_beget_lb_drops_third_of_connections) -- probing through it would
// manufacture false alerts out of LB flakiness that has nothing to do with
// whether the app itself speaks HTTP. Probing the in-cluster Service also
// means this watcher works for an app with no public domain at all, though
// in practice loadCandidates only feeds it apps that already have a "url" in
// their summary.
//
// No readiness probe is added to the helm charts to catch this instead: a
// TCP probe would flag fanvk (a working long-poll bot that binds no port at
// all) as broken, and an HTTP probe would still pass for oxygen (which binds
// its port but never speaks HTTP) while the public domain still 502s. Both
// also touch 120+ live apps in the separate dada-argo repo. This watcher
// stays entirely inside this repo and changes no chart.
//
// Sends no email. The owner has ruled that alert mail stays scoped to
// crash/volume; this watcher only writes app_url_alerts for the console
// banner (app_alerts.go's loadAppAlerts) to read.
type appURLWatcher struct {
	h *Handler
}

// StartAppURLWatcher launches the URL-reality watcher goroutine. No-op
// off-cluster (local dev has no svc.cluster.local DNS and no reason to run
// this), matching the gate newAppHealthClientset already established for
// the other watchers -- reused here purely as an in-cluster signal, this
// watcher does not otherwise touch the Kubernetes API.
func (h *Handler) StartAppURLWatcher(ctx context.Context) {
	if newAppHealthClientset() == nil {
		log.Printf("app-url: no in-cluster client, watcher disabled")
		return
	}
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

// urlProbeCandidate is one app worth probing this tick: it has a public URL
// on record, a known service port, and is not a declared worker.
type urlProbeCandidate struct {
	Namespace string
	AppName   string
	Port      int
}

// parseURLProbeCandidate decides whether one resource_snapshots row is worth
// probing, purely from its summary JSON. Pure and unit-tested without a
// database.
//
// A worker (summary "worker": true) is excluded outright: apps.go already
// never hands a worker a default hostname, so a worker carrying a "url" can
// only be a custom domain the user attached on purpose, and this watcher has
// no business second-guessing that choice. An app with no "url" has nothing
// public to validate. An app with no numeric "port" is left alone rather
// than guessing a default: probing the wrong port would misclassify a
// perfectly healthy app.
func parseURLProbeCandidate(namespace, appName string, summaryRaw []byte) (urlProbeCandidate, bool) {
	if len(summaryRaw) == 0 {
		return urlProbeCandidate{}, false
	}
	var m map[string]any
	if err := json.Unmarshal(summaryRaw, &m); err != nil {
		return urlProbeCandidate{}, false
	}
	if worker, _ := m["worker"].(bool); worker {
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
	return urlProbeCandidate{Namespace: namespace, AppName: appName, Port: int(portVal)}, true
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
		addr := fmt.Sprintf("%s-service.%s.svc.cluster.local:%d", c.AppName, c.Namespace, c.Port)
		probeCtx, cancel := context.WithTimeout(ctx, appURLProbeTimeout)
		healthy, reason, detail := probeAppReality(probeCtx, addr, appURLProbeTimeout)
		cancel()
		w.recordProbeResult(ctx, c.Namespace, c.AppName, healthy, reason, detail)
	}
}

// probeAppReality is the single active check this watcher performs: dial
// the in-cluster Service address over TCP, and if that succeeds, send a
// minimal HTTP/1.1 GET and read whatever comes back.
//
//   - Dial fails (refused, timeout, no such host) -> urlProbeReasonNoListener.
//     Matches oxygen: an MTProto proxy binds 8443 for its own binary
//     protocol reachable from outside the mesh in ways this dial is not, so
//     in practice this also covers "binds nothing at all" (fanvk).
//   - Dial succeeds but the response does not start with "HTTP/" (garbage,
//     a raw binary reply, or nothing at all before the read deadline) ->
//     urlProbeReasonNotHTTP. Matches an app that owns the port but speaks
//     its own protocol on it, not HTTP.
//   - Any response starting with "HTTP/" is healthy, regardless of status
//     code: a 404 or 500 still proves the app answers HTTP requests, which
//     is the only thing a public hostname promises visitors.
func probeAppReality(ctx context.Context, addr string, timeout time.Duration) (healthy bool, reason, detail string) {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, urlProbeReasonNoListener, err.Error()
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))
	host := addr
	if h, _, splitErr := net.SplitHostPort(addr); splitErr == nil {
		host = h
	}
	request := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	if _, werr := conn.Write([]byte(request)); werr != nil {
		return false, urlProbeReasonNotHTTP, fmt.Sprintf("connected but request failed: %v", werr)
	}

	buf := make([]byte, 512)
	n, _ := conn.Read(buf)
	data := buf[:n]
	if looksLikeHTTPResponse(data) {
		return true, "", ""
	}
	return false, urlProbeReasonNotHTTP, notHTTPDetail(data)
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
