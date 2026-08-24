package worker

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// TestHostPortFromPortString pins the guard that decides whether an environment
// already has something on :80/:443. Reading the CONTAINER side instead of the
// host side would let the platform add its own ingress under an app that is
// already serving the web ports, and the whole compose stack would fail to come
// up — taking down the app that was working.
func TestHostPortFromPortString(t *testing.T) {
	cases := map[string]int{
		"80:80":                80,
		"443:443":              443,
		"127.0.0.1:8080:80":    8080,
		"0.0.0.0:443:8443/tcp": 443,
		"8080:80/tcp":          8080,
		"80":                   0,
		"":                     0,
		"host:80":              0,
		"[::1]:80:8080":        80,
		"[::1]:8080:80":        8080,
		"127.0.0.1:19100:9100": 19100,
	}
	for in, want := range cases {
		if got := hostPortFromPortString(in); got != want {
			t.Errorf("hostPortFromPortString(%q) = %d, want %d", in, got, want)
		}
	}
}

// managedCatchAllMeta is the snapshot ensureManagedIngress persists when it
// creates the platform's own ingress: catch-all server_name, no rules, TLS
// enabled but no cert paths yet.
func managedCatchAllMeta() ingressMetaSnapshot {
	meta := ingressMetaSnapshot{Host: managedIngressCatchAllHost}
	meta.TLS.Enabled = true
	return meta
}

func decodeIngressConf(t *testing.T, block map[string]any) string {
	t.Helper()
	env, ok := block["environment"].(map[string]any)
	if !ok {
		t.Fatalf("ingress block has no environment map: %#v", block)
	}
	b64, ok := env["NGINX_CONF_B64"].(string)
	if !ok {
		t.Fatalf("ingress block carries no NGINX_CONF_B64: %#v", env)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode NGINX_CONF_B64: %v", err)
	}
	return string(raw)
}

// TestManagedCatchAllIngressIsSafeWithoutCerts checks the shape the platform
// creates for itself before any hostname is attached. The catch-all must not
// render a 443 block: nginx refuses to start on an ssl_certificate that points
// at nothing, and a crash-looping ingress takes every site on the VM with it.
func TestManagedCatchAllIngressIsSafeWithoutCerts(t *testing.T) {
	spec, deps, _ := ingressRebuildSpec(managedCatchAllMeta(), nil)
	block := ingressComposeBlock(spec, deps)
	conf := decodeIngressConf(t, block)

	if !strings.Contains(conf, "server_name _;") {
		t.Fatalf("catch-all vhost missing, conf:\n%s", conf)
	}
	if strings.Contains(conf, "listen 443") || strings.Contains(conf, "ssl_certificate") {
		t.Fatalf("catch-all rendered a TLS block without a cert, conf:\n%s", conf)
	}
	if _, ok := block["extra_hosts"]; ok {
		t.Fatalf("no loopback upstream yet, so no host-gateway mapping belongs here: %#v", block["extra_hosts"])
	}
	if _, ok := block["depends_on"]; ok {
		t.Fatalf("catch-all has no rules, so it must not depend on any service: %#v", block["depends_on"])
	}
	if len(deps) != 0 {
		t.Fatalf("deps = %v, want none", deps)
	}
}

// TestManagedIngressPublishesLoopbackHost is the whole point of the VM publish
// path: a service the tenant bound to 127.0.0.1 on the VM gets a public
// hostname. The vhost must proxy through the host gateway alias and the compose
// block must declare the mapping that makes that alias resolve — without it the
// upstream name does not exist inside the container and nginx fails to start.
func TestManagedIngressPublishesLoopbackHost(t *testing.T) {
	hosts := []ingressCustomHost{{
		Host:         "harness.dada-tuda.ru",
		Port:         8001,
		HostLoopback: true,
		Managed:      true,
	}}
	spec, deps, _ := ingressRebuildSpec(managedCatchAllMeta(), hosts)
	block := ingressComposeBlock(spec, deps)
	conf := decodeIngressConf(t, block)

	if !strings.Contains(conf, "server_name harness.dada-tuda.ru;") {
		t.Fatalf("attached hostname has no vhost, conf:\n%s", conf)
	}
	want := "proxy_pass http://" + renderer.VMHostGatewayAlias + ":8001;"
	if !strings.Contains(conf, want) {
		t.Fatalf("missing %q, conf:\n%s", want, conf)
	}
	gateway, ok := block["extra_hosts"].([]string)
	if !ok || len(gateway) != 1 || gateway[0] != renderer.VMHostGatewayMapping {
		t.Fatalf("extra_hosts = %#v, want [%q]", block["extra_hosts"], renderer.VMHostGatewayMapping)
	}
	if _, ok := block["depends_on"]; ok {
		t.Fatalf("a loopback upstream is not a compose service, so nothing to depend on: %#v", block["depends_on"])
	}
	if len(deps) != 0 {
		t.Fatalf("deps = %v, want none for a loopback host", deps)
	}
}

// TestManagedIngressServesACMEForAttachedHost covers issuance: the hostname is
// served over plain http until its cert exists, and the http-01 challenge
// location plus the deferred TLS include must be wired the moment there is a
// host to issue for. Skipping either leaves the hostname permanently on http.
func TestManagedIngressServesACMEForAttachedHost(t *testing.T) {
	hosts := []ingressCustomHost{{Host: "harness.dada-tuda.ru", Port: 8001, HostLoopback: true}}
	spec, deps, _ := ingressRebuildSpec(managedCatchAllMeta(), hosts)
	block := ingressComposeBlock(spec, deps)
	conf := decodeIngressConf(t, block)

	if !strings.Contains(conf, "/.well-known/acme-challenge/") {
		t.Fatalf("no ACME challenge location, cert can never be issued, conf:\n%s", conf)
	}
	if !strings.Contains(conf, "include "+ingressTLSIncludeDir+"/*.conf;") {
		t.Fatalf("no deferred TLS include, an issued cert would never be served, conf:\n%s", conf)
	}
	env := block["environment"].(map[string]any)
	blocks, ok := env["NGINX_TLS_BLOCKS"].(string)
	if !ok || !strings.Contains(blocks, "harness.dada-tuda.ru") {
		t.Fatalf("NGINX_TLS_BLOCKS = %#v, want a block for the attached host", env["NGINX_TLS_BLOCKS"])
	}
	vols, ok := block["volumes"].([]string)
	if !ok {
		t.Fatalf("ingress block has no volumes: %#v", block)
	}
	if !hasVolume(vols, ingressACMEWebrootSrc+":"+ingressACMEWebroot+":ro") {
		t.Fatalf("ACME webroot not mounted, challenge files unreachable: %#v", vols)
	}
	if !hasVolume(vols, "/etc/letsencrypt:/etc/nginx/certs:ro") {
		t.Fatalf("cert store not mounted: %#v", vols)
	}
}

func hasVolume(vols []string, want string) bool {
	for _, v := range vols {
		if v == want {
			return true
		}
	}
	return false
}
