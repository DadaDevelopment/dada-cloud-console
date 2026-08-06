package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/billing"
)

// chartTiers mirrors serviceDatabase.tiers in the crossplane-platform-api chart
// (argo-infra, clusters/beget-prod/.../crossplane-platform-api/chart/values.yaml).
// A tier the chart does not declare is rejected by the XRD enum, which would
// wedge the XR — so a rename on either side has to fail here first.
var chartTiers = map[string]bool{
	"unlimited": true,
	"internal":  true,
	"free":      true,
	"starter":   true,
	"business":  true,
}

func TestDatabaseTierByPlan_CoversEveryPlan(t *testing.T) {
	plans, err := billing.LoadPlans("")
	if err != nil {
		t.Fatalf("load plans: %v", err)
	}
	if len(plans) == 0 {
		t.Fatal("no plans loaded")
	}
	for _, p := range plans {
		tier, ok := databaseTierByPlan[p.Key]
		if !ok {
			t.Errorf("plan %q has no database quota tier: its databases would fall back to unlimited", p.Key)
			continue
		}
		if !chartTiers[tier] {
			t.Errorf("plan %q maps to tier %q, which the composition does not declare", p.Key, tier)
		}
	}
}

func TestDatabaseTierByPlan_NoUnknownPlans(t *testing.T) {
	plans, err := billing.LoadPlans("")
	if err != nil {
		t.Fatalf("load plans: %v", err)
	}
	known := map[string]bool{}
	for _, p := range plans {
		known[p.Key] = true
	}
	for key := range databaseTierByPlan {
		if !known[key] {
			t.Errorf("databaseTierByPlan has stale plan key %q", key)
		}
	}
}
