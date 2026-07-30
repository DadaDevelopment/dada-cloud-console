package billing

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/dada-tuda/console/backend/internal/billing/costengine"
)

// TestLoadBoxFleetCostDerivesAUsableUnitCost proves the embedded box fleet config
// actually produces unit costs. ComputeUnitCost fails closed on zero capacity, zero
// cost or weights that do not sum to 1.0, so a typo in the YAML would otherwise
// surface at runtime as "box minutes are not being billed" — logged once at startup
// and then silent for a month.
func TestLoadBoxFleetCostDerivesAUsableUnitCost(t *testing.T) {
	cfg, err := LoadBoxFleetCost("")
	if err != nil {
		t.Fatalf("LoadBoxFleetCost: %v", err)
	}
	unit, err := costengine.ComputeUnitCost(cfg)
	if err != nil {
		t.Fatalf("ComputeUnitCost on the box fleet: %v", err)
	}
	if unit.PerVCPU <= 0 || unit.PerGBRAM <= 0 || unit.PerGBStorage <= 0 {
		t.Fatalf("unit cost has a non-positive dimension: %+v", unit)
	}
}

// TestBoxFleetCostIsSeparateFromTheClusterCost is the guard on decision D5's
// plumbing: the box pool must NOT be folded into cluster-cost.yaml.
//
// ComputeUnitCost divides the whole bill by the whole capacity, so merging the two
// fleets would add box rubles and box vCPU to the k8s pool and silently move the
// per-vCPU cost that every k8s app, database and plan price floor is derived from.
// The dilution would be invisible: every number would still look plausible. So the
// test asserts the two configs are genuinely different documents producing genuinely
// different unit costs.
func TestBoxFleetCostIsSeparateFromTheClusterCost(t *testing.T) {
	box, err := LoadBoxFleetCost("")
	if err != nil {
		t.Fatalf("LoadBoxFleetCost: %v", err)
	}
	cluster, err := LoadClusterCost("")
	if err != nil {
		t.Fatalf("LoadClusterCost: %v", err)
	}
	if box.Capacity == cluster.Capacity {
		t.Error("box pool and k8s cluster report identical capacity; one of the two files is not the pool it claims to be")
	}
	// After ADR-019 the pool is a reserved SHARE of the cluster, so it must be
	// strictly smaller. A pool larger than the cluster it is carved out of would be
	// capacity nobody has — the "infrastructure that does not exist" error ADR-019
	// was written to correct.
	if box.Capacity.RAMGB >= cluster.Capacity.RAMGB {
		t.Errorf("box pool reserves %.1f GB of RAM out of a %.1f GB cluster; the pool is a share of the "+
			"cluster (ADR-019), not a fleet of its own", box.Capacity.RAMGB, cluster.Capacity.RAMGB)
	}
	boxUnit, err := costengine.ComputeUnitCost(box)
	if err != nil {
		t.Fatalf("box unit cost: %v", err)
	}
	clusterUnit, err := costengine.ComputeUnitCost(cluster)
	if err != nil {
		t.Fatalf("cluster unit cost: %v", err)
	}
	if boxUnit == clusterUnit {
		t.Error("box and cluster unit costs are identical; the two fleets have been merged, " +
			"which silently re-prices every k8s app and plan floor")
	}
}

// TestBoxFleetCostWeightsAreRAMHeavy pins the one non-obvious modelling choice in
// box-fleet-cost.yaml against being "corrected" to match cluster-cost.yaml.
//
// A box pod's memory is a hard reservation (a runaway agent build must not OOM its
// neighbour), so memory is what actually decides how many boxes fit; its CPU limit
// is a ceiling handed out several times over. Weighting CPU most heavily — as the
// cluster file does for apps — would charge most of the bill against the dimension
// that is not scarce, and the resulting per-minute price would be wrong in a
// direction no dashboard would show.
func TestBoxFleetCostWeightsAreRAMHeavy(t *testing.T) {
	cfg, err := LoadBoxFleetCost("")
	if err != nil {
		t.Fatalf("LoadBoxFleetCost: %v", err)
	}
	w := cfg.CostWeights
	if math.Abs(w.CPU+w.RAM+w.Storage-1.0) > 0.001 {
		t.Fatalf("weights sum to %.4f, want 1.0", w.CPU+w.RAM+w.Storage)
	}
	if w.RAM <= w.CPU {
		t.Errorf("cost_weights.ram (%.2f) must exceed cost_weights.cpu (%.2f): on a box host "+
			"memory is the binding constraint and CPU is oversubscribed", w.RAM, w.CPU)
	}
}

// TestEmbeddedBoxFleetCostMatchesTheEditableSource keeps data/ (compiled into the
// binary) in step with config/ (the file an operator edits). The same trap 060's
// grants comment describes applies here: a divergence would mean an operator changes
// a price and nothing happens, with no error to notice.
func TestEmbeddedBoxFleetCostMatchesTheEditableSource(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "billing", "box-fleet-cost.yaml"))
	if err != nil {
		t.Skipf("editable source not readable from this checkout: %v", err)
	}
	embedded, err := os.ReadFile(filepath.Join("data", "box-fleet-cost.yaml"))
	if err != nil {
		t.Fatalf("read embedded copy: %v", err)
	}
	if string(source) != string(embedded) {
		t.Error("config/billing/box-fleet-cost.yaml and internal/billing/data/box-fleet-cost.yaml differ; " +
			"an operator editing the first would change nothing, because the second is what is compiled in")
	}
}
