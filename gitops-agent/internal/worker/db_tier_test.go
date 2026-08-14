package worker

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const tierFixture = `apiVersion: platform.dada-tuda.ru/v1alpha1
kind: ServiceDatabaseV2
metadata:
  name: fonbet-db
spec:
  appRef: fonbet-value
  namespace: artemmendeleev-gmail-com-prod
  engine: postgresql
  database: odds-research
  enforcement: read-only
  extensions:
    - pgvector
  backup:
    enabled: true
    frequency: daily
    retention: 14d
`

func TestPatchDatabaseTierKeepsIdentity(t *testing.T) {
	got, changed, err := patchDatabaseTier(tierFixture, "fonbet-db", "free")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !changed {
		t.Fatal("expected a change")
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("patched manifest is not YAML: %v", err)
	}
	spec, _ := doc["spec"].(map[string]any)
	if spec["tier"] != "free" {
		t.Errorf("spec.tier = %v, want free", spec["tier"])
	}
	for key, want := range map[string]any{
		"database":    "odds-research",
		"appRef":      "fonbet-value",
		"namespace":   "artemmendeleev-gmail-com-prod",
		"enforcement": "read-only",
	} {
		if spec[key] != want {
			t.Errorf("spec.%s = %v, want %v (tier must not rewrite identity or enforcement)", key, spec[key], want)
		}
	}
	if backup, _ := spec["backup"].(map[string]any); backup["retention"] != "14d" {
		t.Errorf("backup policy lost by tier patch: %v", spec["backup"])
	}
}

// The reconciler re-decides the same tier every hour. An unchanged tier must
// report changed=false, or every database in the estate would produce a commit
// per tick.
func TestPatchDatabaseTierNoopWhenUnchanged(t *testing.T) {
	if _, changed, err := patchDatabaseTier(tierFixture, "fonbet-db", "unlimited"); err != nil || changed {
		t.Fatalf("absent tier must already count as unlimited: changed=%v err=%v", changed, err)
	}
	applied, _, err := patchDatabaseTier(tierFixture, "fonbet-db", "starter")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if _, changed, err := patchDatabaseTier(applied, "fonbet-db", "starter"); err != nil || changed {
		t.Fatalf("repeat of same tier must be a no-op: changed=%v err=%v", changed, err)
	}
}

// Going back to unlimited drops the field rather than writing tier: unlimited,
// so the manifest matches one that never carried a tier.
func TestPatchDatabaseTierUnlimitedDropsField(t *testing.T) {
	applied, _, err := patchDatabaseTier(tierFixture, "fonbet-db", "business")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	cleared, changed, err := patchDatabaseTier(applied, "fonbet-db", "unlimited")
	if err != nil || !changed {
		t.Fatalf("clearing must change the manifest: changed=%v err=%v", changed, err)
	}
	if strings.Contains(cleared, "tier") {
		t.Errorf("cleared manifest still carries tier:\n%s", cleared)
	}
}

// An operation for one database must never patch a different database that
// happens to share the values file.
func TestPatchDatabaseTierRejectsWrongName(t *testing.T) {
	if _, _, err := patchDatabaseTier(tierFixture, "other-db", "free"); err == nil {
		t.Fatal("expected an error when the manifest is a different database")
	}
}

// A tier outside the XRD enum is rejected by the API server at sync time and
// would wedge the whole app's Application, so the worker must not be able to
// write one.
func TestDatabaseTiersMatchChartEnum(t *testing.T) {
	want := []string{"unlimited", "internal", "free", "starter", "business"}
	if len(databaseTiers) != len(want) {
		t.Fatalf("databaseTiers has %d entries, want %d", len(databaseTiers), len(want))
	}
	for _, tier := range want {
		if !databaseTiers[tier] {
			t.Errorf("tier %q missing from databaseTiers", tier)
		}
	}
}
