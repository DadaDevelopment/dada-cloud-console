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
		PrometheusRemoteWriteURL: "http://prometheus/api/v1/write",
		PrometheusUser:           "dada",
		PrometheusPass:           "secret",
		ElasticsearchURL:         "http://elastic:9200",
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
		"http://prometheus/api/v1/write",
		"http://elastic:9200",
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
