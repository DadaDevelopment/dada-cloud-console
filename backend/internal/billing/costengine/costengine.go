package costengine

import (
	"fmt"
	"math"
)

// NodeSpec describes a single cluster node flavor and its count.
type NodeSpec struct {
	Flavor         string  `yaml:"flavor"`
	Count          int     `yaml:"count"`
	MonthlyCostRub float64 `yaml:"monthly_cost_rub"`
}

// Capacity is the total addressable capacity of the cluster in internal units.
type Capacity struct {
	VCPU      float64 `yaml:"vcpu"`
	RAMGB     float64 `yaml:"ram_gb"`
	StorageGB float64 `yaml:"storage_gb"`
}

// CostWeights splits the cluster bill across resource dimensions.
// All three weights must sum to 1.0 (±0.001).
// This ensures full-capacity utilization recovers exactly 100% of the cluster cost.
type CostWeights struct {
	CPU     float64 `yaml:"cpu"`
	RAM     float64 `yaml:"ram"`
	Storage float64 `yaml:"storage"`
}

// ClusterCost is the top-level cluster cost configuration.
type ClusterCost struct {
	Nodes          []NodeSpec  `yaml:"nodes"`
	Capacity       Capacity    `yaml:"capacity"`
	CostWeights    CostWeights `yaml:"cost_weights"`
	EgressRubPerGB float64     `yaml:"egress_rub_per_gb"`
}

// UnitCost holds the derived per-unit monthly cost in RUB.
// Derived as cluster_cost * weight / capacity so full utilization recovers exactly cluster_cost.
type UnitCost struct {
	PerVCPU      float64
	PerGBRAM     float64
	PerGBStorage float64
}

// Footprint is the expected average actual resource consumption for a plan.
// This is NOT the customer quota ceiling — it is the PaaS oversubscription estimate
// used only for internal margin/cost modeling.
type Footprint struct {
	VCPU      float64 `yaml:"vcpu"`
	RAMGB     float64 `yaml:"ram_gb"`
	StorageGB float64 `yaml:"storage_gb"`
}

// ComputeUnitCost derives per-unit monthly costs from the cluster configuration.
// Fails closed: zero/missing capacity, zero total cost, or weights not summing to 1.0 all return errors.
func ComputeUnitCost(cfg ClusterCost) (UnitCost, error) {
	if cfg.Capacity.VCPU <= 0 {
		return UnitCost{}, fmt.Errorf("billing: capacity.vcpu must be > 0, got %v", cfg.Capacity.VCPU)
	}
	if cfg.Capacity.RAMGB <= 0 {
		return UnitCost{}, fmt.Errorf("billing: capacity.ram_gb must be > 0, got %v", cfg.Capacity.RAMGB)
	}
	if cfg.Capacity.StorageGB <= 0 {
		return UnitCost{}, fmt.Errorf("billing: capacity.storage_gb must be > 0, got %v", cfg.Capacity.StorageGB)
	}

	var totalCost float64
	for _, n := range cfg.Nodes {
		totalCost += float64(n.Count) * n.MonthlyCostRub
	}
	if totalCost <= 0 {
		return UnitCost{}, fmt.Errorf("billing: total cluster cost must be > 0, got %v", totalCost)
	}

	w := cfg.CostWeights
	weightSum := w.CPU + w.RAM + w.Storage
	if math.Abs(weightSum-1.0) > 0.001 {
		return UnitCost{}, fmt.Errorf("billing: cost_weights must sum to 1.0 (±0.001), got %.4f", weightSum)
	}

	return UnitCost{
		PerVCPU:      totalCost * w.CPU / cfg.Capacity.VCPU,
		PerGBRAM:     totalCost * w.RAM / cfg.Capacity.RAMGB,
		PerGBStorage: totalCost * w.Storage / cfg.Capacity.StorageGB,
	}, nil
}

// PlanCost computes the monthly internal cost in RUB for a given footprint and unit cost.
func PlanCost(fp Footprint, u UnitCost) float64 {
	return fp.VCPU*u.PerVCPU + fp.RAMGB*u.PerGBRAM + fp.StorageGB*u.PerGBStorage
}
