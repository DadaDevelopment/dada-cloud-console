package ssh_test

import (
	"strings"
	"testing"

	dadash "github.com/dada-tuda/console/portainer-agent/internal/ssh"
)

func TestRenderBootstrap(t *testing.T) {
	params := dadash.BootstrapParams{
		ServerName:               "test-server-1",
		EdgeKey:                  "aHR0cHM6Ly9wb3J0YWluZXI=",
		EdgeID:                   "550e8400-e29b-41d4-a716-446655440000",
		PrometheusRemoteWriteURL: "https://prometheus.dada-tuda.ru/api/v1/write",
		PrometheusUser:           "dada",
		PrometheusPass:           "secret",
		ElasticsearchURL:         "https://elastic.dada-tuda.ru:9200",
		ElasticsearchAPIKey:      "api_key_here",
	}

	script, err := dadash.RenderBootstrap(params)
	if err != nil {
		t.Fatalf("RenderBootstrap error: %v", err)
	}

	// Bootstrap now only drops the edge agent + the per-VM identity file. All
	// observability (metrics AND logs) comes from the fleet edge stack, keyed off
	// /etc/dada/vm.env (VM_NAME + PROM_*). No sidecars are installed here.
	checks := []string{
		"test-server-1",
		"aHR0cHM6Ly9wb3J0YWluZXI=",
		"550e8400-e29b-41d4-a716-446655440000",
		"VM_NAME=test-server-1",
		"PROM_REMOTE_WRITE_URL=https://prometheus.dada-tuda.ru/api/v1/write",
		"PROM_USER=dada",
		"/etc/dada/vm.env",
		"BOOTSTRAP_COMPLETE",
		"portainer/agent:2.21.0",
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Errorf("expected rendered script to contain %q", check)
		}
	}
	for _, gone := range observabilityMarkers {
		if strings.Contains(script, gone) {
			t.Errorf("bootstrap must NOT install sidecar %q (moved to the fleet edge stack)", gone)
		}
	}
}

// alwaysPresent are markers that must render in EVERY bootstrap, regardless of
// observability configuration — most importantly the Portainer Edge Agent,
// which is the critical path to the AppServer reaching Ready.
var alwaysPresent = []string{
	"BOOTSTRAP_COMPLETE",
	"portainer_edge_agent",
	"portainer/agent:2.21.0",
}

// observabilityMarkers are the sidecar-install command fragments that bootstrap
// must NEVER emit anymore — every observability sidecar (metrics + logs) now
// lives in the fleet edge stack, not the one-shot bootstrap.
var observabilityMarkers = []string{
	"--name prometheus-agent",
	"--name cadvisor",
	"--name node_exporter",
	"--name filebeat",
}

// TestRenderBootstrap_SkipsObservabilityForUnreachableEndpoints proves that a
// manual-connect VM leaves no crash-looping observability containers when the
// monitoring endpoints are either unset or in-cluster-only (unresolvable off
// the cluster network). The edge agent must still render in every case.
func TestRenderBootstrap_SkipsObservabilityForUnreachableEndpoints(t *testing.T) {
	cases := map[string]dadash.BootstrapParams{
		"empty endpoints": {
			ServerName: "vm-empty",
			EdgeKey:    "edgekey",
			EdgeID:     "edge-id-1",
		},
		"in-cluster endpoints (svc.cluster.local)": {
			ServerName:               "vm-incluster",
			EdgeKey:                  "edgekey",
			EdgeID:                   "edge-id-2",
			PrometheusRemoteWriteURL: "http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090/api/v1/write",
			PrometheusUser:           "dada",
			PrometheusPass:           "secret",
			ElasticsearchURL:         "http://elasticsearch-es-http.logging.svc.cluster.local:9200",
			ElasticsearchAPIKey:      "api_key_here",
		},
	}

	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			script, err := dadash.RenderBootstrap(params)
			if err != nil {
				t.Fatalf("RenderBootstrap error: %v", err)
			}
			for _, marker := range observabilityMarkers {
				if strings.Contains(script, marker) {
					t.Errorf("expected observability marker %q to be absent, but it rendered", marker)
				}
			}
			for _, marker := range alwaysPresent {
				if !strings.Contains(script, marker) {
					t.Errorf("expected critical marker %q to render, but it was absent", marker)
				}
			}
		})
	}
}

// TestRenderBootstrap_WritesFleetIdentityNoSidecars proves that with a public
// Prometheus endpoint, bootstrap writes the PROM_* fleet identity into
// /etc/dada/vm.env (for the edge stack's prometheus-agent) but installs NO
// sidecars itself — metrics + logs both come from the fleet edge stack.
func TestRenderBootstrap_WritesFleetIdentityNoSidecars(t *testing.T) {
	script, err := dadash.RenderBootstrap(dadash.BootstrapParams{
		ServerName:               "vm-mixed",
		EdgeKey:                  "edgekey",
		EdgeID:                   "edge-id-3",
		PrometheusRemoteWriteURL: "https://prometheus.dada-tuda.ru/api/v1/write",
		PrometheusUser:           "vmagent",
		PrometheusPass:           "secret",
	})
	if err != nil {
		t.Fatalf("RenderBootstrap error: %v", err)
	}
	for _, want := range []string{
		"VM_NAME=vm-mixed",
		"PROM_REMOTE_WRITE_URL=https://prometheus.dada-tuda.ru/api/v1/write",
		"PROM_USER=vmagent",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("expected vm.env to contain %q", want)
		}
	}
	for _, gone := range observabilityMarkers {
		if strings.Contains(script, gone) {
			t.Errorf("bootstrap must not install sidecar %q", gone)
		}
	}
}
