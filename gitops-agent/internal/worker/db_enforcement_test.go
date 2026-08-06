package worker

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const enforcementFixture = `apiVersion: platform.dada-tuda.ru/v1alpha1
kind: ServiceDatabaseV2
metadata:
  name: fonbet-db
spec:
  appRef: fonbet-value
  namespace: artemmendeleev-gmail-com-prod
  engine: postgresql
  database: odds-research
  tier: business
  extensions:
    - pgvector
  backup:
    enabled: true
    frequency: daily
    retention: 14d
`

func TestPatchDatabaseEnforcementKeepsIdentity(t *testing.T) {
	got, changed, err := patchDatabaseEnforcement(enforcementFixture, "fonbet-db", "read-only")
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
	if spec["enforcement"] != "read-only" {
		t.Errorf("spec.enforcement = %v, want read-only", spec["enforcement"])
	}
	for key, want := range map[string]any{
		"database":  "odds-research",
		"tier":      "business",
		"appRef":    "fonbet-value",
		"namespace": "artemmendeleev-gmail-com-prod",
	} {
		if spec[key] != want {
			t.Errorf("spec.%s = %v, want %v (enforcement must not rewrite identity)", key, spec[key], want)
		}
	}
	if exts, _ := spec["extensions"].([]any); len(exts) != 1 || exts[0] != "pgvector" {
		t.Errorf("extensions lost by enforcement patch: %v", spec["extensions"])
	}
	if backup, _ := spec["backup"].(map[string]any); backup["retention"] != "14d" {
		t.Errorf("backup policy lost by enforcement patch: %v", spec["backup"])
	}
}

// A watcher re-deciding the same state every tick must not produce a commit per
// tick, so an unchanged state reports changed=false.
func TestPatchDatabaseEnforcementNoopWhenUnchanged(t *testing.T) {
	if _, changed, err := patchDatabaseEnforcement(enforcementFixture, "fonbet-db", "none"); err != nil || changed {
		t.Fatalf("absent enforcement must already count as none: changed=%v err=%v", changed, err)
	}
	applied, _, err := patchDatabaseEnforcement(enforcementFixture, "fonbet-db", "frozen")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if _, changed, err := patchDatabaseEnforcement(applied, "fonbet-db", "frozen"); err != nil || changed {
		t.Fatalf("repeat of same state must be a no-op: changed=%v err=%v", changed, err)
	}
}

// Releasing drops the field entirely rather than writing enforcement: none, so a
// released database is byte-identical to one that was never enforced.
func TestPatchDatabaseEnforcementReleaseDropsField(t *testing.T) {
	applied, _, err := patchDatabaseEnforcement(enforcementFixture, "fonbet-db", "read-only")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	released, changed, err := patchDatabaseEnforcement(applied, "fonbet-db", "none")
	if err != nil || !changed {
		t.Fatalf("release must change the manifest: changed=%v err=%v", changed, err)
	}
	if strings.Contains(released, "enforcement") {
		t.Errorf("released manifest still carries enforcement:\n%s", released)
	}
}

// An operation for one database must never patch a different database that
// happens to share the values file.
func TestPatchDatabaseEnforcementRejectsWrongName(t *testing.T) {
	if _, _, err := patchDatabaseEnforcement(enforcementFixture, "other-db", "read-only"); err == nil {
		t.Fatal("expected an error when the manifest is a different database")
	}
}
