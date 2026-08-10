package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coreResourcesReadByBackend lists the core-API resources the backend reads
// through its clientset. Each one must be granted in the chart's status-reader
// ClusterRole: a missing verb does not fail the deploy, it turns the reader
// into a per-namespace "forbidden" log line and the surrounding pass into a
// silent no-op. limitranges is here because repairLimitRangeViolations shipped
// green and inert for two days while a user app sat at readyReplicas=0.
var coreResourcesReadByBackend = []string{
	"events",
	"limitranges",
	"namespaces",
	"persistentvolumeclaims",
	"pods",
	"resourcequotas",
	"services",
}

// TestChartGrantsEveryCoreResourceTheBackendReads pins the pairing between a
// clientset read in Go and a verb in the chart. It reads the template as text
// rather than rendering it, which is enough: the resource names appear
// verbatim under rules, and the file has no conditional resource lists.
func TestChartGrantsEveryCoreResourceTheBackendReads(t *testing.T) {
	path := filepath.Join("..", "..", "..", "helm", "dada-cloud-console", "templates", "rbac.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	chart := string(raw)

	for _, resource := range coreResourcesReadByBackend {
		if !strings.Contains(chart, "- "+resource+"\n") {
			t.Errorf("%s is read by the backend but never granted in %s -- the read will fail with \"forbidden\" in every namespace and the caller will skip silently", resource, path)
		}
	}
}
