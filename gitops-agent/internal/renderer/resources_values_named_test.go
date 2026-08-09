package renderer

import (
	"strings"
	"testing"
)

const carrierValues = `manifests:
    - apiVersion: platform.dada-tuda.ru/v1alpha1
      kind: ServiceDatabaseV2
      metadata:
        name: test-db-1
      spec:
        appRef: test-db-1
        database: test-db-1
    - apiVersion: platform.dada-tuda.ru/v1alpha1
      kind: ServiceDatabaseV2
      metadata:
        name: recog-db
      spec:
        appRef: recog-db
        database: recog
`

// TestManifestOfKindNamedPicksTheRequestedDatabase covers the carrier app that
// holds every standalone database of a project: a lookup by kind alone returns
// the neighbour, and the shard patch is then rejected for targeting the wrong
// manifest.
func TestManifestOfKindNamedPicksTheRequestedDatabase(t *testing.T) {
	rv, err := ParseResourcesValues(carrierValues)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, ok, err := rv.ManifestOfKindNamed("ServiceDatabaseV2", "recog-db")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want the recog-db manifest", ok, err)
	}
	if want := "name: recog-db"; !strings.Contains(raw, want) {
		t.Fatalf("got a manifest without %q:\n%s", want, raw)
	}
	if strings.Contains(raw, "test-db-1") {
		t.Fatalf("the lookup returned the neighbouring database:\n%s", raw)
	}
}

func TestManifestOfKindNamedReportsAbsence(t *testing.T) {
	rv, err := ParseResourcesValues(carrierValues)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok, err := rv.ManifestOfKindNamed("ServiceDatabaseV2", "nope"); ok || err != nil {
		t.Fatalf("ok=%v err=%v, want a clean miss", ok, err)
	}
}
