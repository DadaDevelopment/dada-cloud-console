package worker

import (
	"context"
	"encoding/base64"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// Must compose-escape the env ref as $$ — a single $ is eaten by compose
	// interpolation at deploy time, leaving the shell an empty var (proven on edge).
	if !strings.Contains(ep[2], "$$NGINX_CONF_B64") {
		t.Errorf("entrypoint must reference $$NGINX_CONF_B64 (compose-escaped), got %q", ep[2])
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

// A custom domain is attached before its cert exists (DNS not delegated yet, and
// http-01 cannot be answered until the vhost serves on :80). nginx refuses to
// start against a missing ssl_certificate, so an eager 443 block would take the
// WHOLE ingress down — every site on the VM, over a domain that is not even
// pointed at us yet. The 443 block must therefore ship deferred: out of the main
// conf, installed by the entrypoint only when the cert file is really there.
func TestIngressComposeBlock_CustomDomainTLSIsDeferred(t *testing.T) {
	spec := renderer.VMIngressSpec{
		Host:        "fin-data.pro",
		SSLRedirect: true,
		TLS:         renderer.VMIngressTLS{Enabled: true, MinVersion: "1.2", CertPath: "/etc/nginx/certs/live/fin-data.pro/fullchain.pem", KeyPath: "/etc/nginx/certs/live/fin-data.pro/privkey.pem"},
		Rules:       []renderer.VMIngressRule{{Path: "/", App: "frontend", Port: 5173}},
		ExtraHosts: []renderer.VMExtraHost{{
			Host:     "shop.example.com",
			CertPath: "/etc/nginx/certs/live/shop.example.com/fullchain.pem",
			KeyPath:  "/etc/nginx/certs/live/shop.example.com/privkey.pem",
			App:      "storefront",
			Port:     3000,
		}},
	}
	block := ingressComposeBlock(spec, []string{"frontend", "storefront"})
	env := block["environment"].(map[string]any)
	decoded, err := base64.StdEncoding.DecodeString(env["NGINX_CONF_B64"].(string))
	if err != nil {
		t.Fatalf("decode conf: %v", err)
	}
	conf := string(decoded)
	if strings.Contains(conf, "/etc/nginx/certs/live/shop.example.com/") {
		t.Fatalf("custom-domain cert must not be referenced by the boot conf:\n%s", conf)
	}
	if !strings.Contains(conf, "include /etc/nginx/tls.d/*.conf;") {
		t.Errorf("boot conf must glob-include the deferred TLS dir:\n%s", conf)
	}
	if !strings.Contains(conf, "location /.well-known/acme-challenge/ {") {
		t.Errorf("http-01 challenge must be servable on :80:\n%s", conf)
	}
	if !strings.Contains(conf, "proxy_pass http://storefront:3000;") {
		t.Errorf("custom domain must serve over http while its cert is pending:\n%s", conf)
	}

	blocks, _ := env["NGINX_TLS_BLOCKS"].(string)
	line := strings.TrimSpace(blocks)
	parts := strings.Fields(line)
	if len(parts) != 3 || parts[0] != "/etc/nginx/certs/live/shop.example.com/fullchain.pem" {
		t.Fatalf("deferred block line must be `<cert> <key> <b64>`, got %q", line)
	}
	tls, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil || !strings.Contains(string(tls), "listen 443 ssl http2;") || !strings.Contains(string(tls), "server_name shop.example.com;") {
		t.Fatalf("deferred payload must be the host's 443 block: err=%v\n%s", err, tls)
	}

	ep := block["entrypoint"].([]string)
	for _, want := range []string{"$$NGINX_TLS_BLOCKS", `[ -f "$$cert" ] && [ -f "$$key" ] || continue`, "nginx -s reload"} {
		if !strings.Contains(ep[2], want) {
			t.Errorf("entrypoint missing %q:\n%s", want, ep[2])
		}
	}

	var mounted bool
	for _, v := range block["volumes"].([]string) {
		if v == "/var/lib/dada/acme:/var/www/acme:ro" {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("acme webroot must be mounted, got %v", block["volumes"])
	}
}

// Without custom domains nothing changes: no include, no challenge locations, no
// extra mounts — the findata-class ingress keeps rendering exactly as before.
func TestIngressComposeBlock_NoCustomDomainsIsUnchanged(t *testing.T) {
	spec := renderer.VMIngressSpec{
		Host: "fin-data.pro", SSLRedirect: true,
		TLS:   renderer.VMIngressTLS{Enabled: true, CertPath: "/c.pem", KeyPath: "/k.pem"},
		Rules: []renderer.VMIngressRule{{Path: "/", App: "frontend", Port: 5173}},
	}
	block := ingressComposeBlock(spec, nil)
	env := block["environment"].(map[string]any)
	if _, ok := env["NGINX_TLS_BLOCKS"]; ok {
		t.Errorf("no deferred blocks expected without custom domains")
	}
	decoded, _ := base64.StdEncoding.DecodeString(env["NGINX_CONF_B64"].(string))
	conf := string(decoded)
	if strings.Contains(conf, "acme-challenge") || strings.Contains(conf, "include /etc/nginx/tls.d") {
		t.Errorf("unchanged path must not gain acme/include wiring:\n%s", conf)
	}
	if !strings.Contains(conf, "return 301 https://fin-data.pro$request_uri;") {
		t.Errorf("plain http->https redirect must survive:\n%s", conf)
	}
	for _, v := range block["volumes"].([]string) {
		if strings.Contains(v, "acme") {
			t.Errorf("no acme mount expected, got %v", block["volumes"])
		}
	}
}

// Runs the real entrypoint script through /bin/sh with only the path prefix and
// the nginx binary stubbed. The shell is where the whole deferral either holds or
// silently degrades into "nginx never starts", and that failure mode is invisible
// to any Go-level assertion about the generated strings.
func TestIngressEntrypointScript_InstallsOnlyBlocksWhoseCertExists(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "etc", "nginx")
	if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	certDir := filepath.Join(tmp, "certs", "ready")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatal(err)
	}
	readyCert := filepath.Join(certDir, "fullchain.pem")
	readyKey := filepath.Join(certDir, "privkey.pem")
	for _, f := range []string{readyCert, readyKey} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	script := strings.ReplaceAll(ingressEntrypointScript, "$$", "$")
	script = strings.ReplaceAll(script, "/etc/nginx", root)

	readyBlock := renderer.RenderExtraHostTLS(renderer.VMExtraHost{
		Host: "ready.example.com", CertPath: readyCert, KeyPath: readyKey, App: "a", Port: 1,
	}, "1.2")
	pendingBlock := renderer.RenderExtraHostTLS(renderer.VMExtraHost{
		Host: "pending.example.com", CertPath: filepath.Join(tmp, "certs", "pending", "fullchain.pem"),
		KeyPath: filepath.Join(tmp, "certs", "pending", "privkey.pem"), App: "b", Port: 2,
	}, "1.2")
	blocks := readyCert + " " + readyKey + " " + base64.StdEncoding.EncodeToString([]byte(readyBlock)) + "\n" +
		filepath.Join(tmp, "certs", "pending", "fullchain.pem") + " " + filepath.Join(tmp, "certs", "pending", "privkey.pem") + " " +
		base64.StdEncoding.EncodeToString([]byte(pendingBlock)) + "\n"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := osexec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"NGINX_CONF_B64="+base64.StdEncoding.EncodeToString([]byte("server { listen 80; }\n")),
		"NGINX_TLS_BLOCKS="+blocks,
	)
	// A pipe would keep the test waiting on the background reload loop's inherited
	// stdout for an hour; files close with the foreground process.
	out, err := os.Create(filepath.Join(tmp, "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		logged, _ := os.ReadFile(filepath.Join(tmp, "out"))
		t.Fatalf("entrypoint failed: %v\n%s", err, logged)
	}

	conf, err := os.ReadFile(filepath.Join(root, "conf.d", "default.conf"))
	if err != nil || !strings.Contains(string(conf), "listen 80;") {
		t.Fatalf("boot conf not decoded to disk: err=%v got %q", err, conf)
	}

	installed, err := filepath.Glob(filepath.Join(root, "tls.d", "*.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 {
		t.Fatalf("exactly the cert-backed host may be installed, got %v", installed)
	}
	got, _ := os.ReadFile(installed[0])
	if !strings.Contains(string(got), "server_name ready.example.com;") {
		t.Fatalf("wrong block installed:\n%s", got)
	}
	if strings.Contains(string(got), "pending.example.com") {
		t.Fatalf("cert-less host must stay out of nginx config:\n%s", got)
	}
}

func TestCertbotComposeBlock_IssuesPerHostAndSurvivesFailures(t *testing.T) {
	block := certbotComposeBlock("web-ingress", "bot@dada-tuda.ru", []string{"a.example.com", "b.example.com"}, []string{"web-ingress"})

	env := block["environment"].(map[string]any)
	if env["CERTBOT_HOSTS"] != "a.example.com b.example.com" || env["CERTBOT_EMAIL"] != "bot@dada-tuda.ru" {
		t.Fatalf("hosts/email not wired: %+v", env)
	}
	script := block["entrypoint"].([]string)[2]
	// A domain is normally attached BEFORE its DNS is delegated, so the first
	// attempts fail. Without `|| true` the container dies and never retries, and
	// the domain stays certless forever.
	if !strings.Contains(script, "|| true") {
		t.Errorf("issuance failure must not kill the loop:\n%s", script)
	}
	// One cert per host: a single multi-SAN cert would be reissued (and briefly
	// invalidated) for every other domain each time one is attached or detached.
	if !strings.Contains(script, `--cert-name "$$h" -d "$$h"`) {
		t.Errorf("expected per-host issuance:\n%s", script)
	}
	if !strings.Contains(script, "--webroot -w /var/www/acme") {
		t.Errorf("must use the webroot nginx serves:\n%s", script)
	}
	if !strings.Contains(script, "while :;") || !strings.Contains(script, "--keep-until-expiring") {
		t.Errorf("renewal loop missing:\n%s", script)
	}

	vols := block["volumes"].([]string)
	var rwCerts, rwWebroot bool
	for _, v := range vols {
		if v == "/etc/letsencrypt:/etc/letsencrypt" {
			rwCerts = true
		}
		if v == "/var/lib/dada/acme:/var/www/acme" {
			rwWebroot = true
		}
	}
	if !rwCerts || !rwWebroot {
		t.Fatalf("certbot needs writable certs+webroot (nginx mounts both ro), got %v", vols)
	}
	if got := block["depends_on"].([]string); len(got) != 1 || got[0] != "web-ingress" {
		t.Errorf("certbot must start behind the vhost that answers the challenge, got %v", got)
	}
}
