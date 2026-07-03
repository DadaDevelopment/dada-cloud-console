package worker

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/portainer-agent/internal/portainer"
)

func TestBuildDiscoveryResult(t *testing.T) {
	containers := []portainer.Container{
		{
			Names: []string{"/compose-postgres-1"},
			Image: "postgres:16",
			State: "running",
			Ports: []portainer.Port{{PrivatePort: 5432, Type: "tcp"}},
			Mounts: []portainer.Mount{
				{Type: "volume", Name: "compose_profi_pg_data", Destination: "/var/lib/postgresql/data", RW: true},
			},
		},
		{
			Names: []string{"/compose-nginx-1"},
			Image: "nginx:1.25",
			State: "running",
			Ports: []portainer.Port{{PublicPort: 443, PrivatePort: 443, Type: "tcp"}},
			Mounts: []portainer.Mount{
				{Type: "bind", Source: "/home/u/nginx.conf", Destination: "/etc/nginx/conf.d", RW: false},
			},
		},
	}

	// A platform sidecar with its own named volume must be excluded so it does
	// not pollute the inventory or the external-volume block.
	containers = append(containers, portainer.Container{
		Names: []string{"/portainer_edge_agent"},
		Image: "portainer/agent:2.21.0",
		State: "running",
		Mounts: []portainer.Mount{
			{Type: "volume", Name: "portainer_agent_data", Destination: "/data", RW: true},
		},
	})

	res := buildDiscoveryResult(3, containers)

	if len(res.Containers) != 2 {
		t.Fatalf("want 2 workload containers (sidecar excluded), got %d", len(res.Containers))
	}
	if strings.Contains(res.ExternalVolumesYAML, "portainer_agent_data") {
		t.Errorf("sidecar volume leaked into external-volume block:\n%s", res.ExternalVolumesYAML)
	}
	if res.Containers[0].Name != "compose-postgres-1" {
		t.Errorf("name not de-slashed: %q", res.Containers[0].Name)
	}
	if !strings.Contains(res.ExternalVolumesYAML, "name: compose_profi_pg_data") ||
		!strings.Contains(res.ExternalVolumesYAML, "external: true") {
		t.Errorf("external volume block missing the live PG volume:\n%s", res.ExternalVolumesYAML)
	}
	hasBindWarn := false
	hasSidecarWarn := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "/home/u/nginx.conf") {
			hasBindWarn = true
		}
		if strings.Contains(w, "sidecar") {
			hasSidecarWarn = true
		}
	}
	if !hasBindWarn {
		t.Errorf("bind-mount warning missing: %v", res.Warnings)
	}
	if !hasSidecarWarn {
		t.Errorf("sidecar-exclusion warning missing: %v", res.Warnings)
	}
	if res.Containers[1].Ports[0] != "443:443/tcp" {
		t.Errorf("published port not formatted: %v", res.Containers[1].Ports)
	}
}

func TestRenderExternalVolumesYAMLEmpty(t *testing.T) {
	out := renderExternalVolumesYAML(map[string]string{})
	if !strings.Contains(out, "no named volumes found") {
		t.Errorf("empty case should warn about bind mounts, got:\n%s", out)
	}
}
