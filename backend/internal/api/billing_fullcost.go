package api

import (
	"context"
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
// where userShare_T = (cost of USER namespaces for type T) / (whole-cluster cost
// for type T). If users consume all of type T the factor is 1; the less of the
// cluster users occupy, the more overhead each unit carries. The minUtilization
// floor caps the factor (1/minUtil) so early-stage, when a few small workloads
// sit on a big platform, bills do not explode to 30-40x -- the factor stays put
// at the floor and converges to the true ratio as adoption grows. Because the
// denominator is the whole cluster (not the head-count of users), the factor is
// stable: adding a user does not swing existing users' bills.
//
// billingMinUtilization floors the per-type user share so the overhead factor
// tops out at 1/billingMinUtilization (~3.33x). billingMargin is the profit
// lever applied AFTER overhead loading; it replaces the old flat 2.7 markup,
// which conflated overhead and margin. Both are tunable.
const (
	billingMinUtilization = 0.30
	billingMargin         = 1.4
)

// consumptionPricing carries the per-type overhead factors (>=1) and the profit
// margin used to turn a raw OpenCost allocation into a customer-facing price.
type consumptionPricing struct {
	fCPU, fRAM, fPV float64
	margin          float64
}

// price applies the per-type overhead factors and the margin to a resource's raw
// OpenCost per-type costs, rounded to two decimals.
func (p consumptionPricing) price(cpuCost, ramCost, pvCost float64) float64 {
	loaded := cpuCost*p.fCPU + ramCost*p.fRAM + pvCost*p.fPV
	return round2(loaded * p.margin)
}

// billingPricing derives the current per-type overhead factors from live
// OpenCost cluster data. User namespaces come from the environments table (every
// k8s environment namespace); everything else OpenCost reports is shared infra.
// Best-effort: when OpenCost is unavailable it returns factors of 1 (no overhead
// loading), so pricing degrades to raw*margin rather than erroring.
func (h *Handler) billingPricing(ctx context.Context, start, end time.Time) consumptionPricing {
	p := consumptionPricing{fCPU: 1, fRAM: 1, fPV: 1, margin: billingMargin}
	if h.opencost == nil {
		return p
	}
	userNS, err := h.userNamespaces(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("billing pricing: user namespace lookup failed; overhead factor defaults to 1")
		return p
	}
	window := start.Format(time.RFC3339) + "," + end.Format(time.RFC3339)
	allocs, err := h.opencost.Compute(ctx, window, "namespace", "")
	if err != nil {
		log.Warn().Err(err).Msg("billing pricing: opencost cluster query failed; overhead factor defaults to 1")
		return p
	}

	var userCPU, userRAM, userPV float64
	var totCPU, totRAM, totPV float64
	for ns, a := range allocs {
		totCPU += a.CPUCost
		totRAM += a.RAMCost
		totPV += a.PVCost
		if userNS[ns] {
			userCPU += a.CPUCost
			userRAM += a.RAMCost
			userPV += a.PVCost
		}
	}

	p.fCPU = overheadFactor(userCPU, totCPU)
	p.fRAM = overheadFactor(userRAM, totRAM)
	p.fPV = overheadFactor(userPV, totPV)
	return p
}

// overheadFactor returns 1 / max(userCost/total, minUtilization): how much each
// raw user unit must scale up to also carry the shared infra overhead, bounded
// by the minUtilization floor. Falls back to 1 when the total is zero.
func overheadFactor(userCost, total float64) float64 {
	if total <= 0 {
		return 1
	}
	share := userCost / total
	if share < billingMinUtilization {
		share = billingMinUtilization
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

// opencostPodCosts returns the raw per-type OpenCost allocation for pods in a
// namespace over the window, keyed by pod name. Used to price the shared
// Postgres pod and the PowerDNS pod, whose cost is split across their logical
// consumers (databases by data size, DNS by active zone). Best-effort: empty on
// any failure.
func (h *Handler) opencostPodCosts(ctx context.Context, namespace string, start, end time.Time) map[string]opencost.Allocation {
	out := map[string]opencost.Allocation{}
	if h.opencost == nil {
		return out
	}
	window := start.Format(time.RFC3339) + "," + end.Format(time.RFC3339)
	allocs, err := h.opencost.Compute(ctx, window, "pod", `namespace:"`+namespace+`"`)
	if err != nil {
		log.Warn().Err(err).Str("namespace", namespace).Msg("billing consumption: opencost pod cost query failed")
		return out
	}
	return allocs
}
