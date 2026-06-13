package renderer_test

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"gopkg.in/yaml.v3"
)

const publicApiCR = `apiVersion: platform.dada-tuda.ru/v1alpha1
kind: PublicApi
metadata:
  name: console
spec:
  route:
    prefix: /
`

const serviceDBCR = `apiVersion: platform.dada-tuda.ru/v1alpha1
kind: ServiceDatabaseV2
metadata:
  name: cloud-console
spec:
  database: cloud-console
`

func TestResourcesValues_EmptyParse(t *testing.T) {
	rv, err := renderer.ParseResourcesValues("")
	if err != nil {
		t.Fatalf("ParseResourcesValues(empty): %v", err)
	}
	if rv.Manifests == nil {
		t.Fatal("empty content must yield non-nil Manifests")
	}
	if len(rv.Manifests) != 0 {
		t.Fatalf("empty content must yield 0 manifests, got %d", len(rv.Manifests))
	}
	out, err := rv.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(out, "manifests: []") {
		t.Errorf("empty marshal must emit 'manifests: []', got:\n%s", out)
	}
}

func TestResourcesValues_UpsertAppendThenReplace(t *testing.T) {
	rv, _ := renderer.ParseResourcesValues("")
	if err := rv.Upsert(publicApiCR); err != nil {
		t.Fatalf("Upsert publicapi: %v", err)
	}
	if err := rv.Upsert(serviceDBCR); err != nil {
		t.Fatalf("Upsert servicedb: %v", err)
	}
	if len(rv.Manifests) != 2 {
		t.Fatalf("want 2 manifests, got %d", len(rv.Manifests))
	}

	// Re-upsert the PublicApi with a changed spec: replaces in place, no append,
	// and keeps its original slot (index 0) so ordering is stable.
	changed := strings.Replace(publicApiCR, "prefix: /", "prefix: /api", 1)
	if err := rv.Upsert(changed); err != nil {
		t.Fatalf("Upsert replace: %v", err)
	}
	if len(rv.Manifests) != 2 {
		t.Fatalf("re-upsert must not append, got %d manifests", len(rv.Manifests))
	}

	out, err := rv.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(out, "manifests:") {
		t.Errorf("missing top-level manifests key:\n%s", out)
	}
	if !strings.Contains(out, "prefix: /api") {
		t.Errorf("replaced spec not present:\n%s", out)
	}

	// Round-trip: the marshalled output parses back into 2 entries, and the
	// PublicApi (the replaced entry) still precedes the ServiceDatabaseV2.
	var doc struct {
		Manifests []struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		} `yaml:"manifests"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("re-parse marshalled output: %v", err)
	}
	if len(doc.Manifests) != 2 {
		t.Fatalf("re-parsed want 2, got %d", len(doc.Manifests))
	}
	if doc.Manifests[0].Kind != "PublicApi" || doc.Manifests[0].Metadata.Name != "console" {
		t.Errorf("entry 0 = %+v, want PublicApi/console", doc.Manifests[0])
	}
	if doc.Manifests[1].Kind != "ServiceDatabaseV2" || doc.Manifests[1].Metadata.Name != "cloud-console" {
		t.Errorf("entry 1 = %+v, want ServiceDatabaseV2/cloud-console", doc.Manifests[1])
	}
}

func TestResourcesValues_Remove(t *testing.T) {
	rv, _ := renderer.ParseResourcesValues("")
	_ = rv.Upsert(publicApiCR)
	_ = rv.Upsert(serviceDBCR)

	if removed := rv.Remove("PublicApi", "console"); !removed {
		t.Error("Remove existing must return true")
	}
	if len(rv.Manifests) != 1 {
		t.Fatalf("after remove want 1 manifest, got %d", len(rv.Manifests))
	}
	if removed := rv.Remove("PublicApi", "console"); removed {
		t.Error("Remove absent must return false")
	}

	out, _ := rv.Marshal()
	if strings.Contains(out, "kind: PublicApi") {
		t.Errorf("removed PublicApi still present:\n%s", out)
	}
	if !strings.Contains(out, "kind: ServiceDatabaseV2") {
		t.Errorf("surviving ServiceDatabaseV2 missing:\n%s", out)
	}
}

func TestResourcesValues_DeterministicMarshal(t *testing.T) {
	// Two identical upsert sequences must produce byte-identical output (no key
	// reordering churn), which is what keeps git diffs minimal.
	build := func() string {
		rv, _ := renderer.ParseResourcesValues("")
		_ = rv.Upsert(publicApiCR)
		_ = rv.Upsert(serviceDBCR)
		out, _ := rv.Marshal()
		return out
	}
	if build() != build() {
		t.Error("Marshal is not deterministic across identical builds")
	}

	// Parsing then re-marshalling without changes must also be stable.
	first := build()
	rv, err := renderer.ParseResourcesValues(first)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	second, _ := rv.Marshal()
	if first != second {
		t.Errorf("round-trip marshal changed output\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
