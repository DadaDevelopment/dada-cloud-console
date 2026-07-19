package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDeleteImpactItemsNeverNull locks the contract that a delete-impact preview
// always encodes items as a JSON array, never null. A nil Items slice (the
// default when a scan finds nothing) marshals to `"items":null`, which the
// console delete modal iterates during render and crashes on
// ("null is not an object"). withNonNilItems must normalize it to `[]`.
//
// The first assertion proves the raw zero value really does encode null, so the
// test fails loudly if the underlying trap ever changes shape.
func TestDeleteImpactItemsNeverNull(t *testing.T) {
	raw, err := json.Marshal(DeleteImpact{App: "web", Namespace: "ns"})
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	if !strings.Contains(string(raw), `"items":null`) {
		t.Fatalf("expected raw nil-Items to encode null (trap changed): %s", raw)
	}

	got, err := json.Marshal(DeleteImpact{App: "web", Namespace: "ns"}.withNonNilItems())
	if err != nil {
		t.Fatalf("marshal normalized: %v", err)
	}
	if strings.Contains(string(got), `"items":null`) {
		t.Fatalf("items still encodes null after withNonNilItems: %s", got)
	}
	if !strings.Contains(string(got), `"items":[]`) {
		t.Fatalf("items not normalized to []: %s", got)
	}
}

// TestDeleteImpactWithNonNilItemsPreservesData ensures normalization only fills
// an absent slice and never drops real impact rows.
func TestDeleteImpactWithNonNilItemsPreservesData(t *testing.T) {
	in := DeleteImpact{
		App:       "web",
		Namespace: "ns",
		Items:     []ImpactItem{{Kind: "ServiceDatabaseV2", Name: "db-1", Group: impactGroupDatabase, Source: impactSourceConsole}},
	}
	out := in.withNonNilItems()
	if len(out.Items) != 1 || out.Items[0].Name != "db-1" {
		t.Fatalf("normalization mutated real items: %+v", out.Items)
	}
}
