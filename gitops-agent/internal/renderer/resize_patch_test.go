package renderer

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const handMaintainedValues = `common:
  image:
    name: nexus.dada-tuda.ru/dada/gateway-service
    tag: develop-0.0.1-SNAPSHOT-18
  servicePort: 8080
  replicas: 1
  useDotEnv: "false"
  resources:
    requests:
      cpu: "100m"
      memory: "256Mi"
      ephemeral-storage: "200Mi"
    limits:
      cpu: "500m"
      memory: "512Mi"
      ephemeral-storage: "1Gi"
  extraEnv:
    - name: SPRING_PROFILES_ACTIVE
      value: prod
    - name: DATABASE_URL
      valueFrom:
        secretKeyRef:
          name: gateway-env
          key: DATABASE_URL
  pvc:
    size: 10Gi
    storageClass: longhorn-rwx
    path: /data
  serviceDatabase:
    engine: postgres
    name: gateway-db
`

func grown() AppResources {
	return AppResources{
		CPURequest:    "200m",
		MemoryRequest: "512Mi",
		CPULimit:      "1",
		MemoryLimit:   "1Gi",
	}
}

func TestPatchValuesResourcesKeepsEverythingItWasNotAskedToChange(t *testing.T) {
	out, err := PatchValuesResources(handMaintainedValues, grown())
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	var before, after map[string]any
	if err := yaml.Unmarshal([]byte(handMaintainedValues), &before); err != nil {
		t.Fatalf("parse input: %v", err)
	}
	if err := yaml.Unmarshal([]byte(out), &after); err != nil {
		t.Fatalf("parse output: %v", err)
	}
	b := before["common"].(map[string]any)
	a := after["common"].(map[string]any)

	for _, key := range []string{"image", "servicePort", "replicas", "useDotEnv", "extraEnv", "pvc", "serviceDatabase"} {
		if got, want := render(t, a[key]), render(t, b[key]); got != want {
			t.Errorf("patch changed common.%s\n got: %s\nwant: %s", key, got, want)
		}
	}
}

func TestPatchValuesResourcesWritesTheNewEnvelope(t *testing.T) {
	out, err := PatchValuesResources(handMaintainedValues, grown())
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	res := resourcesOf(t, out)
	requests := res["requests"].(map[string]any)
	limits := res["limits"].(map[string]any)

	if requests["cpu"] != "200m" || requests["memory"] != "512Mi" {
		t.Errorf("requests not applied: %v", requests)
	}
	if limits["cpu"] != "1" || limits["memory"] != "1Gi" {
		t.Errorf("limits not applied: %v", limits)
	}
}

// A resize is about CPU and memory. The ephemeral-storage limit somebody set by
// hand is not the autoscaler's to have an opinion about, and dropping it evicts
// the container the first time it writes past the node default.
func TestPatchValuesResourcesLeavesEphemeralStorageAlone(t *testing.T) {
	out, err := PatchValuesResources(handMaintainedValues, grown())
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	res := resourcesOf(t, out)
	if got := res["requests"].(map[string]any)["ephemeral-storage"]; got != "200Mi" {
		t.Errorf("ephemeral request = %v, want 200Mi", got)
	}
	if got := res["limits"].(map[string]any)["ephemeral-storage"]; got != "1Gi" {
		t.Errorf("ephemeral limit = %v, want 1Gi", got)
	}
}

func TestPatchValuesResourcesWritesEphemeralWhenTheCallerHasOne(t *testing.T) {
	r := grown()
	r.EphemeralRequest = "500Mi"
	r.EphemeralLimit = "2Gi"
	out, err := PatchValuesResources(handMaintainedValues, r)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	res := resourcesOf(t, out)
	if got := res["limits"].(map[string]any)["ephemeral-storage"]; got != "2Gi" {
		t.Errorf("ephemeral limit = %v, want 2Gi", got)
	}
}

// Kubernetes quantities are strings. "1" emitted bare parses as an integer and
// the chart hands the API server a manifest it rejects.
func TestPatchValuesResourcesQuotesBareNumbers(t *testing.T) {
	out, err := PatchValuesResources(handMaintainedValues, grown())
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !strings.Contains(out, `cpu: "1"`) {
		t.Errorf("cpu limit not quoted:\n%s", out)
	}
}

func TestPatchValuesResourcesBuildsAMissingResourcesBlock(t *testing.T) {
	input := "common:\n  replicas: 1\n"
	out, err := PatchValuesResources(input, grown())
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	res := resourcesOf(t, out)
	if got := res["limits"].(map[string]any)["memory"]; got != "1Gi" {
		t.Errorf("memory limit = %v, want 1Gi", got)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parse output: %v", err)
	}
	if got := parsed["common"].(map[string]any)["replicas"]; got != 1 {
		t.Errorf("replicas = %v, want 1", got)
	}
}

// A file the patcher does not recognise is left alone rather than rewritten
// into something the chart cannot read.
func TestPatchValuesResourcesRefusesAFileWithNoCommonBlock(t *testing.T) {
	if _, err := PatchValuesResources("replicaCount: 2\n", grown()); err == nil {
		t.Fatal("expected a refusal for values.yaml with no common: block")
	}
	if _, err := PatchValuesResources("", grown()); err == nil {
		t.Fatal("expected a refusal for an empty values.yaml")
	}
}

func TestPatchValuesResourcesRejectsUnparseableYAML(t *testing.T) {
	if _, err := PatchValuesResources("common:\n  - [unbalanced\n", grown()); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestPatchValuesResourcesIsIdempotent(t *testing.T) {
	once, err := PatchValuesResources(handMaintainedValues, grown())
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	twice, err := PatchValuesResources(once, grown())
	if err != nil {
		t.Fatalf("patch again: %v", err)
	}
	if once != twice {
		t.Errorf("second patch changed the file:\n%s\n---\n%s", once, twice)
	}
}

func resourcesOf(t *testing.T, values string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(values), &parsed); err != nil {
		t.Fatalf("parse values: %v", err)
	}
	common, ok := parsed["common"].(map[string]any)
	if !ok {
		t.Fatalf("no common block in:\n%s", values)
	}
	res, ok := common["resources"].(map[string]any)
	if !ok {
		t.Fatalf("no resources block in:\n%s", values)
	}
	return res
}

func render(t *testing.T, v any) string {
	t.Helper()
	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
