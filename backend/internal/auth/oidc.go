package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// KeycloakClaims is the typed payload extracted from a verified Keycloak access
// token. Roles is the merged set of realm_access.roles and
// resource_access.<rolesClient>.roles. Groups is the full-path group list emitted
// by the Group Membership mapper (e.g. "/projects/acme/developer").
type KeycloakClaims struct {
	Subject           string
	PreferredUsername string
	Email             string
	Name              string
	Groups            []string
	Roles             []string

	// Native OIDC scope claim (space-delimited). dada-cloud decodes authz from
	// Groups + Scope; there is no pre-shaped org_role/projects claim (ADR-009).
	Scope string
}

// rawKeycloakClaims mirrors the JSON shape of a Keycloak access token. Only the
// fields we consume are declared.
type rawKeycloakClaims struct {
	jwt.RegisteredClaims
	PreferredUsername string   `json:"preferred_username"`
	Email             string   `json:"email"`
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`
	Scope             string   `json:"scope"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
	ResourceAccess map[string]struct {
		Roles []string `json:"roles"`
	} `json:"resource_access"`
}

// jwk is a single RSA key from a JWKS document.
type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDoc struct {
	Keys []jwk `json:"keys"`
}

// KeycloakVerifier validates Keycloak RS256 access tokens against the realm's
// JWKS. The JWKS is fetched lazily and cached; an unknown kid triggers a single
// refresh before the token is rejected (covers Keycloak key rotation). It is
// safe for concurrent use.
type KeycloakVerifier struct {
	issuer      string
	jwksURL     string
	verifyAud   bool
	audience    string
	rolesClient string
	httpClient  *http.Client

	mu       sync.RWMutex
	keys     map[string]*rsa.PublicKey
	lastSync time.Time
}

// oidcDiscovery is the subset of the issuer's .well-known/openid-configuration
// we need to locate the JWKS endpoint.
type oidcDiscovery struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// NewKeycloakVerifier builds a verifier by discovering the JWKS endpoint from the
// issuer's .well-known/openid-configuration. The call performs one HTTP request
// (discovery); JWKS keys are fetched on first verification. A localhost issuer
// (httptest server) works without modification, keeping tests hermetic.
func NewKeycloakVerifier(ctx context.Context, issuer string, verifyAud bool, audience, rolesClient string) (*KeycloakVerifier, error) {
	hc := &http.Client{Timeout: 10 * time.Second}

	discoURL := issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build discovery request: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery returned %d", resp.StatusCode)
	}
	var disco oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&disco); err != nil {
		return nil, fmt.Errorf("decode oidc discovery: %w", err)
	}
	if disco.JWKSURI == "" {
		return nil, fmt.Errorf("oidc discovery missing jwks_uri")
	}

	return &KeycloakVerifier{
		issuer:      issuer,
		jwksURL:     disco.JWKSURI,
		verifyAud:   verifyAud,
		audience:    audience,
		rolesClient: rolesClient,
		httpClient:  hc,
		keys:        map[string]*rsa.PublicKey{},
	}, nil
}

// Verify validates a raw bearer token (RS256, correct issuer, not expired,
// optional audience) and returns the typed Keycloak claims.
func (v *KeycloakVerifier) Verify(ctx context.Context, rawToken string) (*KeycloakClaims, error) {
	var rc rawKeycloakClaims

	parserOpts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithExpirationRequired(),
	}
	if v.verifyAud {
		parserOpts = append(parserOpts, jwt.WithAudience(v.audience))
	}
	parser := jwt.NewParser(parserOpts...)

	keyFunc := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("token missing kid header")
		}
		key, err := v.keyForKid(ctx, kid)
		if err != nil {
			return nil, err
		}
		return key, nil
	}

	token, err := parser.ParseWithClaims(rawToken, &rc, keyFunc)
	if err != nil {
		return nil, fmt.Errorf("validate keycloak token: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid keycloak token")
	}

	roles := append([]string{}, rc.RealmAccess.Roles...)
	if v.rolesClient != "" {
		if ra, ok := rc.ResourceAccess[v.rolesClient]; ok {
			roles = append(roles, ra.Roles...)
		}
	}

	return &KeycloakClaims{
		Subject:           rc.Subject,
		PreferredUsername: rc.PreferredUsername,
		Email:             rc.Email,
		Name:              rc.Name,
		Groups:            rc.Groups,
		Roles:             roles,
		Scope:             rc.Scope,
	}, nil
}

// keyForKid returns the cached RSA public key for kid, refreshing the JWKS once
// (rate-limited) if the kid is unknown — this covers Keycloak signing-key
// rotation without a fixed refresh interval.
func (v *KeycloakVerifier) keyForKid(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	key, ok := v.keys[kid]
	v.mu.RUnlock()
	if ok {
		return key, nil
	}

	// Unknown kid: refresh JWKS (cap refresh rate to avoid hammering on bad kids).
	if err := v.refreshJWKS(ctx); err != nil {
		return nil, err
	}

	v.mu.RLock()
	key, ok = v.keys[kid]
	v.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no signing key for kid %q", kid)
	}
	return key, nil
}

// refreshJWKS fetches the JWKS document and replaces the cached key set. Refresh
// is throttled to at most once per 10s for unknown-kid bursts; the throttle is
// bypassed on the very first fetch (lastSync zero).
func (v *KeycloakVerifier) refreshJWKS(ctx context.Context) error {
	v.mu.Lock()
	if !v.lastSync.IsZero() && time.Since(v.lastSync) < 10*time.Second {
		v.mu.Unlock()
		return nil
	}
	v.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("build jwks request: %w", err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks endpoint returned %d", resp.StatusCode)
	}

	var doc jwksDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := parseRSAPublicKey(k.N, k.E)
		if err != nil {
			continue // skip malformed key, keep the rest
		}
		keys[k.Kid] = pub
	}

	v.mu.Lock()
	v.keys = keys
	v.lastSync = time.Now()
	v.mu.Unlock()
	return nil
}

// parseRSAPublicKey reconstructs an RSA public key from the base64url-encoded
// modulus (n) and exponent (e) of a JWK.
func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode jwk n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode jwk e: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	var e int
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("jwk has zero exponent")
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}
