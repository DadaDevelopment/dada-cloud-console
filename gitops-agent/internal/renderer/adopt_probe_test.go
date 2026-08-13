package renderer

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAdoptProbeRendersTheLiveAggregate is a pre-flight probe, not a unit test:
// point ADOPT_PROBE_DIR at a fresh clone's environment directory and it renders
// the aggregate exactly as doAdoptComposeStack would from that directory's
// apps/<ADOPT_PROBE_SOURCE>/compose.yaml, then diffs it against the aggregate
// compose.yaml the environment is running. It exists because proving a
// transformation is an identity on paper is not proof that the input the worker
// will read is the one you looked at.
func TestAdoptProbeRendersTheLiveAggregate(t *testing.T) {
	dir := os.Getenv("ADOPT_PROBE_DIR")
	if dir == "" {
		t.Skip("ADOPT_PROBE_DIR not set")
	}
	source := os.Getenv("ADOPT_PROBE_SOURCE")
	if source == "" {
		t.Fatal("ADOPT_PROBE_SOURCE not set")
	}

	raw, err := os.ReadFile(filepath.Join(dir, "apps", source, "compose.yaml"))
	if err != nil {
		t.Fatalf("read source compose: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse source compose: %v", err)
	}
	servicesRaw, _ := doc["services"].(map[string]any)
	volumes, _ := doc["volumes"].(map[string]any)

	names := make([]string, 0, len(servicesRaw))
	for name := range servicesRaw {
		names = append(names, name)
	}
	sort.Strings(names)

	specs := make([]AppServiceSpec, 0, len(names))
	for _, name := range names {
		block, _ := servicesRaw[name].(map[string]any)
		specs = append(specs, AppServiceSpec{AppName: name, Service: block})
	}

	got, err := RenderAggregateCompose(specs, volumes)
	if err != nil {
		t.Fatalf("render aggregate: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("read live aggregate: %v", err)
	}

	var gotDoc, wantDoc map[string]any
	if err := yaml.Unmarshal([]byte(got), &gotDoc); err != nil {
		t.Fatalf("parse rendered aggregate: %v", err)
	}
	if err := yaml.Unmarshal(want, &wantDoc); err != nil {
		t.Fatalf("parse live aggregate: %v", err)
	}
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		out := filepath.Join(os.TempDir(), "adopt-probe-rendered.yaml")
		_ = os.WriteFile(out, []byte(got), 0o644)
		t.Fatalf("adopt would change the deployed stack; rendered aggregate written to %s", out)
	}
	if got != string(want) {
		t.Logf("adopt of %q deploys the same stack but rewrites %d bytes of YAML formatting", source, len(want))
	}
	t.Logf("adopt of %q renders the live stack (%d services)", source, len(specs))
}
