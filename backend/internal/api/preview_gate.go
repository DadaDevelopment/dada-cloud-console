package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// PreviewGate serves the frame-embeddable live preview of a deployed app on a
// dedicated wildcard subdomain (*.PreviewHostBase, e.g. *.pv.dada-tuda.ru).
//
// Why it exists: the console's live-preview pane iframes the app's real URL,
// and any app that sends X-Frame-Options or CSP frame-ancestors (Spring
// Security, Django, helmet all do by default) blocks that iframe. Stripping
// those headers on the app's REAL domain would remove its clickjacking
// protection for everyone, so instead the gate reverse-proxies the app on a
// separate, unguessable preview hostname and rewrites only the frame-blocking
// headers there. The app's own domain is never touched.
//
// Security model: the hostname IS the capability. It embeds an HMAC of
// (app, environment) under PreviewHostSecret, so preview hosts cannot be
// enumerated or forged. On top of that the gate pins CSP frame-ancestors to
// the console origin, so even a leaked preview host cannot be framed by a
// third party, and marks every response X-Robots-Tag noindex. Session cookies
// of the app's real users live on the app's own hostname and are never sent
// to the preview host by the browser, which is what makes header-stripping
// safe here.
//
// Host label layout: "<app>-<env8>-<mac12>" where env8 is the first 8 hex
// chars of the environment UUID and mac12 is the first 12 hex chars of
// HMAC-SHA256(secret, "<app>|<envID>"). The app name may itself contain
// dashes, so parsing peels the fixed-size fields off the right.
type PreviewGate struct {
	pool      *pgxpool.Pool
	cfg       *config.Config
	clientset kubernetes.Interface
	next      http.Handler

	mu    sync.Mutex
	cache map[string]previewTarget
}

type previewTarget struct {
	svcURL    *url.URL
	appHost   string
	expiresAt time.Time
}

const previewTargetTTL = 60 * time.Second

// previewUpstreamUnavailableBody is what the user actually reads inside the
// preview pane when the gate cannot reach the app, so it has to say whose
// answer it is. The old body was the bare string "preview upstream
// unavailable": it named neither the sender nor the nature of the failure, and
// on 2026-08-08 a first-time user read it as a verdict about his own code,
// asked the assistant, and deleted the app twelve minutes after deploying
// (RestateUnreachablePhase in apps.go documents that incident). The assistant
// misread it the same way, which is the same class of defect the tool-error
// framing closed on the MCP path: our own failure text must announce that it
// is ours, and must not pretend to know a cause it has not proven. The gate
// genuinely does not know why the connection failed, so the text lists the
// real candidates instead of picking one.
const previewUpstreamUnavailableBody = "Предпросмотр Dada Cloud не смог открыть соединение с приложением (ответ шлюза предпросмотра, HTTP 502).\n\n" +
	"Это ответ платформы, а не вашего приложения: до самого приложения запрос не дошёл, поэтому его код тут ни при чём.\n\n" +
	"Обычные причины: приложение ещё поднимается после деплоя; оно не слушает тот порт, который указан в настройках; это не HTTP-приложение (например бот) и веб-порта у него нет вовсе; либо недоступна сама платформа.\n" +
	"Что именно из этого — покажет состояние приложения в консоли."

// previewErrorCodeHeader carries the machine-readable counterpart of
// previewUpstreamUnavailableBody. The body is delivered as text/plain straight
// into the iframe, so it cannot also carry a JSON sibling field the way
// live_error.go does; the header is the equivalent out-of-band channel for any
// consumer (monitoring, a future assistant tool) that must branch on what
// happened rather than pattern-match the Russian prose a human reads.
const previewErrorCodeHeader = "X-Dada-Preview-Error-Code"

const previewUpstreamUnavailableCode = "preview_upstream_unavailable"

// writePreviewUpstreamUnavailable is the single writer for the 502 both the
// live ErrorHandler and its test hit, so the header and the body can never
// drift apart.
func writePreviewUpstreamUnavailable(w http.ResponseWriter) {
	w.Header().Set(previewErrorCodeHeader, previewUpstreamUnavailableCode)
	http.Error(w, previewUpstreamUnavailableBody, http.StatusBadGateway)
}

// NewPreviewGate wraps next with preview-host dispatch: requests whose Host is
// under cfg.PreviewHostBase are proxied to the target app, everything else
// falls through to next. Returns next unchanged when the gate is not
// configured (no secret or no base), so wiring is always safe.
func NewPreviewGate(pool *pgxpool.Pool, cfg *config.Config, next http.Handler) http.Handler {
	if cfg.PreviewHostBase == "" || cfg.PreviewHostSecret == "" {
		log.Warn().Msg("preview gate disabled: PREVIEW_HOST_BASE or PREVIEW_HOST_SECRET empty")
		return next
	}
	g := &PreviewGate{
		pool:  pool,
		cfg:   cfg,
		next:  next,
		cache: map[string]previewTarget{},
	}
	if rc, err := rest.InClusterConfig(); err == nil {
		if cs, csErr := kubernetes.NewForConfig(rc); csErr == nil {
			g.clientset = cs
		}
	}
	return g
}

// PreviewHostFor computes the preview hostname for an app in an environment,
// or "" when the gate is not configured or the label would exceed the DNS
// 63-char limit. Pure function of its inputs so the ListApps enrichment and
// the gate itself can never disagree.
func PreviewHostFor(app string, envID uuid.UUID, cfg *config.Config) string {
	if cfg.PreviewHostBase == "" || cfg.PreviewHostSecret == "" {
		return ""
	}
	env8 := envID.String()[:8]
	label := fmt.Sprintf("%s-%s-%s", app, env8, previewMAC(app, envID, cfg.PreviewHostSecret))
	if len(label) > 63 {
		return ""
	}
	return label + "." + cfg.PreviewHostBase
}

func previewMAC(app string, envID uuid.UUID, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s|%s", app, envID.String())
	return hex.EncodeToString(mac.Sum(nil))[:12]
}

// parsePreviewLabel splits "<app>-<env8>-<mac12>" from the right; the app
// name may itself contain dashes so only the two trailing fixed-size fields
// are positional.
func parsePreviewLabel(label string) (app, env8, mac12 string, ok bool) {
	parts := strings.Split(label, "-")
	if len(parts) < 3 {
		return "", "", "", false
	}
	mac12 = parts[len(parts)-1]
	env8 = parts[len(parts)-2]
	app = strings.Join(parts[:len(parts)-2], "-")
	if len(mac12) != 12 || len(env8) != 8 || app == "" {
		return "", "", "", false
	}
	return app, env8, mac12, true
}

func (g *PreviewGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	suffix := "." + g.cfg.PreviewHostBase
	if !strings.HasSuffix(host, suffix) {
		g.next.ServeHTTP(w, r)
		return
	}
	label := strings.TrimSuffix(host, suffix)
	if strings.Contains(label, ".") {
		http.NotFound(w, r)
		return
	}

	target, err := g.resolve(r.Context(), label)
	if err != nil {
		log.Debug().Err(err).Str("host", host).Msg("preview gate: resolve failed")
		http.NotFound(w, r)
		return
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target.svcURL)
			pr.Out.Host = target.svcURL.Host
			pr.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error {
			scrubFrameHeaders(resp.Header, g.cfg.PublicBaseURL)
			rewriteLocation(resp.Header, target.appHost, host)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Debug().Err(err).Str("host", host).Msg("preview gate: upstream error")
			writePreviewUpstreamUnavailable(w)
		},
	}
	proxy.ServeHTTP(w, r)
}

// scrubFrameHeaders removes the app's frame-blocking headers and pins
// embedding to the console origin. The rest of the app's CSP is preserved:
// only the frame-ancestors directive is dropped, and the console-only
// frame-ancestors policy is added as a separate header (CSP is enforced as
// the intersection of all policies, so every other directive stays active).
func scrubFrameHeaders(h http.Header, consoleOrigin string) {
	h.Del("X-Frame-Options")
	for _, key := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		values := h.Values(key)
		if len(values) == 0 {
			continue
		}
		h.Del(key)
		for _, v := range values {
			if cleaned := dropFrameAncestors(v); cleaned != "" {
				h.Add(key, cleaned)
			}
		}
	}
	h.Add("Content-Security-Policy", "frame-ancestors 'self' "+consoleOrigin)
	h.Set("X-Robots-Tag", "noindex, nofollow")
}

func dropFrameAncestors(csp string) string {
	var kept []string
	for _, directive := range strings.Split(csp, ";") {
		d := strings.TrimSpace(directive)
		if d == "" {
			continue
		}
		if strings.EqualFold(strings.Fields(d)[0], "frame-ancestors") {
			continue
		}
		kept = append(kept, d)
	}
	return strings.Join(kept, "; ")
}

// rewriteLocation keeps same-app absolute redirects inside the preview host:
// an app that 302s to its own public hostname would otherwise navigate the
// iframe to the real domain, whose frame-blocking headers kill the preview.
func rewriteLocation(h http.Header, appHost, previewHost string) {
	loc := h.Get("Location")
	if loc == "" || appHost == "" {
		return
	}
	u, err := url.Parse(loc)
	if err != nil || u.Host == "" {
		return
	}
	if !strings.EqualFold(u.Host, appHost) {
		return
	}
	u.Scheme = "https"
	u.Host = previewHost
	h.Set("Location", u.String())
}

// resolve maps a preview host label to its upstream service URL, verifying
// the HMAC against every environment matching the label's UUID prefix. The
// app must have an App snapshot in the environment, so a valid MAC for a
// deleted app stops resolving as soon as its snapshot is gone.
func (g *PreviewGate) resolve(ctx context.Context, label string) (previewTarget, error) {
	g.mu.Lock()
	if t, ok := g.cache[label]; ok && time.Now().Before(t.expiresAt) {
		g.mu.Unlock()
		return t, nil
	}
	g.mu.Unlock()

	app, env8, mac12, ok := parsePreviewLabel(label)
	if !ok {
		return previewTarget{}, fmt.Errorf("bad preview label %q", label)
	}

	rows, err := g.pool.Query(ctx, `
		SELECT e.id, e.namespace, COALESCE(rs.summary_json->>'url', ''),
		       COALESCE((
		           SELECT pa.summary_json->'spec'->'upstream'->>'servicePort'
		           FROM resource_snapshots pa
		           WHERE pa.environment_id = e.id AND pa.kind = 'PublicApi'
		             AND pa.summary_json->>'app_name' = $2
		             AND pa.summary_json->'spec'->'upstream'->>'serviceName' = $2 || '-service'
		           LIMIT 1
		       ), '')
		FROM environments e
		JOIN resource_snapshots rs
		  ON rs.environment_id = e.id AND rs.kind = 'App' AND rs.name = $2
		WHERE e.id::text LIKE $1 || '%'`,
		env8, app)
	if err != nil {
		return previewTarget{}, err
	}
	defer rows.Close()

	var namespace, appURL, snapshotPort string
	found := false
	for rows.Next() {
		var id uuid.UUID
		var ns, u, sp string
		if scanErr := rows.Scan(&id, &ns, &u, &sp); scanErr != nil {
			return previewTarget{}, scanErr
		}
		if hmac.Equal([]byte(previewMAC(app, id, g.cfg.PreviewHostSecret)), []byte(mac12)) {
			namespace, appURL, snapshotPort, found = ns, u, sp, true
			break
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return previewTarget{}, rowsErr
	}
	if !found {
		return previewTarget{}, fmt.Errorf("no environment matches preview label %q", label)
	}

	port, err := g.servicePort(ctx, namespace, app, snapshotPort)
	if err != nil {
		return previewTarget{}, err
	}

	appHost := ""
	if appURL != "" {
		if u, parseErr := url.Parse(appURL); parseErr == nil {
			appHost = u.Host
		}
	}

	t := previewTarget{
		svcURL:    &url.URL{Scheme: "http", Host: fmt.Sprintf("%s-service.%s.svc.cluster.local:%d", app, namespace, port)},
		appHost:   appHost,
		expiresAt: time.Now().Add(previewTargetTTL),
	}
	g.mu.Lock()
	g.cache[label] = t
	g.mu.Unlock()
	log.Debug().Str("label", label).Str("upstream", t.svcURL.Host).Msg("preview gate: resolved")
	return t, nil
}

// servicePort resolves the app Service's port. Priority: the live cluster
// Service (authoritative, needs RBAC), then the PublicApi snapshot's
// spec.upstream.servicePort already fetched by resolve (covers every app with
// a platform-managed domain even without cluster RBAC), then the stored
// git_repos port, then 8080.
func (g *PreviewGate) servicePort(ctx context.Context, namespace, app, snapshotPort string) (int, error) {
	if g.clientset != nil {
		svc, err := g.clientset.CoreV1().Services(namespace).Get(ctx, app+"-service", metav1.GetOptions{})
		if err == nil && len(svc.Spec.Ports) > 0 {
			return int(svc.Spec.Ports[0].Port), nil
		}
		if err != nil {
			log.Debug().Err(err).Str("namespace", namespace).Str("app", app).Msg("preview gate: service lookup failed, using stored port")
		}
	}
	if p, convErr := strconv.Atoi(snapshotPort); convErr == nil && p > 0 {
		return p, nil
	}
	var port int
	err := g.pool.QueryRow(ctx,
		`SELECT COALESCE(port, 8080) FROM git_repos WHERE app_name = $1 LIMIT 1`, app).Scan(&port)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 8080, nil
		}
		return 0, err
	}
	if port <= 0 {
		port = 8080
	}
	return port, nil
}

// EnrichPreviewURL adds a computed, never-persisted "preview_url" field to
// every app summary that already carries a live "url": the address the
// console's live-preview iframe must use instead of the real domain.
func EnrichPreviewURL(apps []models.ResourceSnapshot, envID uuid.UUID, cfg *config.Config) {
	for i := range apps {
		if len(apps[i].SummaryJSON) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(apps[i].SummaryJSON, &m); err != nil {
			continue
		}
		rawURL, _ := m["url"].(string)
		if rawURL == "" {
			continue
		}
		host := PreviewHostFor(apps[i].Name, envID, cfg)
		if host == "" {
			continue
		}
		m["preview_url"] = "https://" + host
		if b, err := json.Marshal(m); err == nil {
			apps[i].SummaryJSON = b
		}
	}
}
