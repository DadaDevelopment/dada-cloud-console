package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
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

// TestMintInstallTokenForRepos_NarrowsToNamedRepo: the repo-scoped variant must
// actually tell GitHub which repository the token is for. If the body were
// dropped the caller would silently receive an installation-wide credential —
// the failure mode this variant exists to prevent, and one no response field
// would reveal.
func TestMintInstallTokenForRepos_NarrowsToNamedRepo(t *testing.T) {
	_, pemStr := testPEM(t)

	var gotBody, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_scoped",
			"expires_at": "2099-01-01T00:00:00Z",
		})
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	tok, _, err := MintInstallTokenForRepos(context.Background(), "12345", pemStr, 777, []string{"console"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if tok != "ghs_scoped" {
		t.Fatalf("token=%q want ghs_scoped", tok)
	}
	if !strings.Contains(gotBody, `"repositories":["console"]`) {
		t.Fatalf("body=%q, want the repositories narrowing", gotBody)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type=%q", gotContentType)
	}
}

// TestMintInstallToken_SendsNoBody keeps the platform's own pipelines on the
// installation-wide token they already depend on: an empty repositories list
// must mean "send nothing", not "send an empty list", which GitHub would read as
// a token scoped to no repository at all.
func TestMintInstallToken_SendsNoBody(t *testing.T) {
	_, pemStr := testPEM(t)

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_xxx",
			"expires_at": "2099-01-01T00:00:00Z",
		})
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	if _, _, err := MintInstallToken(context.Background(), "12345", pemStr, 777); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if gotBody != "" {
		t.Fatalf("body=%q, want empty", gotBody)
	}
}

// TestMintInstallTokenForRepos_SurfacesRefusal: GitHub answers 422 when the
// named repository is not part of the installation. That refusal IS the
// authorization check for the agent endpoint, so it must reach the caller as an
// error rather than an empty token.
func TestMintInstallTokenForRepos_SurfacesRefusal(t *testing.T) {
	_, pemStr := testPEM(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"There is at least one repository that does not exist"}`))
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	tok, _, err := MintInstallTokenForRepos(context.Background(), "12345", pemStr, 777, []string{"not-mine"})
	if err == nil {
		t.Fatal("refusal reported as success")
	}
	if tok != "" {
		t.Fatalf("token=%q returned alongside error", tok)
	}
}
