package worker

import (
	"testing"

	"gopkg.in/yaml.v3"
)

const shardFixture = `apiVersion: platform.dada-tuda.ru/v1alpha1
kind: ServiceDatabaseV2
metadata:
  name: fonbet-db
spec:
  appRef: fonbet-value
  namespace: artemmendeleev-gmail-com-prod
  engine: postgresql
  database: odds-research
  tier: business
  backup:
    enabled: true
    frequency: daily
    retention: 14d
`

func TestPatchDatabaseShardKeepsIdentity(t *testing.T) {
	got, changed, err := patchDatabaseShard(shardFixture, "fonbet-db", "shard-0")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !changed {
		t.Fatal("a CR still naming the shard the data left has to change")
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("patched manifest is not YAML: %v", err)
	}
	spec, _ := doc["spec"].(map[string]any)
	if spec["shard"] != "shard-0" {
		t.Fatalf("shard = %v, want shard-0", spec["shard"])
	}
	if spec["database"] != "odds-research" || spec["tier"] != "business" {
		t.Fatalf("the patch may only touch placement, got %v", spec)
	}
	if _, ok := spec["backup"].(map[string]any); !ok {
		t.Fatalf("the backup policy has to survive the patch, got %v", spec["backup"])
	}
}

func TestPatchDatabaseShardIsIdempotent(t *testing.T) {
	once, _, err := patchDatabaseShard(shardFixture, "fonbet-db", "shard-0")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if _, changed, err := patchDatabaseShard(once, "fonbet-db", "shard-0"); err != nil || changed {
		t.Fatalf("a move re-recorded must produce no commit, changed=%v err=%v", changed, err)
	}
}

func TestPatchDatabaseShardRefusesAnotherDatabase(t *testing.T) {
	if _, _, err := patchDatabaseShard(shardFixture, "other-db", "shard-0"); err == nil {
		t.Fatal("a patch for one database must not rewrite the one sharing its values file")
	}
}
