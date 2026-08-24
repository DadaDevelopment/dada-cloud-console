package ssh_test

import (
	"strings"
	"testing"

	dadash "github.com/dada-tuda/console/portainer-agent/internal/ssh"
)

func TestTenantScriptWritesTenantAndReloadsAgent(t *testing.T) {
	script, err := dadash.TenantScript("dbf07d18-c978-4ce2-885a-043b50efeaa4")
	if err != nil {
		t.Fatalf("TenantScript error: %v", err)
	}
	for _, want := range []string{
		"grep -qx 'PROM_TENANT=dbf07d18-c978-4ce2-885a-043b50efeaa4' /etc/dada/vm.env",
		"sed -i '/^PROM_TENANT=/d' /etc/dada/vm.env",
		"echo 'PROM_TENANT=dbf07d18-c978-4ce2-885a-043b50efeaa4' >> /etc/dada/vm.env",
		"docker ps --filter label=com.docker.compose.service=prometheus-agent -q | xargs -r docker restart",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q\n---\n%s", want, script)
		}
	}
}

func TestTenantScriptRefusesUnsafeTenant(t *testing.T) {
	for _, bad := range []string{"", "a'; rm -rf /; echo '", "tenant with space", "tenant\nnewline"} {
		if _, err := dadash.TenantScript(bad); err == nil {
			t.Errorf("TenantScript(%q) accepted an unsafe tenant id", bad)
		}
	}
}

func TestRenderBootstrapWritesPromTenant(t *testing.T) {
	script, err := dadash.RenderBootstrap(dadash.BootstrapParams{
		ServerName:               "vm-1",
		PrometheusRemoteWriteURL: "https://prometheus.dada-tuda.ru/api/v1/write",
		PrometheusUser:           "dada",
		PrometheusPass:           "secret",
		PromTenant:               "dbf07d18-c978-4ce2-885a-043b50efeaa4",
	})
	if err != nil {
		t.Fatalf("RenderBootstrap error: %v", err)
	}
	if !strings.Contains(script, "PROM_TENANT=dbf07d18-c978-4ce2-885a-043b50efeaa4") {
		t.Fatalf("bootstrap does not write PROM_TENANT into /etc/dada/vm.env:\n%s", script)
	}
}
