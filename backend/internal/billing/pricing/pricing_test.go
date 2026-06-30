package pricing_test

import (
	"fmt"
	"testing"

	"github.com/dada-tuda/console/backend/internal/billing"
	"github.com/dada-tuda/console/backend/internal/billing/costengine"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
)

func testUnitCost() costengine.UnitCost {
	cfg := costengine.ClusterCost{
		Nodes: []costengine.NodeSpec{
			{Flavor: "8vcpu-16gb", Count: 3, MonthlyCostRub: 4000},
		},
		Capacity:    costengine.Capacity{VCPU: 24, RAMGB: 48, StorageGB: 300},
		CostWeights: costengine.CostWeights{CPU: 0.5, RAM: 0.3, Storage: 0.2},
	}
	u, err := costengine.ComputeUnitCost(cfg)
	if err != nil {
		panic(err)
	}
	return u
}

// realUnitCost loads the embedded production cluster-cost config so the margin
// guard validates published plan prices against the REAL cluster cost, not a fixture.
func realUnitCost(t *testing.T) costengine.UnitCost {
	t.Helper()
	cfg, err := billing.LoadClusterCost("")
	if err != nil {
		t.Fatalf("LoadClusterCost: %v", err)
	}
	u, err := costengine.ComputeUnitCost(cfg)
	if err != nil {
		t.Fatalf("ComputeUnitCost: %v", err)
	}
	return u
}

func testPlans() []pricing.Plan {
	return []pricing.Plan{
		{
			Key:               "free",
			Name:              "Free",
			PriceRUB:          0,
			Quotas:            pricing.Quotas{Apps: 1, Databases: 1, StorageGB: 1, Domains: 1, Environments: 1, TeamMembers: 1},
			InternalFootprint: costengine.Footprint{VCPU: 0.1, RAMGB: 0.25, StorageGB: 1},
		},
		{
			Key:               "startup",
			Name:              "Startup",
			PriceRUB:          990,
			Quotas:            pricing.Quotas{Apps: 5, Databases: 2, StorageGB: 10, Domains: 5, Environments: 2, TeamMembers: 3},
			InternalFootprint: costengine.Footprint{VCPU: 0.3, RAMGB: 0.6, StorageGB: 10},
		},
		{
			Key:               "business",
			Name:              "Business",
			PriceRUB:          2900,
			Quotas:            pricing.Quotas{Apps: 20, Databases: 10, StorageGB: 100, Domains: 20, Environments: 5, TeamMembers: 10},
			InternalFootprint: costengine.Footprint{VCPU: 1.2, RAMGB: 3.0, StorageGB: 20},
		},
		{
			Key:               "enterprise",
			Name:              "Enterprise",
			PriceRUB:          0,
			Quotas:            pricing.Quotas{},
			InternalFootprint: costengine.Footprint{},
		},
	}
}

func TestPriceFloor(t *testing.T) {
	u := testUnitCost()
	plans := testPlans()

	planMap := make(map[string]pricing.Plan)
	for _, p := range plans {
		planMap[p.Key] = p
	}

	cases := []struct {
		planKey string
		wantGT  float64
	}{
		{"free", -1},
		{"startup", 0},
		{"business", 0},
	}

	for _, tc := range cases {
		t.Run(tc.planKey, func(t *testing.T) {
			p := planMap[tc.planKey]
			floor := pricing.PriceFloor(p, u)
			if floor < tc.wantGT {
				t.Errorf("PriceFloor(%s) = %v, want > %v", tc.planKey, floor, tc.wantGT)
			}
		})
	}
}

func TestRecommendPlan(t *testing.T) {
	plans := testPlans()

	cases := []struct {
		name     string
		need     pricing.Need
		wantPlan string
	}{
		{
			name:     "exactly fits free",
			need:     pricing.Need{Apps: 1, Databases: 1, Domains: 1, Members: 1, StorageGB: 1},
			wantPlan: "free",
		},
		{
			name:     "just over free apps → startup",
			need:     pricing.Need{Apps: 2, Databases: 1, Domains: 1, Members: 1, StorageGB: 1},
			wantPlan: "startup",
		},
		{
			name:     "just fits startup",
			need:     pricing.Need{Apps: 5, Databases: 2, Domains: 5, Members: 3, StorageGB: 10},
			wantPlan: "startup",
		},
		{
			name:     "just over startup apps → business",
			need:     pricing.Need{Apps: 6, Databases: 2, Domains: 5, Members: 3, StorageGB: 10},
			wantPlan: "business",
		},
		{
			name:     "over business → enterprise",
			need:     pricing.Need{Apps: 21, Databases: 10, Domains: 20, Members: 10, StorageGB: 100},
			wantPlan: "enterprise",
		},
		{
			name:     "members exceed business → enterprise",
			need:     pricing.Need{Apps: 1, Databases: 1, Domains: 1, Members: 11, StorageGB: 1},
			wantPlan: "enterprise",
		},
		{
			name:     "storage exceeds startup → business",
			need:     pricing.Need{Apps: 1, Databases: 1, Domains: 1, Members: 1, StorageGB: 11},
			wantPlan: "business",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := pricing.RecommendPlan(tc.need, plans)
			if got.Key != tc.wantPlan {
				t.Errorf("RecommendPlan = %q, want %q (reason: %s)", got.Key, tc.wantPlan, reason)
			}
		})
	}
}

func TestMarginGuard(t *testing.T) {
	plans, err := billing.LoadPlans("")
	if err != nil {
		t.Fatalf("LoadPlans: %v", err)
	}
	u := realUnitCost(t)

	t.Logf("UnitCost: PerVCPU=%.4f  PerGBRAM=%.4f  PerGBStorage=%.4f", u.PerVCPU, u.PerGBRAM, u.PerGBStorage)

	for _, p := range plans {
		if p.Key == "enterprise" || p.Key == "free" {
			continue
		}
		floor := pricing.PriceFloor(p, u)
		margin := p.PriceRUB - floor
		t.Logf("plan=%-10s  price=%.2f  floor=%.2f  margin=%.2f  margin%%=%.1f%%",
			p.Key, p.PriceRUB, floor, margin, margin/floor*100)
		if p.PriceRUB < floor {
			t.Errorf("MARGIN GUARD FAIL: plan %q published price %.2f RUB < floor %.2f RUB (deficit %.2f RUB)",
				p.Key, p.PriceRUB, floor, floor-p.PriceRUB)
		}
	}
}

func TestMarginTable(t *testing.T) {
	plans, err := billing.LoadPlans("")
	if err != nil {
		t.Fatalf("LoadPlans: %v", err)
	}
	u := realUnitCost(t)

	fmt.Printf("\n=== Margin Table (real prod cluster cost, weights cpu=0.5 ram=0.3 storage=0.2) ===\n")
	fmt.Printf("UnitCost: PerVCPU=%.4f  PerGBRAM=%.4f  PerGBStorage=%.4f\n\n", u.PerVCPU, u.PerGBRAM, u.PerGBStorage)
	fmt.Printf("%-12s  %10s  %10s  %10s  %10s\n", "Plan", "Price(RUB)", "Floor(RUB)", "Margin(RUB)", "Margin%")
	for _, p := range plans {
		if p.Key == "enterprise" {
			fmt.Printf("%-12s  %10s  %10s  %10s  %10s\n", p.Key, "custom", "N/A", "N/A", "N/A")
			continue
		}
		floor := pricing.PriceFloor(p, u)
		margin := p.PriceRUB - floor
		pct := ""
		if floor > 0 {
			pct = fmt.Sprintf("%.1f%%", margin/floor*100)
		} else {
			pct = "N/A"
		}
		fmt.Printf("%-12s  %10.2f  %10.2f  %10.2f  %10s\n", p.Key, p.PriceRUB, floor, margin, pct)
	}
	fmt.Println()
}

func TestQuota(t *testing.T) {
	plans := testPlans()
	planMap := make(map[string]pricing.Plan)
	for _, p := range plans {
		planMap[p.Key] = p
	}

	cases := []struct {
		planKey  string
		resource string
		wantVal  int
		wantOK   bool
	}{
		{"free", "apps", 1, true},
		{"startup", "databases", 2, true},
		{"business", "domains", 20, true},
		{"business", "team_members", 10, true},
		{"enterprise", "apps", 0, true},
		{"free", "unknown_resource", 0, false},
		{"startup", "storage_gb", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.planKey+"/"+tc.resource, func(t *testing.T) {
			p := planMap[tc.planKey]
			val, ok := pricing.Quota(p, tc.resource)
			if ok != tc.wantOK {
				t.Errorf("Quota(%s, %s) ok=%v, want %v", tc.planKey, tc.resource, ok, tc.wantOK)
			}
			if ok && val != tc.wantVal {
				t.Errorf("Quota(%s, %s) = %d, want %d", tc.planKey, tc.resource, val, tc.wantVal)
			}
		})
	}
}
