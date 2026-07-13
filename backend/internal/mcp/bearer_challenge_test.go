package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

func signedJWT(t *testing.T, claims jwt.RegisteredClaims) string {
	t.Helper()
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func TestExpOnlyVerifier(t *testing.T) {
	ctx := context.Background()

	ti, err := expOnlyVerifier(ctx, signedJWT(t, jwt.RegisteredClaims{
		Subject:   "user-1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}), nil)
	if err != nil {
		t.Fatalf("valid token: %v", err)
	}
	if ti.UserID != "user-1" || ti.Expiration.Before(time.Now()) {
		t.Errorf("bad TokenInfo: %+v", ti)
	}

	if _, err := expOnlyVerifier(ctx, signedJWT(t, jwt.RegisteredClaims{Subject: "x"}), nil); !errors.Is(err, sdkauth.ErrInvalidToken) {
		t.Errorf("missing exp should be ErrInvalidToken, got %v", err)
	}

	if _, err := expOnlyVerifier(ctx, "not-a-jwt", nil); !errors.Is(err, sdkauth.ErrInvalidToken) {
		t.Errorf("garbage should be ErrInvalidToken, got %v", err)
	}
}

func newTestHandler(t *testing.T, requireBearer bool) http.Handler {
	t.Helper()
	raw := []byte(`{"swagger":"2.0","basePath":"/api/v1","paths":{}}`)
	h, err := NewHandler(raw, Config{
		BackendURL:     "http://127.0.0.1:0",
		ResourceURL:    "https://console.dada-tuda.ru/mcp",
		KeycloakIssuer: "https://id.dada-tuda.ru/realms/master",
		RequireBearer:  requireBearer,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func TestChallengeOnMissingBearer(t *testing.T) {
	h := newTestHandler(t, true)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	wa := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wa, "resource_metadata=") || !strings.Contains(wa, "/.well-known/oauth-protected-resource") {
		t.Errorf("WWW-Authenticate missing resource_metadata: %q", wa)
	}
}

func TestWellKnownStaysPublic(t *testing.T) {
	h := newTestHandler(t, true)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("well-known want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "authorization_servers") {
		t.Errorf("well-known body unexpected: %s", rec.Body.String())
	}
}

func TestNoChallengeWhenDisabled(t *testing.T) {
	h := newTestHandler(t, false)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Errorf("local mode must not 401-challenge")
	}
}
