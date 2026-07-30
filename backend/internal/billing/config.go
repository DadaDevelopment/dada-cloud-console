package billing

import (
	_ "embed"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/dada-tuda/console/backend/internal/billing/costengine"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
)

//go:embed data/cluster-cost.yaml
var embeddedClusterCost []byte

//go:embed data/plans.yaml
var embeddedPlans []byte

//go:embed data/box-fleet-cost.yaml
var embeddedBoxFleetCost []byte

// LoadClusterCost reads cluster cost config from path.
// If path is empty, the embedded data/cluster-cost.yaml (compiled from
// config/billing/cluster-cost.yaml at build time) is used.
// config/billing/cluster-cost.yaml is the editable source of truth;
// data/ contains the build-time copy embedded into the binary.
func LoadClusterCost(path string) (costengine.ClusterCost, error) {
	raw, err := readFileOrEmbedded(path, embeddedClusterCost)
	if err != nil {
		return costengine.ClusterCost{}, fmt.Errorf("billing: load cluster-cost: %w", err)
	}
	var cfg costengine.ClusterCost
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return costengine.ClusterCost{}, fmt.Errorf("billing: parse cluster-cost: %w", err)
	}
	return cfg, nil
}

// LoadBoxFleetCost reads the Dada Box capacity-pool cost config from path.
// If path is empty, the embedded data/box-fleet-cost.yaml is used.
// config/billing/box-fleet-cost.yaml is the editable source of truth;
// data/ contains the build-time copy embedded into the binary.
//
// A SECOND LOADER RATHER THAN A SECOND SECTION of cluster-cost.yaml, and the
// reason is not symmetry: costengine.ComputeUnitCost divides the whole bill by
// the whole capacity, so merging the box pool into the cluster config would add
// its rubles and its vCPU to the k8s totals and silently shift the per-vCPU cost
// that every k8s app, database and plan price floor is derived from. The dilution
// would be invisible — every number would still look plausible.
//
// Note what the file describes after ADR-019: a reserved SHARE of the existing
// cluster, not a dedicated fleet. There is no gVisor VM fleet and none is planned,
// so the pool's numbers are derived from cluster-cost.yaml rather than invented.
// The separation still earns itself because boxes and apps are sold against the
// same hardware under different density rules (hard memory reservation, heavily
// oversubscribed CPU), which is why the two files carry different weights.
//
// The return type is the same costengine.ClusterCost because the shape of "nodes
// + capacity + weights" is identical; only the pool differs.
func LoadBoxFleetCost(path string) (costengine.ClusterCost, error) {
	raw, err := readFileOrEmbedded(path, embeddedBoxFleetCost)
	if err != nil {
		return costengine.ClusterCost{}, fmt.Errorf("billing: load box-fleet-cost: %w", err)
	}
	var cfg costengine.ClusterCost
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return costengine.ClusterCost{}, fmt.Errorf("billing: parse box-fleet-cost: %w", err)
	}
	return cfg, nil
}

// LoadPlans reads plan definitions from path.
// If path is empty, the embedded data/plans.yaml is used.
// config/billing/plans.yaml is the editable source of truth;
// data/ contains the build-time copy embedded into the binary.
func LoadPlans(path string) ([]pricing.Plan, error) {
	raw, err := readFileOrEmbedded(path, embeddedPlans)
	if err != nil {
		return nil, fmt.Errorf("billing: load plans: %w", err)
	}

	var envelope struct {
		Plans []pricing.Plan `yaml:"plans"`
	}
	if err := yaml.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("billing: parse plans: %w", err)
	}
	return envelope.Plans, nil
}

func readFileOrEmbedded(path string, fallback []byte) ([]byte, error) {
	if path == "" {
		return fallback, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return b, nil
}
