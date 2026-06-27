package grafanaembed

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// Header names Grafana auth.proxy is configured to read. The gateway is the ONLY
// thing allowed to set them (Grafana's auth.proxy whitelist pins the gateway pod
// CIDR); the gateway strips any client-supplied copy on every request so a
// crafted browser request can never smuggle an identity through.
const (
	HeaderUser   = "X-Webauth-User"
	HeaderEmail  = "X-Webauth-Email"
	HeaderGroups = "X-Webauth-Groups"
)

// QueryParam is the iframe-URL parameter carrying the one-shot handoff token.
const QueryParam = "embed_token"

// nowFunc is overridable in tests.
type nowFunc func() time.Time

// Proxy is the reverse proxy that fronts grafana.dada-tuda.ru. Requests carrying
// a valid embed token (query param on the first hit, cookie thereafter) are
// authenticated: the gateway promotes the token to a sliding-session cookie and
// injects the auth.proxy identity headers. Requests with no/invalid token pass
// through untouched so direct admin SSO to grafana.dada-tuda.ru is unaffected.
type Proxy struct {
	secret     []byte
	cookieName string
	cookieDom  string        // cookie Domain (e.g. grafana.dada-tuda.ru); empty = host-only
	sessionTTL time.Duration // sliding cookie lifetime, refreshed each authed request
	rp         *httputil.ReverseProxy
	now        nowFunc
}

// Config configures a Proxy.
type Config struct {
	UpstreamURL  string        // internal Grafana base, e.g. http://kube-prometheus-stack-monitoring-grafana.monitoring.svc.cluster.local
	Secret       []byte        // shared HMAC secret (same as the API uses to mint)
	CookieName   string        // default "dada_grafana_embed"
	CookieDom    string        // cookie Domain; empty = host-only on the request host
	UpstreamHost string        // Host header to send upstream (e.g. grafana.dada-tuda.ru); empty = keep upstream URL host
	SessionTTL   time.Duration // default 30m
}

// NewProxy builds a Proxy. Returns an error if the upstream URL is unparseable.
func NewProxy(cfg Config) (*Proxy, error) {
	u, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return nil, err
	}
	name := cfg.CookieName
	if name == "" {
		name = "dada_grafana_embed"
	}
	ttl := cfg.SessionTTL
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	baseDirector := rp.Director
	rp.Director = func(r *http.Request) {
		baseDirector(r)
		if cfg.UpstreamHost != "" {
			r.Host = cfg.UpstreamHost
		}
	}
	return &Proxy{
		secret:     cfg.Secret,
		cookieName: name,
		cookieDom:  cfg.CookieDom,
		sessionTTL: ttl,
		rp:         rp,
		now:        time.Now,
	}, nil
}

// ServeHTTP authenticates (best-effort) then reverse-proxies to Grafana.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Anti-spoof: never trust client-supplied identity headers.
	r.Header.Del(HeaderUser)
	r.Header.Del(HeaderEmail)
	r.Header.Del(HeaderGroups)

	now := p.now()
	if claims := p.authenticate(r, now); claims != nil {
		r.Header.Set(HeaderUser, claims.User)
		if claims.Email != "" {
			r.Header.Set(HeaderEmail, claims.Email)
		}
		if g := claims.GroupsHeader(); g != "" {
			r.Header.Set(HeaderGroups, g)
		}
		// Slide the session: re-mint a fresh cookie token so an actively-viewed
		// dashboard stays authenticated past the short handoff-token TTL.
		if refreshed, err := Sign(p.secret, *claims, now, p.sessionTTL); err == nil {
			p.setCookie(w, r, refreshed)
		}
		// Drop the one-shot token from the upstream query so it never reaches
		// Grafana logs; identity now rides the header.
		stripQueryParam(r)
	}
	p.rp.ServeHTTP(w, r)
}

// authenticate returns valid claims from the query token (preferred, fresh) or
// the session cookie, or nil when neither is present/valid.
func (p *Proxy) authenticate(r *http.Request, now time.Time) *Claims {
	if tok := r.URL.Query().Get(QueryParam); tok != "" {
		if c, err := Verify(p.secret, tok, now); err == nil {
			return c
		}
	}
	if ck, err := r.Cookie(p.cookieName); err == nil && ck.Value != "" {
		if c, err := Verify(p.secret, ck.Value, now); err == nil {
			return c
		}
	}
	return nil
}

func (p *Proxy) setCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     p.cookieName,
		Value:    token,
		Path:     "/",
		Domain:   p.cookieDom,
		Expires:  p.now().Add(p.sessionTTL),
		MaxAge:   int(p.sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		// Cross-site: the console (different subdomain) frames Grafana, so the
		// cookie is sent in a third-party context. None+Secure is required for
		// the browser to attach it — matches Grafana's cookie_samesite=none.
		SameSite: http.SameSiteNoneMode,
	})
}

// stripQueryParam removes embed_token from the request URL in place.
func stripQueryParam(r *http.Request) {
	if !strings.Contains(r.URL.RawQuery, QueryParam+"=") {
		return
	}
	q := r.URL.Query()
	q.Del(QueryParam)
	r.URL.RawQuery = q.Encode()
}
