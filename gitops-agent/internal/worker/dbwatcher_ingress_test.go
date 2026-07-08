package worker

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// Locks the EDGE-safe delivery contract for managed Ingress: the generated nginx
// conf must ship base64-in-env + entrypoint-decode, NEVER a git-relative bind
// mount (proven not to resolve on edge agents). A regression to a relative mount
// would silently break findata-class endpoints.
func TestIngressComposeBlock_EdgeSafeDelivery(t *testing.T) {
	spec := renderer.VMIngressSpec{
		Host: "fin-data.pro", SSLRedirect: true, BasicAuth: "/etc/nginx/.htpasswd",
		TLS:   renderer.VMIngressTLS{Enabled: true, MinVersion: "1.2", CertPath: "/etc/nginx/certs/live/fin-data.pro/fullchain.pem", KeyPath: "/etc/nginx/certs/live/fin-data.pro/privkey.pem"},
		Rules: []renderer.VMIngressRule{{Path: "/api/", App: "backend", Port: 8001}, {Path: "/", App: "frontend", Port: 5173}},
	}
	block := ingressComposeBlock(spec, []string{"backend", "frontend"})

	env, ok := block["environment"].(map[string]any)
	if !ok || env["NGINX_CONF_B64"] == "" {
		t.Fatalf("expected NGINX_CONF_B64 env, got %+v", block["environment"])
	}
	decoded, err := base64.StdEncoding.DecodeString(env["NGINX_CONF_B64"].(string))
	if err != nil || !strings.Contains(string(decoded), "proxy_pass http://backend:8001;") {
		t.Fatalf("env conf must decode to the rendered nginx conf: err=%v", err)
	}

	ep, ok := block["entrypoint"].([]string)
	if !ok || len(ep) != 3 || !strings.Contains(ep[2], "base64 -d > /etc/nginx/conf.d/default.conf") {
		t.Fatalf("entrypoint must decode conf to disk before nginx, got %+v", block["entrypoint"])
	}

	// No git-relative bind mount may appear — only host-absolute certs/htpasswd.
	for _, v := range block["volumes"].([]string) {
		if strings.HasPrefix(v, "./") || strings.Contains(v, "nginx.conf") {
			t.Errorf("volume %q is a git-relative/conf mount — breaks edge delivery", v)
		}
	}

	if got := block["depends_on"].([]string); len(got) != 2 || got[0] != "backend" {
		t.Errorf("depends_on wrong: %+v", got)
	}
}
