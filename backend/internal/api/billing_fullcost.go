package api

import (
	"context"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/opencost"
	"github.com/rs/zerolog/log"
)

// Consumption pricing applies the one platform-wide cost-plus multiplier to the
// allocated resource cost. Shared and idle infrastructure remains platform cost;
// it is not reintroduced as an invisible resource-specific uplift.
//
// billingCostWindow is the OpenCost window used for consumption pricing. A short
// duration form is used deliberately: a calendar-month RFC3339 range
// intermittently 500s ("AllocationSetRange has empty AssetSet") over data-less
// days, and a wide aggregate=pod window fetches a multi-megabyte per-pod set
// from CPU-throttled Mimir (measured 38-85s+, intermittently TRUNCATING the
// response so the client decodes "unexpected end of JSON input" and the snapshot
// never builds). "24h" returns in ~7s / ~3MB. It captures the current run rate
// and is cached + scaled to a month, so the estimate fills in as data
// accumulates. Do NOT widen this back to 7d/30d.
const billingCostWindow = "24h"

// billingWindowDays / billingMonthDays project the window cost to a 30-day month
// run-rate so consumption reads as a monthly figure ("/мес") consistent with the
// spec-based estimate, rather than a raw sample-window sum.
const billingWindowDays = 1
const billingMonthDays = 30

// billingMonthlyScale scales a billingCostWindow-worth of cost up to a month.
var billingMonthlyScale = float64(billingMonthDays) / float64(billingWindowDays)

// estimateFootprintDB is the reserved baseline footprint used to ESTIMATE a
// managed database's monthly cost before OpenCost has usage data for it (a
// freshly created DB otherwise reads a confusing 0). A managed DB is a real
// provisioned resource (a schema in the shared Postgres pod reserving storage +
// compute), so a baseline estimate is fair. Apps are NOT estimated: an app with
// no data is usually a stopped/idle workload that genuinely costs ~0, and there
// are many such snapshots, so estimating them would badly inflate the total. The
// per-resource reserved spec is not stored, so this is a typical-footprint proxy
// priced at the same unit costs + overhead + margin as real usage. Tunable.
var estimateFootprintDB = billingFootprint{vcpu: 0.05, ramGB: 0.10, storageGB: 1.0}

// billingFootprint is a reserved resource footprint in internal units.
type billingFootprint struct {
	vcpu, ramGB, storageGB float64
}

// billingSnapshotTTL bounds how long a cluster cost snapshot is reused before a
// refresh. All OpenCost data feeding consumption is cluster-global (same for
// every project), so it is fetched once per TTL, not per request/project -- this
// is what keeps the billing endpoints from issuing an OpenCost query per project
// and timing out.
const billingSnapshotTTL = 4 * time.Minute

// consumptionPricing turns an allocated OpenCost amount into its customer-facing
// cost-plus price.
type consumptionPricing struct {
	markup float64
}

// price applies the common markup once to a resource's raw allocated cost,
// rounded to two decimals. Negative inputs are clamped to zero.
func (p consumptionPricing) price(cpuCost, ramCost, pvCost float64) float64 {
	return round2((nonNeg(cpuCost) + nonNeg(ramCost) + nonNeg(pvCost)) * p.markup)
}

// nonNeg clamps a cost to zero. OpenCost can report small negative costs from
// cost adjustments over incomplete windows, which must not become negative bills.
func nonNeg(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// billingCostSnapshot is a cluster-wide, cached view of OpenCost costs from which
// every project's consumption is computed with no further OpenCost calls: the
// per-type pricing factors, raw per-app cost keyed "namespace/appName" (the
// dada.io/app label), and the shared Postgres / PowerDNS pod costs.
type billingCostSnapshot struct {
	pricing  consumptionPricing
	appCost  map[string]opencost.Allocation
	postgres opencost.Allocation
	powerdns opencost.Allocation
	builtAt  time.Time
}

// emptySnapshot is the safe fallback when OpenCost is unavailable: unit factors,
// no per-resource costs, so consumption degrades to the metrics/storage fallback
// rather than erroring.
func (h *Handler) emptySnapshot() *billingCostSnapshot {
	return &billingCostSnapshot{
		pricing: consumptionPricing{markup: h.billingMargin},
		appCost: map[string]opencost.Allocation{},
	}
}

// billingSnapshot returns the current cluster cost snapshot straight from
// memory, falling back to an empty snapshot when the warmer has not
// populated one yet (cold boot). It NEVER rebuilds live: every caller of this
// method is on a request path (GetAccountSummary's per-project loop,
// buildAdminCostSummary's revenue walk) or the warmer's own per-project sub-loop
// (warmProjectConsumptions), and a stale/missing snapshot triggering its own
// synchronous OpenCost call here was the actual root cause of a single
// project appearing to "hang": whichever project happened to be first to see
// a stale snapshot paid the full cost of a live OpenCost rebuild attempt
// (bounded only by whatever context deadline its caller passed), burning that
// caller's entire time budget whenever OpenCost was slow or degraded. The
// live rebuild now happens exclusively in warmBillingSnapshot, on the
// warmer's own schedule with its own patient client and bounded timeout.
func (h *Handler) billingSnapshot() *billingCostSnapshot {
	h.billingSnapMu.Lock()
	defer h.billingSnapMu.Unlock()

	if h.billingSnap != nil {
		return h.billingSnap
	}
	return h.emptySnapshot()
}

// warmBillingSnapshot rebuilds the billing snapshot with the given (patient)
// OpenCost client and stores it, called from the background cost-cache
// warmer (cost.go) so a request almost never pays the ~7s cold
// buildBillingSnapshot query itself. Best-effort: a failure is logged and the
// last-good snapshot (if any) is left in place.
func (h *Handler) warmBillingSnapshot(ctx context.Context, client *opencost.Client) {
	snap, err := h.buildBillingSnapshot(ctx, client)
	if err != nil {
		log.Warn().Err(err).Msg("cost warmer: billing snapshot build failed")
		return
	}
	h.billingSnapMu.Lock()
	h.billingSnap = snap
	h.billingSnapMu.Unlock()
}

// appLabelKeys are the pod labels, in priority order, an app's console name is
// matched against. Console-deployed user apps carry dada.io/app; platform/infra
// apps (installed via GitOps) instead carry the standard app.kubernetes.io/*
// labels, so keying on dada.io/app alone left every infra app at 0.
var appLabelKeys = []string{"dada_io_app", "app_kubernetes_io_instance", "app_kubernetes_io_name"}

// buildBillingSnapshot issues ONE cluster-wide OpenCost pod query and, from it,
// derives everything: per-app cost indexed by every candidate app label, and
// the shared Postgres/PowerDNS pod costs. Costs are scaled to a monthly run-rate.
func (h *Handler) buildBillingSnapshot(ctx context.Context, client *opencost.Client) (*billingCostSnapshot, error) {
	pods, err := client.Compute(ctx, billingCostWindow, "pod", "")
	if err != nil {
		return nil, err
	}

	appCost := make(map[string]opencost.Allocation)
	snap := &billingCostSnapshot{appCost: appCost, builtAt: time.Now()}

	for _, a := range pods {
		ns := a.Properties.Namespace
		if ns == "" || strings.HasPrefix(ns, "__") {
			continue
		}

		scaled := scaleAlloc(a, billingMonthlyScale)
		switch {
		case strings.HasPrefix(a.Properties.Pod, "postgresql"):
			snap.postgres = addAlloc(snap.postgres, scaled)
		case strings.HasPrefix(a.Properties.Pod, "powerdns"):
			snap.powerdns = addAlloc(snap.powerdns, scaled)
		}

		seen := map[string]bool{}
		for _, lk := range appLabelKeys {
			if v := a.Properties.Labels[lk]; v != "" {
				key := ns + "/" + v
				if !seen[key] {
					appCost[key] = addAlloc(appCost[key], scaled)
					seen[key] = true
				}
			}
		}
	}

	snap.pricing = consumptionPricing{markup: h.billingMargin}
	return snap, nil
}

// addAlloc sums the cost fields of two allocations.
func addAlloc(a, b opencost.Allocation) opencost.Allocation {
	a.CPUCost += b.CPUCost
	a.RAMCost += b.RAMCost
	a.PVCost += b.PVCost
	a.TotalCost += b.TotalCost
	return a
}

// scaleAlloc returns a copy of an allocation with every cost field multiplied by
// s (used to project a window cost to a monthly run-rate).
func scaleAlloc(a opencost.Allocation, s float64) opencost.Allocation {
	a.CPUCost *= s
	a.RAMCost *= s
	a.PVCost *= s
	a.TotalCost *= s
	return a
}

// estimateCost prices a reserved baseline footprint at the elegant per-unit
// cluster costs (billingUnit, RUB per unit-month) then applies the same per-type
// overhead + margin as real usage. It is the "ориентировочно" figure shown for a
// resource OpenCost has no data for yet. Zero when the unit costs are unset.
func (h *Handler) estimateCost(fp billingFootprint, p consumptionPricing) float64 {
	cpuRaw := fp.vcpu * h.billingUnit.PerVCPU
	ramRaw := fp.ramGB * h.billingUnit.PerGBRAM
	pvRaw := fp.storageGB * h.billingUnit.PerGBStorage
	return p.price(cpuRaw, ramRaw, pvRaw)
}

// userNamespaces returns the set of k8s environment namespaces that belong to
// projects (tenant workloads). Everything OpenCost reports outside this set is
// treated as shared platform infra.
func (h *Handler) userNamespaces(ctx context.Context) (map[string]bool, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT DISTINCT namespace FROM environments
		 WHERE runtime = 'k8s' AND namespace <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var ns string
		if err := rows.Scan(&ns); err != nil {
			return nil, err
		}
		out[ns] = true
	}
	return out, rows.Err()
}
