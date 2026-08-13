package renderer

import (
	"os"
	"reflect"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAdoptRoundTripsLiveFindataStack proves the adoption transform is an
// identity on an already-adopted stack: splitting the LIVE fin-core/findata
// aggregate into per-service AppServiceSpecs and re-rendering it reproduces the
// same compose document.
//
// This is the safety gate for re-running doAdoptComposeStack against findata.
// That handler splits `services` into one spec per service (verbatim block),
// carries the top-level `volumes` through, and calls RenderAggregateCompose. If
// that round trip is lossless, re-adoption cannot change what prod runs — it
// only refreshes the console's snapshots, which are frozen at the 2026-07-08
// adoption while prod moved on.
//
// The fixture is the real aggregate Portainer deploys (argo-infra
// console-migration, clusters/beget-prod/projects/fin-core/environments/findata/
// compose.yaml). A diff here means re-adoption would mutate prod.
//
// Equality is asserted on the PARSED document, not on bytes. The live file was
// hand-edited (releases bump the image tag by hand), so nginx carries quoted
// "80:80" ports while postgres carries bare 65433:5432; the renderer emits both
// bare. YAML reads a colon without a following space as part of a plain scalar,
// so both forms parse to the same string — verified with a second, independent
// parser (Python PyYAML) as well as the one under test. Docker sees the parsed
// document, so semantic equality is the invariant that protects prod.
func TestAdoptRoundTripsLiveFindataStack(t *testing.T) {
	original, err := os.ReadFile("testdata/findata-live-compose.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(original, &doc); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	servicesRaw, _ := doc["services"].(map[string]any)
	if len(servicesRaw) == 0 {
		t.Fatal("fixture has no services")
	}
	volumes, _ := doc["volumes"].(map[string]any)

	names := make([]string, 0, len(servicesRaw))
	for name := range servicesRaw {
		names = append(names, name)
	}
	sort.Strings(names)

	var specs []AppServiceSpec
	for _, name := range names {
		block, _ := servicesRaw[name].(map[string]any)
		specs = append(specs, AppServiceSpec{AppName: name, Service: block})
	}

	got, err := RenderAggregateCompose(specs, volumes)
	if err != nil {
		t.Fatalf("render aggregate: %v", err)
	}

	var reparsed map[string]any
	if err := yaml.Unmarshal([]byte(got), &reparsed); err != nil {
		t.Fatalf("parse rendered: %v", err)
	}
	if !reflect.DeepEqual(doc, reparsed) {
		t.Errorf("re-adoption would MUTATE prod\n--- want (live) ---\n%s\n--- got (re-rendered) ---\n%s", original, got)
	}
}

// TestAdoptPreservesFindataDataSafety asserts the two invariants whose loss
// would be silent and expensive: the postgres external-volume name mapping
// (renaming it would attach an EMPTY volume over live prod data) and the nginx
// certbot bind mount (losing it breaks ACME renewal, surfacing weeks later as an
// expired certificate).
func TestAdoptPreservesFindataDataSafety(t *testing.T) {
	original, err := os.ReadFile("testdata/findata-live-compose.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(original, &doc); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	servicesRaw, _ := doc["services"].(map[string]any)
	volumes, _ := doc["volumes"].(map[string]any)

	names := make([]string, 0, len(servicesRaw))
	for name := range servicesRaw {
		names = append(names, name)
	}
	sort.Strings(names)
	var specs []AppServiceSpec
	for _, name := range names {
		block, _ := servicesRaw[name].(map[string]any)
		specs = append(specs, AppServiceSpec{AppName: name, Service: block})
	}

	got, err := RenderAggregateCompose(specs, volumes)
	if err != nil {
		t.Fatalf("render aggregate: %v", err)
	}
	var out map[string]any
	if err := yaml.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("parse rendered: %v", err)
	}

	vols, _ := out["volumes"].(map[string]any)
	pg, _ := vols["profi_pg_data"].(map[string]any)
	if pg == nil {
		t.Fatal("top-level volumes lost profi_pg_data: compose would mint an empty fin-core-findata_profi_pg_data over prod data")
	}
	if pg["external"] != true {
		t.Errorf("profi_pg_data external = %v, want true", pg["external"])
	}
	if pg["name"] != "compose_profi_pg_data" {
		t.Errorf("profi_pg_data name = %v, want compose_profi_pg_data", pg["name"])
	}

	svcs, _ := out["services"].(map[string]any)
	nginx, _ := svcs["nginx"].(map[string]any)
	if nginx == nil {
		t.Fatal("nginx service missing from rendered aggregate")
	}
	mounts, _ := nginx["volumes"].([]any)
	var hasCertbot bool
	for _, m := range mounts {
		if s, ok := m.(string); ok && s == "/var/www/certbot:/var/www/certbot:ro" {
			hasCertbot = true
		}
	}
	if !hasCertbot {
		t.Error("nginx lost the /var/www/certbot bind mount: ACME renewal would break silently")
	}
}
