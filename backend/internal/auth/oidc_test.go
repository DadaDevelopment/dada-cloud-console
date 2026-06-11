package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// mockKeycloak is an httptest server that serves an OIDC discovery document and a
// JWKS built from in-memory RSA keys, and mints tokens signed by them. It lets
// the verifier run fully offline.
type mockKeycloak struct {
	server  *httptest.Server
	keys    map[string]*rsa.PrivateKey // kid -> private key
	issuer  string
	jwksHit int
}

func newMockKeycloak(t *testing.T) *mockKeycloak {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen rsa key: %v", err)
	}
	mk := &mockKeycloak{keys: map[string]*rsa.PrivateKey{"key-1": priv}}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":   mk.issuer,
			"jwks_uri": mk.issuer + "/protocol/openid-connect/certs",
		})
	})
	mux.HandleFunc("/protocol/openid-connect/certs", func(w http.ResponseWriter, r *http.Request) {
		mk.jwksHit++
		var keys []map[string]string
		for kid, k := range mk.keys {
			keys = append(keys, jwkFromPublic(kid, &k.PublicKey))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	})

	mk.server = httptest.NewServer(mux)
	mk.issuer = mk.server.URL
	t.Cleanup(mk.server.Close)
	return mk
}

// jwkFromPublic encodes an RSA public key as a JWK map.
func jwkFromPublic(kid string, pub *rsa.PublicKey) map[string]string {
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	return map[string]string{
		"kid": kid,
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(eBytes),
	}
}

// mint signs a token with the given kid (must exist in mk.keys). claims overrides
// are applied on top of a valid baseline.
func (mk *mockKeycloak) mint(t *testing.T, kid string, mutate func(jwt.MapClaims)) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":                mk.issuer,
		"sub":                "kc-sub-123",
		"aud":                "account",
		"exp":                now.Add(time.Hour).Unix(),
		"iat":                now.Unix(),
		"preferred_username": "alice",
		"email":              "alice@example.com",
		"name":               "Alice Example",
		"groups":             []string{"/projects/acme/developer", "/platform-admins"},
		"realm_access":       map[string]any{"roles": []string{"user", "platform-admin"}},
		"resource_access": map[string]any{
			"service-client": map[string]any{"roles": []string{"mcp-user"}},
		},
	}
	if mutate != nil {
		mutate(claims)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(mk.keys[kid])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func newVerifier(t *testing.T, mk *mockKeycloak, verifyAud bool) *KeycloakVerifier {
	t.Helper()
	v, err := NewKeycloakVerifier(context.Background(), mk.issuer, verifyAud, "account", "service-client")
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	return v
}

func TestVerify_ValidTokenExtractsClaims(t *testing.T) {
	mk := newMockKeycloak(t)
	v := newVerifier(t, mk, false)

	kc, err := v.Verify(context.Background(), mk.mint(t, "key-1", nil))
	if err != nil {
		t.Fatalf("verify valid token: %v", err)
	}
	if kc.Subject != "kc-sub-123" {
		t.Errorf("sub = %q", kc.Subject)
	}
	if kc.PreferredUsername != "alice" || kc.Email != "alice@example.com" || kc.Name != "Alice Example" {
		t.Errorf("identity fields wrong: %+v", kc)
	}
	if len(kc.Groups) != 2 || kc.Groups[0] != "/projects/acme/developer" {
		t.Errorf("groups = %v", kc.Groups)
	}
	// realm roles + resource_access[service-client] roles merged.
	wantRoles := map[string]bool{"user": true, "platform-admin": true, "mcp-user": true}
	if len(kc.Roles) != 3 {
		t.Fatalf("roles = %v", kc.Roles)
	}
	for _, r := range kc.Roles {
		if !wantRoles[r] {
			t.Errorf("unexpected role %q in %v", r, kc.Roles)
		}
	}
}

func TestVerify_WrongIssuerRejected(t *testing.T) {
	mk := newMockKeycloak(t)
	v := newVerifier(t, mk, false)

	tok := mk.mint(t, "key-1", func(c jwt.MapClaims) { c["iss"] = "https://evil.example.com" })
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected wrong-issuer rejection")
	}
}

func TestVerify_ExpiredRejected(t *testing.T) {
	mk := newMockKeycloak(t)
	v := newVerifier(t, mk, false)

	tok := mk.mint(t, "key-1", func(c jwt.MapClaims) {
		c["exp"] = time.Now().Add(-time.Hour).Unix()
	})
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected expired rejection")
	}
}

func TestVerify_BadSignatureRejected(t *testing.T) {
	mk := newMockKeycloak(t)
	v := newVerifier(t, mk, false)

	// Sign with a different key but advertise an existing kid → signature fails.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": mk.issuer, "sub": "x", "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
	})
	tok.Header["kid"] = "key-1"
	signed, _ := tok.SignedString(other)

	if _, err := v.Verify(context.Background(), signed); err == nil {
		t.Fatal("expected bad-signature rejection")
	}
}

func TestVerify_UnknownKidTriggersRefresh(t *testing.T) {
	mk := newMockKeycloak(t)
	v := newVerifier(t, mk, false)

	// First valid call populates the cache (1 jwks hit).
	if _, err := v.Verify(context.Background(), mk.mint(t, "key-1", nil)); err != nil {
		t.Fatalf("warm-up verify: %v", err)
	}
	hitsAfterWarmup := mk.jwksHit

	// Rotate: add key-2 server-side, mint a token with the new kid. The verifier
	// doesn't know key-2 yet → it must refresh JWKS and then succeed.
	priv2, _ := rsa.GenerateKey(rand.Reader, 2048)
	mk.keys["key-2"] = priv2

	// Force the throttle window open (test injects an old lastSync).
	v.mu.Lock()
	v.lastSync = time.Now().Add(-time.Minute)
	v.mu.Unlock()

	kc, err := v.Verify(context.Background(), mk.mint(t, "key-2", nil))
	if err != nil {
		t.Fatalf("verify after rotation: %v", err)
	}
	if kc.Subject != "kc-sub-123" {
		t.Errorf("sub = %q", kc.Subject)
	}
	if mk.jwksHit <= hitsAfterWarmup {
		t.Errorf("expected a JWKS refresh on unknown kid; hits %d -> %d", hitsAfterWarmup, mk.jwksHit)
	}
}

func TestVerify_AudienceCheckOnOff(t *testing.T) {
	mk := newMockKeycloak(t)

	// aud check ON, token aud "account" matches configured audience → ok.
	vOn := newVerifier(t, mk, true)
	if _, err := vOn.Verify(context.Background(), mk.mint(t, "key-1", nil)); err != nil {
		t.Fatalf("aud-on matching aud should pass: %v", err)
	}

	// aud check ON, wrong aud → rejected.
	tokWrongAud := mk.mint(t, "key-1", func(c jwt.MapClaims) { c["aud"] = "some-other-client" })
	if _, err := vOn.Verify(context.Background(), tokWrongAud); err == nil {
		t.Fatal("aud-on with wrong aud should fail")
	}

	// aud check OFF, wrong aud → accepted (Keycloak access-token aud is often "account").
	vOff := newVerifier(t, mk, false)
	if _, err := vOff.Verify(context.Background(), tokWrongAud); err != nil {
		t.Fatalf("aud-off should ignore aud: %v", err)
	}
}
