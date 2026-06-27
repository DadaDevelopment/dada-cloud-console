package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func testPEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	return key, string(pemBytes)
}

func TestBuildAppJWT_IsValidRS256(t *testing.T) {
	key, pemStr := testPEM(t)

	tok, err := buildAppJWT("12345", pemStr)
	if err != nil {
		t.Fatalf("buildAppJWT: %v", err)
	}
	parsed, err := jwt.Parse(tok, func(t *jwt.Token) (any, error) { return &key.PublicKey, nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("token invalid: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["iss"] != "12345" {
		t.Fatalf("iss = %v, want 12345", claims["iss"])
	}
}

func TestMintInstallToken_ExchangesJWT(t *testing.T) {
	_, pemStr := testPEM(t)

	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_xxx",
			"expires_at": "2099-01-01T00:00:00Z",
		})
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	tok, exp, err := MintInstallToken(context.Background(), "12345", pemStr, 777)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if tok != "ghs_xxx" {
		t.Fatalf("token=%q want ghs_xxx", tok)
	}
	if exp.IsZero() {
		t.Fatalf("expiry not parsed")
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("auth=%q want bearer JWT", gotAuth)
	}
	if gotPath != "/app/installations/777/access_tokens" {
		t.Fatalf("path=%q", gotPath)
	}
}
