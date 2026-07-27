package costengine_test

import (
	"math"
	"testing"

	"github.com/dada-tuda/console/backend/internal/billing/costengine"
)

func validBase() costengine.ClusterCost {
	return costengine.ClusterCost{
		Nodes: []costengine.NodeSpec{
			{Flavor: "8vcpu-16gb", Count: 3, MonthlyCostRub: 4000},
		},
		Capacity:    costengine.Capacity{VCPU: 24, RAMGB: 48, StorageGB: 300},
		CostWeights: costengine.CostWeights{CPU: 0.5, RAM: 0.3, Storage: 0.2},
	}
}

func TestComputeUnitCost_Happy(t *testing.T) {
	cfg := validBase()
	u, err := costengine.ComputeUnitCost(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	totalCost := 3 * 4000.0
	wantVCPU := totalCost * 0.5 / 24
	wantRAM := totalCost * 0.3 / 48
	wantStorage := totalCost * 0.2 / 300

	if u.PerVCPU != wantVCPU {
		t.Errorf("PerVCPU = %v, want %v", u.PerVCPU, wantVCPU)
	}
	if u.PerGBRAM != wantRAM {
		t.Errorf("PerGBRAM = %v, want %v", u.PerGBRAM, wantRAM)
	}
	if u.PerGBStorage != wantStorage {
		t.Errorf("PerGBStorage = %v, want %v", u.PerGBStorage, wantStorage)
	}
}

// TestFullCapacityRecovery asserts that consuming 100% of capacity across all
// dimensions recovers exactly the cluster monthly cost — no over- or under-count.
func TestFullCapacityRecovery(t *testing.T) {
	cfg := validBase()
	u, err := costengine.ComputeUnitCost(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var totalCost float64
	for _, n := range cfg.Nodes {
		totalCost += float64(n.Count) * n.MonthlyCostRub
	}

	fullUtilFP := costengine.Footprint{
		VCPU:      cfg.Capacity.VCPU,
		RAMGB:     cfg.Capacity.RAMGB,
		StorageGB: cfg.Capacity.StorageGB,
	}
	recovered := costengine.PlanCost(fullUtilFP, u)

	if math.Abs(recovered-totalCost) > 0.01 {
		t.Errorf("full-capacity utilization recovered %.4f RUB, want %.4f RUB (delta %.4f)",
			recovered, totalCost, recovered-totalCost)
	}
}

func TestComputeUnitCost_FailClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*costengine.ClusterCost)
	}{
		{
			name:   "zero vcpu capacity",
			mutate: func(c *costengine.ClusterCost) { c.Capacity.VCPU = 0 },
		},
		{
			name:   "zero ram capacity",
			mutate: func(c *costengine.ClusterCost) { c.Capacity.RAMGB = 0 },
		},
		{
			name:   "zero storage capacity",
			mutate: func(c *costengine.ClusterCost) { c.Capacity.StorageGB = 0 },
		},
		{
			name: "zero cluster cost",
			mutate: func(c *costengine.ClusterCost) {
				for i := range c.Nodes {
					c.Nodes[i].MonthlyCostRub = 0
				}
			},
		},
		{
			name:   "no nodes",
			mutate: func(c *costengine.ClusterCost) { c.Nodes = nil },
		},
		{
			name: "weights sum > 1",
			mutate: func(c *costengine.ClusterCost) {
				c.CostWeights = costengine.CostWeights{CPU: 0.6, RAM: 0.3, Storage: 0.2}
			},
		},
		{
			name: "weights sum < 1",
			mutate: func(c *costengine.ClusterCost) {
				c.CostWeights = costengine.CostWeights{CPU: 0.4, RAM: 0.3, Storage: 0.2}
			},
		},
		{
			name:   "zero weights",
			mutate: func(c *costengine.ClusterCost) { c.CostWeights = costengine.CostWeights{} },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBase()
			nodes := make([]costengine.NodeSpec, len(cfg.Nodes))
			copy(nodes, cfg.Nodes)
			cfg.Nodes = nodes
			tc.mutate(&cfg)
			_, err := costengine.ComputeUnitCost(cfg)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestPlanCost(t *testing.T) {
	u := costengine.UnitCost{
		PerVCPU:      250,
		PerGBRAM:     75,
		PerGBStorage: 8,
	}

	cases := []struct {
		name string
		fp   costengine.Footprint
		want float64
	}{
		{
			name: "free plan footprint",
			fp:   costengine.Footprint{VCPU: 0.1, RAMGB: 0.25, StorageGB: 1},
			want: 0.1*250 + 0.25*75 + 1*8,
		},
		{
			name: "startup plan footprint",
			fp:   costengine.Footprint{VCPU: 0.3, RAMGB: 0.6, StorageGB: 10},
			want: 0.3*250 + 0.6*75 + 10*8,
		},
		{
			name: "business plan footprint",
			fp:   costengine.Footprint{VCPU: 1.2, RAMGB: 3.0, StorageGB: 20},
			want: 1.2*250 + 3.0*75 + 20*8,
		},
		{
			name: "zero footprint",
			fp:   costengine.Footprint{},
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := costengine.PlanCost(tc.fp, u)
			if got != tc.want {
				t.Errorf("PlanCost = %v, want %v", got, tc.want)
			}
		})
	}
}
