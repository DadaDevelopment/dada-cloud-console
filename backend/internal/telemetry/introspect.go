package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IntrospectionResult is the authoritative principal+tenant data user-service
// returns for a unified sk-dada- key. The tenant fields map 1:1 onto the same
// labels the legacy dmon_ path resolves from monitoring_apps, so the gateway
// label contract stays identical regardless of which key type authenticated.
type IntrospectionResult struct {
	Valid         bool     `json:"valid"`
	PrincipalID   string   `json:"principal_id"`
	Scopes        []string `json:"scopes"`
	ProjectID     string   `json:"project_id"`
	OrgID         string   `json:"org_id"`
	MonitoringApp string   `json:"monitoring_app"`
}

// introspectCacheEntry is a cached introspection outcome with its expiry.
type introspectCacheEntry struct {
	result    IntrospectionResult
	expiresAt time.Time
}

// Introspector resolves unified sk-dada- ingest keys via the user-service
// introspection endpoint, caching results locally by key-hash so the hot ingest
// path makes at most one network call per key per TTL window. The cache key is
// the SHA-256 of the raw key (the raw key is never stored or logged).
type Introspector struct {
	baseURL string
	hc      *http.Client
	ttl     time.Duration

	mu    sync.Mutex
	cache map[string]introspectCacheEntry
}

// IntrospectorOption tunes an Introspector.
type IntrospectorOption func(*Introspector)

// WithIntrospectTTL overrides the cache TTL (default 5 minutes).
func WithIntrospectTTL(ttl time.Duration) IntrospectorOption {
	return func(i *Introspector) {
		if ttl > 0 {
			i.ttl = ttl
		}
	}
}

// WithIntrospectHTTPClient overrides the HTTP client (tests inject a stub).
func WithIntrospectHTTPClient(hc *http.Client) IntrospectorOption {
	return func(i *Introspector) {
		if hc != nil {
			i.hc = hc
		}
	}
}

// NewIntrospector builds an Introspector against the user-service base URL.
// Returns nil when baseURL is empty so the gateway can treat unified-key
// resolution as unconfigured (sk-dada- keys then fail closed).
func NewIntrospector(baseURL string, opts ...IntrospectorOption) *Introspector {
	if strings.TrimSpace(baseURL) == "" {
		return nil
	}
	i := &Introspector{
		baseURL: strings.TrimRight(baseURL, "/"),
		hc:      &http.Client{Timeout: 5 * time.Second},
		ttl:     5 * time.Minute,
		cache:   make(map[string]introspectCacheEntry),
	}
	for _, opt := range opts {
		opt(i)
	}
	return i
}

// hashKey returns the SHA-256 hex digest of a raw key, used as the cache key.
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Introspect resolves a unified key to its principal+tenant data. It serves a
// fresh, non-expired cache entry without a network call. On a miss (or expiry)
// it calls user-service and caches the outcome. If user-service is unreachable
// it serves a stale cached entry when one exists; otherwise it fails closed
// (returns an error), so an unverifiable key is never accepted.
func (i *Introspector) Introspect(ctx context.Context, key string) (IntrospectionResult, error) {
	h := hashKey(key)

	if entry, ok := i.lookup(h); ok && time.Now().Before(entry.expiresAt) {
		return entry.result, nil
	}

	res, err := i.call(ctx, key)
	if err != nil {
		if entry, ok := i.lookup(h); ok {
			return entry.result, nil
		}
		return IntrospectionResult{}, err
	}

	i.store(h, res)
	return res, nil
}

func (i *Introspector) lookup(h string) (introspectCacheEntry, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	entry, ok := i.cache[h]
	return entry, ok
}

func (i *Introspector) store(h string, res IntrospectionResult) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.cache[h] = introspectCacheEntry{result: res, expiresAt: time.Now().Add(i.ttl)}
}

// call performs the POST /v1/apikeys/introspect request against user-service.
func (i *Introspector) call(ctx context.Context, key string) (IntrospectionResult, error) {
	body, err := json.Marshal(map[string]string{"api_key": key})
	if err != nil {
		return IntrospectionResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.baseURL+"/v1/apikeys/introspect", bytes.NewReader(body))
	if err != nil {
		return IntrospectionResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := i.hc.Do(req)
	if err != nil {
		return IntrospectionResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return IntrospectionResult{}, fmt.Errorf("introspection failed: status %d", resp.StatusCode)
	}

	var out IntrospectionResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return IntrospectionResult{}, err
	}
	return out, nil
}
