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
