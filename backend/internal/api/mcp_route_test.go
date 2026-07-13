package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	internalmcp "github.com/dada-tuda/console/backend/internal/mcp"
	"github.com/gin-gonic/gin"
)

// mcpRouteEngine mirrors the /mcp wiring in NewRouter: bare /mcp and /mcp/...
// both served on one handler with a path rewrite and NO HTTP redirect.
func mcpRouteEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h, err := internalmcp.NewHandler([]byte(`{"swagger":"2.0","basePath":"/api/v1","paths":{}}`), internalmcp.Config{
		BackendURL:     "http://127.0.0.1:0",
		ResourceURL:    "https://console.dada-tuda.ru/mcp",
		KeycloakIssuer: "https://id.dada-tuda.ru/realms/master",
		RequireBearer:  true,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	serve := func(c *gin.Context) {
		req := c.Request
		p := strings.TrimPrefix(req.URL.Path, "/mcp")
		if p == "" {
			p = "/"
		}
		req.URL.Path = p
		h.ServeHTTP(c.Writer, req)
	}
	r := gin.New()
	r.Any("/mcp", serve)
	r.Any("/mcp/*path", serve)
	return r
}

func TestMCPRouteNoRedirect(t *testing.T) {
	r := mcpRouteEngine(t)

	cases := []struct {
		path string
		want int
	}{
		{"/mcp", http.StatusUnauthorized},
		{"/mcp/", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code == http.StatusPermanentRedirect || rec.Code == http.StatusMovedPermanently {
			t.Errorf("%s must not redirect, got %d -> %s", tc.path, rec.Code, rec.Header().Get("Location"))
		}
		if rec.Code != tc.want {
			t.Errorf("%s: want %d, got %d", tc.path, tc.want, rec.Code)
		}
		if tc.path == "/mcp" && !strings.Contains(rec.Header().Get("WWW-Authenticate"), "resource_metadata=") {
			t.Errorf("bare /mcp missing WWW-Authenticate challenge")
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp/.well-known/oauth-protected-resource", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "authorization_servers") {
		t.Errorf("well-known via route: code=%d body=%s", rec.Code, rec.Body.String())
	}
}
