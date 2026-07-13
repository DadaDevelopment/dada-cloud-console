package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMetadataHandler verifies the exported RFC 9728 protected-resource metadata
// handler (mounted at the host root by the backend router) returns the resource
// identifier and the Keycloak authorization server, so OAuth clients can discover
// the auth server before starting the flow.
func TestMetadataHandler(t *testing.T) {
	h := MetadataHandler(Config{
		ResourceURL:    "https://console.dada-tuda.ru/mcp",
		KeycloakIssuer: "https://id.dada-tuda.ru/realms/master",
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode metadata: %v (body=%s)", err, rec.Body.String())
	}
	if body.Resource != "https://console.dada-tuda.ru/mcp" {
		t.Errorf("resource = %q, want console mcp url", body.Resource)
	}
	if len(body.AuthorizationServers) != 1 || body.AuthorizationServers[0] != "https://id.dada-tuda.ru/realms/master" {
		t.Errorf("authorization_servers = %v, want [id.dada-tuda.ru/realms/master]", body.AuthorizationServers)
	}
}

// TestNewHandlerServesMetadataAtRoot confirms the /mcp-mounted handler still
// serves the metadata at its own root (the path the /mcp-prefixed URL reaches
// after StripPrefix), so the existing /mcp/.well-known path keeps working.
func TestNewHandlerServesMetadataAtRoot(t *testing.T) {
	h := newTestHandler(t, false)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
