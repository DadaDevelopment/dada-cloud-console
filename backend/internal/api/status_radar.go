package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// statusRadarCacheTTL bounds how often the public status probe actually hits
// the upstream competitor domains. Anonymous, unauthenticated traffic to this
// route must never be able to hammer third-party sites, so every request
// inside the window is served the last snapshot.
const statusRadarCacheTTL = 60 * time.Second

// statusRadarProbeTimeout bounds a single target probe. All targets run
// concurrently, so the worst-case handler latency stays close to this value
// regardless of how many targets are configured.
const statusRadarProbeTimeout = 5 * time.Second

// statusRadarTarget is one competitor PaaS probed by the status radar, in the
// fixed order the response contract requires.
type statusRadarTarget struct {
	ID     string
	Name   string
	Target string
}

// statusRadarTargets is the ordered target list. The response "services"
// array MUST preserve this order — the frontend Status Radar landing renders
// rows positionally.
var statusRadarTargets = []statusRadarTarget{
	{ID: "vercel", Name: "Vercel", Target: "https://vercel.com"},
	{ID: "railway", Name: "Railway", Target: "https://railway.com"},
	{ID: "render", Name: "Render", Target: "https://render.com"},
	{ID: "netlify", Name: "Netlify", Target: "https://www.netlify.com"},
	{ID: "heroku", Name: "Heroku", Target: "https://www.heroku.com"},
	{ID: "fly", Name: "Fly.io", Target: "https://fly.io"},
}

// statusRadarService is one probed target in the public status response.
type statusRadarService struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Target     string `json:"target"`
	Reachable  bool   `json:"reachable"`
	HTTPStatus int    `json:"http_status"`
	LatencyMs  int    `json:"latency_ms"`
	TLSOk      bool   `json:"tls_ok"`
}

// statusRadarResponse is the public status radar snapshot.
type statusRadarResponse struct {
	Vantage   string               `json:"vantage"`
	UpdatedAt string               `json:"updated_at"`
	Services  []statusRadarService `json:"services"`
}

// statusRadarCache holds the last computed snapshot behind a RWMutex so
// concurrent requests within the TTL window share one result instead of each
// re-probing every competitor.
type statusRadarCache struct {
	mu       sync.RWMutex
	snapshot *statusRadarResponse
	at       time.Time
}

var globalStatusRadarCache = &statusRadarCache{}

// get returns the cached snapshot if it is still within TTL, plus whether it
// was a hit.
func (c *statusRadarCache) get() (*statusRadarResponse, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.snapshot == nil || time.Since(c.at) > statusRadarCacheTTL {
		return nil, false
	}
	return c.snapshot, true
}

// set stores a freshly computed snapshot as the current cache entry.
func (c *statusRadarCache) set(snapshot *statusRadarResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot = snapshot
	c.at = time.Now()
}

// probeStatusRadarTarget performs a single GET against target, following
// redirects, and reports reachability/status/latency/TLS state. It never
// returns an error — every failure mode (timeout, DNS, TLS, connection
// refused) collapses into reachable=false so the target row is never
// dropped from the response.
func probeStatusRadarTarget(client *http.Client, t statusRadarTarget) statusRadarService {
	svc := statusRadarService{
		ID:     t.ID,
		Name:   t.Name,
		Target: t.Target,
	}

	start := time.Now()
	resp, err := client.Get(t.Target)
	svc.LatencyMs = int(time.Since(start).Milliseconds())
	if err != nil {
		svc.Reachable = false
		svc.HTTPStatus = 0
		svc.TLSOk = false
		return svc
	}
	defer resp.Body.Close()

	svc.Reachable = true
	svc.HTTPStatus = resp.StatusCode
	svc.TLSOk = resp.TLS != nil
	return svc
}

// runStatusRadarProbe probes every configured target concurrently and
// assembles the response in the fixed target order.
func runStatusRadarProbe() *statusRadarResponse {
	client := &http.Client{
		Timeout: statusRadarProbeTimeout,
	}

	services := make([]statusRadarService, len(statusRadarTargets))
	var wg sync.WaitGroup
	for i, target := range statusRadarTargets {
		wg.Add(1)
		go func(i int, target statusRadarTarget) {
			defer wg.Done()
			services[i] = probeStatusRadarTarget(client, target)
		}(i, target)
	}
	wg.Wait()

	return &statusRadarResponse{
		Vantage:   "РФ (dada-cloud, beget-prod)",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Services:  services,
	}
}

// PublicStatusRadar reports live reachability of competitor PaaS platforms as
// probed from this RU-hosted server, powering the /status acquisition
// landing. Public, unauthenticated endpoint registered outside /api/v1 (like
// /health and /metrics) on purpose -- the data is a measured snapshot of
// third-party public homepages, not tenant data, so it carries no OpenAPI
// annotation and is exempt from the /api/v1 coverage gate the same way those
// routes are. Results are cached for statusRadarCacheTTL so repeated hits do
// not hammer the probed targets.
func (h *Handler) PublicStatusRadar(c *gin.Context) {
	if cached, ok := globalStatusRadarCache.get(); ok {
		c.JSON(http.StatusOK, cached)
		return
	}

	snapshot := runStatusRadarProbe()
	globalStatusRadarCache.set(snapshot)
	c.JSON(http.StatusOK, snapshot)
}
