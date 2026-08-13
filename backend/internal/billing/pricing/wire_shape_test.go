package pricing

import (
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/backend/internal/billing/costengine"
)

// TestPlanJSONUsesConsoleFieldNames pins the wire shape of GET /billing/plans
// and of the "quotas" block in GET /projects/{id}/billing/account.
//
// Both endpoints serialize these structs directly, so a missing json tag makes
// Go emit its own field names and every console reader of price_rub or
// quotas.apps silently reads undefined -- no error, no 500, just an upsell that
// can never name a plan. A compile-time check cannot see that; this test can.
func TestPlanJSONUsesConsoleFieldNames(t *testing.T) {
	p := Plan{
		Key:      "startup",
		Name:     "Startup",
		PriceRUB: 990,
		Quotas: Quotas{
			Apps:                5,
			Databases:           2,
			StorageGB:           10,
			Domains:             5,
			Environments:        2,
			TeamMembers:         3,
			BackupRetentionDays: 7,
			BoxMinutes:          300,
			AppServers:          1,
		},
		Capabilities:      []string{"custom_domains"},
		SupportLevel:      "email",
		InternalFootprint: costengine.Footprint{VCPU: 1, RAMGB: 2, StorageGB: 10},
	}

	var got map[string]any
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}

	for _, field := range []string{"key", "name", "price_rub", "quotas", "capabilities", "support_level"} {
		if _, ok := got[field]; !ok {
			t.Errorf("plan JSON is missing %q; got %s", field, raw)
		}
	}
	if _, leaked := got["internal_footprint"]; leaked {
		t.Errorf("plan JSON leaks the internal footprint: %s", raw)
	}
	if _, leaked := got["InternalFootprint"]; leaked {
		t.Errorf("plan JSON leaks the internal footprint: %s", raw)
	}

	quotas, ok := got["quotas"].(map[string]any)
	if !ok {
		t.Fatalf("quotas is not an object: %s", raw)
	}
	want := map[string]float64{
		"apps":                  5,
		"databases":             2,
		"storage_gb":            10,
		"domains":               5,
		"environments":          2,
		"team_members":          3,
		"backup_retention_days": 7,
		"box_minutes":           300,
		"app_servers":           1,
	}
	for field, value := range want {
		v, ok := quotas[field]
		if !ok {
			t.Errorf("quotas JSON is missing %q; got %v", field, quotas)
			continue
		}
		if v != value {
			t.Errorf("quotas[%q] = %v, want %v", field, v, value)
		}
	}
	if len(quotas) != len(want) {
		t.Errorf("quotas JSON has %d fields, want %d: %v", len(quotas), len(want), quotas)
	}
}

// TestQuotaResourceNamesMatchJSONFields keeps the two vocabularies married: the
// resource names the quota gate refuses growth on (and puts in the 403 payload)
// must be the same strings the console finds inside plan.quotas, or the upsell
// cannot look up the limit it was just refused on.
func TestQuotaResourceNamesMatchJSONFields(t *testing.T) {
	raw, err := json.Marshal(Quotas{})
	if err != nil {
		t.Fatalf("marshal quotas: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal quotas: %v", err)
	}

	for _, resource := range []string{"apps", "databases", "domains", "team_members", "storage_gb", "box_minutes", "app_servers"} {
		if _, ok := Quota(Plan{}, resource); !ok {
			t.Fatalf("Quota does not know resource %q", resource)
		}
		if _, ok := fields[resource]; !ok {
			t.Errorf("resource %q has no matching field in quotas JSON: %v", resource, fields)
		}
	}
}
