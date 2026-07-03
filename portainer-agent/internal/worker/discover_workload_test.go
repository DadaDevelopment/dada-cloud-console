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

	res := buildDiscoveryResult(3, containers)

	if len(res.Containers) != 2 {
		t.Fatalf("want 2 containers, got %d", len(res.Containers))
	}
	if res.Containers[0].Name != "compose-postgres-1" {
		t.Errorf("name not de-slashed: %q", res.Containers[0].Name)
	}
	if !strings.Contains(res.ExternalVolumesYAML, "name: compose_profi_pg_data") ||
		!strings.Contains(res.ExternalVolumesYAML, "external: true") {
		t.Errorf("external volume block missing the live PG volume:\n%s", res.ExternalVolumesYAML)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "/home/u/nginx.conf") {
		t.Errorf("bind-mount warning missing: %v", res.Warnings)
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
