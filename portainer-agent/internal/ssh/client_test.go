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

	checks := []string{
		"test-server-1",
		"aHR0cHM6Ly9wb3J0YWluZXI=",
		"550e8400-e29b-41d4-a716-446655440000",
		"https://prometheus.dada-tuda.ru/api/v1/write",
		"https://elastic.dada-tuda.ru:9200",
		"api_key_here",
		"BOOTSTRAP_COMPLETE",
		"portainer/agent:2.21.0",
		"node_exporter",
		"cadvisor",
		"filebeat",
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Errorf("expected rendered script to contain %q", check)
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

// observabilityMarkers are command-level fragments emitted ONLY when a public
// endpoint is configured (kept distinct from the explanatory comments, which
// also mention these container names). On external VMs that can't reach these
// endpoints they must be absent, else prometheus-agent / filebeat crash-loop.
var observabilityMarkers = []string{
	"--name prometheus-agent",
	"--enable-feature=agent",
	"--name filebeat",
	"/usr/local/bin/node_exporter",
	"--name cadvisor",
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

// TestRenderBootstrap_IndependentSidecarGating proves the two sidecars are gated
// independently: a public Prometheus endpoint with an in-cluster Elasticsearch
// endpoint renders prometheus-agent but skips filebeat.
func TestRenderBootstrap_IndependentSidecarGating(t *testing.T) {
	script, err := dadash.RenderBootstrap(dadash.BootstrapParams{
		ServerName:               "vm-mixed",
		EdgeKey:                  "edgekey",
		EdgeID:                   "edge-id-3",
		PrometheusRemoteWriteURL: "https://prometheus.dada-tuda.ru/api/v1/write",
		ElasticsearchURL:         "http://elasticsearch-es-http.logging.svc.cluster.local:9200",
	})
	if err != nil {
		t.Fatalf("RenderBootstrap error: %v", err)
	}
	if !strings.Contains(script, "--name prometheus-agent") {
		t.Error("expected prometheus-agent to render for a public remote_write endpoint")
	}
	if strings.Contains(script, "--name filebeat") {
		t.Error("expected filebeat to be skipped for an in-cluster Elasticsearch endpoint")
	}
}
