package api

import (
	"context"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/opencost"
	"github.com/rs/zerolog/log"
)

// Fully-loaded consumption pricing.
//
// Users must cover not just the bare cost of their own workloads but our shared
// infra overhead (platform namespaces + idle capacity). That overhead is loaded
// onto each user resource through a DYNAMIC per-type factor derived from live
// OpenCost data, then a profit margin is applied on top:
//
//	price_T = raw_cost_T * overhead_factor_T * margin      (T in cpu, ram, pv)
//
// overhead_factor_T = 1 / max(userShare_T, minUtilization)
//
// userShare_T = (cost of USER namespaces for type T) / (whole-cluster cost for
// type T). The minUtilization floor (BILLING_MIN_UTILIZATION, default 0.30) caps
// the factor at 1/minUtil (~3.33x) so early-stage bills do not explode to the
// raw 30-40x infra:user ratio; it converges to the true ratio as adoption grows.
// The denominator is the whole cluster, not the user head-count, so the factor
// is stable. The margin (BILLING_MARGIN, default 1.4) is the profit lever applied
// after overhead loading. Both are config (h.billingMinUtil / h.billingMargin),
// tunable via env without a rebuild.
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

// consumptionPricing carries the per-type overhead factors (>=1) and the profit
// margin used to turn a raw OpenCost allocation into a customer-facing price.
type consumptionPricing struct {
	fCPU, fRAM, fPV float64
	margin          float64
}

// price applies the per-type overhead factors and the margin to a resource's raw
// OpenCost per-type costs, rounded to two decimals. Negative inputs (OpenCost
// emits small negative cost adjustments on sparse data) are clamped to zero.
func (p consumptionPricing) price(cpuCost, ramCost, pvCost float64) float64 {
	loaded := nonNeg(cpuCost)*p.fCPU + nonNeg(ramCost)*p.fRAM + nonNeg(pvCost)*p.fPV
	return round2(loaded * p.margin)
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
		pricing: consumptionPricing{fCPU: 1, fRAM: 1, fPV: 1, margin: h.billingMargin},
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
// derives everything: per-type overhead factors (whole-cluster vs user-namespace
// split), per-app cost indexed by every candidate app label, and the shared
// Postgres/PowerDNS pod costs. Costs are scaled to a monthly run-rate.
func (h *Handler) buildBillingSnapshot(ctx context.Context, client *opencost.Client) (*billingCostSnapshot, error) {
	userNS, err := h.userNamespaces(ctx)
	if err != nil {
		return nil, err
	}

	pods, err := client.Compute(ctx, billingCostWindow, "pod", "")
	if err != nil {
		return nil, err
	}

	var userCPU, userRAM, userPV, totCPU, totRAM, totPV float64
	appCost := make(map[string]opencost.Allocation)
	snap := &billingCostSnapshot{appCost: appCost, builtAt: time.Now()}

	for _, a := range pods {
		ns := a.Properties.Namespace
		if ns == "" || strings.HasPrefix(ns, "__") {
			totCPU += nonNeg(a.CPUCost)
			totRAM += nonNeg(a.RAMCost)
			totPV += nonNeg(a.PVCost)
			continue
		}
		totCPU += nonNeg(a.CPUCost)
		totRAM += nonNeg(a.RAMCost)
		totPV += nonNeg(a.PVCost)
		if userNS[ns] {
			userCPU += nonNeg(a.CPUCost)
			userRAM += nonNeg(a.RAMCost)
			userPV += nonNeg(a.PVCost)
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

	snap.pricing = consumptionPricing{
		fCPU:   overheadFactor(userCPU, totCPU, h.billingMinUtil),
		fRAM:   overheadFactor(userRAM, totRAM, h.billingMinUtil),
		fPV:    overheadFactor(userPV, totPV, h.billingMinUtil),
		margin: h.billingMargin,
	}
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

// overheadFactor returns 1 / max(userCost/total, minUtil): how much each raw
// user unit must scale up to also carry the shared infra overhead, bounded by
// the minUtil floor. Falls back to 1 when the total is zero.
func overheadFactor(userCost, total, minUtil float64) float64 {
	if total <= 0 {
		return 1
	}
	share := userCost / total
	if share < minUtil {
		share = minUtil
	}
	return 1 / share
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
