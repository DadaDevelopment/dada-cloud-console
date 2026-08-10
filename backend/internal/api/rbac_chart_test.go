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

// writesByBackend lists the mutating calls the backend makes against workloads.
// A read that lacks its verb goes quiet; a write that lacks its verb leaves an
// app wedged with no way out, which is what patchDeploymentTemplateEnvelope
// exists to prevent.
var writesByBackend = []struct {
	apiGroup string
	resource string
	verb     string
	caller   string
}{
	{"", "pods/resize", "patch", "resizeLivePods"},
	{"apps", "deployments", "patch", "patchDeploymentTemplateEnvelope"},
}

// TestChartGrantsEveryWorkloadWriteTheBackendMakes checks that each mutating
// call has a rule naming its apiGroup, resource and verb together. Matching the
// three separately would pass on a chart that reads deployments and patches
// something else entirely.
func TestChartGrantsEveryWorkloadWriteTheBackendMakes(t *testing.T) {
	path := filepath.Join("..", "..", "..", "helm", "dada-cloud-console", "templates", "rbac.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	rules := strings.Split(string(raw), "- apiGroups:")

	for _, write := range writesByBackend {
		granted := false
		for _, rule := range rules {
			head, _, found := strings.Cut(rule, "\n")
			if !found || !strings.Contains(head, `"`+write.apiGroup+`"`) {
				continue
			}
			if strings.Contains(rule, "- "+write.resource+"\n") && strings.Contains(rule, `"`+write.verb+`"`) {
				granted = true
				break
			}
		}
		if !granted {
			t.Errorf("%s issues %s on %s/%s but no rule in %s grants it -- the call fails with \"forbidden\" and the app it was repairing stays wedged", write.caller, write.verb, write.apiGroup, write.resource, path)
		}
	}
}
