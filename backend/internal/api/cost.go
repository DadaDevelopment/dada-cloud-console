package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dada-tuda/console/backend/internal/cache"
	"github.com/dada-tuda/console/backend/internal/opencost"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// costCurrency is the currency the on-cluster OpenCost custom pricing is
// denominated in (see the OpenCost custom-pricing configmap in argo-infra,
// which mirrors the console billing engine). OpenCost itself is unitless; the
// console owns the label.
const costCurrency = "RUB"

// costRequestBudget is the hard wall-clock cap a user-facing request may
// spend waiting on cost data (OpenCost/Mimir aggregations, DB fan-out over
// cached snapshots). Every cost-reading handler is expected to serve from a
// snapshot that a background warmer refreshes well inside this budget; the
// budget exists so a cold/miss cache degrades to a fast "stale" response
// instead of hanging the request on a live 20s OpenCost call. The background
// warmer itself uses its own patient client/context and is not subject to
// this budget.
const costRequestBudget = 2 * time.Second

// withCostBudget derives a context capped at costRequestBudget from a request
// context, for use around any cache.Fetch call that might fall through to a
// live OpenCost/Mimir/Prometheus query on a cache miss.
func withCostBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, costRequestBudget)
}

// fastCostWindows and slowCostWindows together whitelist the OpenCost window
// values the UI may request. Restricting to a fixed set keeps the value out of
// any injection surface and bounds the query cost. Note the on-cluster
// Prometheus retains only ~7d, so windows beyond that fill in from OpenCost's
// own history as it accumulates.
//
// The split is by what the window COSTS upstream, not by what it means.
// OpenCost answers an allocation window by issuing one PromQL query per day in
// it, so cost grows with the window: measured on prod, 24h ~6s and 7d ~40-86s
// against 14d ~63-111s and 30d ~94-121s. The two long windows were ~75% of
// every warm sweep while changing least between refreshes, so they are warmed
// on their own slow schedule (CostSlowWarmInterval) and cached far longer
// (CacheCostSlowTTL) than the short ones.
var (
	fastCostWindows = []string{"24h", "7d"}
	slowCostWindows = []string{"14d", "30d"}
)

// costWindowAllowed reports whether a user-supplied window is one of the four
// whitelisted values.
func costWindowAllowed(window string) bool {
	return slices.Contains(fastCostWindows, window) || slices.Contains(slowCostWindows, window)
}

// costCacheTTL is how long a cached allocation set for a window stays valid.
// Read and write paths must agree on it: a user request that misses and
// recomputes must not re-store a long window under the short TTL, or the next
// user pays the full aggregation again.
func (h *Handler) costCacheTTL(window string) time.Duration {
	if slices.Contains(slowCostWindows, window) {
		return h.cfg.CacheCostSlowTTL
	}
	return h.cfg.CacheCostTTL
}

// envCost is the per-environment (per-namespace) cost breakdown.
type envCost struct {
	Environment string  `json:"environment"`
	Namespace   string  `json:"namespace"`
	CPU         float64 `json:"cpu"`
	RAM         float64 `json:"ram"`
	PV          float64 `json:"pv"`
	Total       float64 `json:"total"`
}

// GetProjectCost returns the resource cost of a project over a time window,
// aggregated from the OpenCost Allocation API by Kubernetes namespace. The
// project->namespace join lives in the console DB (environments table), so the
// view does not depend on OpenCost reading namespace labels.
//
// The OpenCost allocation set is fetched aggregated by namespace with no filter,
// so it is cluster-wide and identical for every project; only the per-project
// namespace filter differs. It is therefore cached by window alone (the four
// allowed values) via the fail-open cache-aside layer, so one OpenCost
// aggregation serves every project's cost card for CacheCostTTL and caps the
// tail latency of a slow/cold OpenCost query. A Redis outage falls straight
// through to OpenCost.
//
// @ID          getProjectCost
// @Summary     Get per-project resource cost
// @Description Returns the project's resource cost (CPU, RAM, persistent volumes) over a window, aggregated from OpenCost by namespace with a per-environment breakdown. Costs are in RUB (on-cluster OpenCost custom pricing). Read-only; best-effort, returns 503 when OpenCost is not configured.
// @Tags        cost
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       window    query    string false "Cost window: 24h, 7d, 14d or 30d (default 30d)"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/cost [get]
func (h *Handler) GetProjectCost(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	if !h.requireProjectMember(c, projectID) {
		return
	}
	if h.opencost == nil {
		respondError(c, http.StatusServiceUnavailable, "cost reporting not configured")
		return
	}

	window := c.DefaultQuery("window", "30d")
	if !costWindowAllowed(window) {
		respondError(c, http.StatusBadRequest, "invalid window: allowed 24h, 7d, 14d, 30d")
		return
	}

	nsToEnv, err := h.projectNamespaces(c.Request.Context(), projectID)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cost data temporarily unavailable")
		return
	}
	if len(nsToEnv) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"window": window, "currency": costCurrency, "total": 0,
			"cpu": 0, "ram": 0, "pv": 0, "by_environment": []envCost{},
		})
		return
	}

	allocs, err := h.clusterAllocsByWindow(c.Request.Context(), window)
	if err != nil {
		respondError(c, http.StatusServiceUnavailable, "cost data temporarily unavailable")
		return
	}

	var totalCPU, totalRAM, totalPV, total float64
	breakdown := make([]envCost, 0, len(nsToEnv))
	for ns, env := range nsToEnv {
		a := allocs[ns]
		breakdown = append(breakdown, envCost{
			Environment: env,
			Namespace:   ns,
			CPU:         a.CPUCost,
			RAM:         a.RAMCost,
			PV:          a.PVCost,
			Total:       a.TotalCost,
		})
		totalCPU += a.CPUCost
		totalRAM += a.RAMCost
		totalPV += a.PVCost
		total += a.TotalCost
	}

	c.JSON(http.StatusOK, gin.H{
		"window":         window,
		"currency":       costCurrency,
		"total":          total,
		"cpu":            totalCPU,
		"ram":            totalRAM,
		"pv":             totalPV,
		"by_environment": breakdown,
	})
}

// clusterAllocsByWindow returns the cluster-wide OpenCost allocation set for a
// window, served from the cache-aside layer. The set is identical for every
// project (aggregated by namespace, no filter), so one entry per window backs
// every project's cost card. Kept warm by StartCostCacheWarmer so a request
// almost never pays OpenCost's cold ~14s 30d aggregation.
func (h *Handler) clusterAllocsByWindow(ctx context.Context, window string) (map[string]opencost.Allocation, error) {
	bctx, cancel := withCostBudget(ctx)
	defer cancel()
	return cache.Fetch(bctx, h.cache,
		"cost:allocs:"+window, h.costCacheTTL(window),
		func() (map[string]opencost.Allocation, error) {
			return h.opencost.Compute(bctx, window, "namespace", "")
		})
}

// StartCostCacheWarmer refreshes the cost cache so the expensive OpenCost
// aggregation is paid by this background loop, never by a user request. It also
// keeps OpenCost's own compute cache warm. No-op unless both OpenCost and the
// Redis cache are configured.
//
// It runs TWO loops, not one, because the windows differ by an order of
// magnitude in what they cost upstream (see fastCostWindows/slowCostWindows):
//
//   - fast (CostWarmInterval, default 150s): 24h + 7d namespace allocations,
//     the admin pod-allocation window, the billing snapshot, per-project
//     consumptions. All cached for CacheCostTTL.
//   - slow (CostSlowWarmInterval, default 30m): 14d + 30d namespace
//     allocations, cached for CacheCostSlowTTL.
//
// The intervals are explicit config, NOT derived from the cache TTL. Deriving
// the interval as CacheCostTTL/2 silently assumed a sweep fits inside the TTL;
// once the full sweep grew to 215-339s against a 150s interval, ticks ran
// end-to-end with no idle gap, on both replicas, and OpenCost's fan-out
// (one PromQL per day of window) pinned Mimir at ~1.7 CPU — the cluster's
// single largest consumer. If a sweep ever again takes longer than its
// interval, that is a signal to lengthen the interval, not to let it free-run.
//
// Both loops are guarded by a Postgres advisory lock (advisory_lock.go), so
// exactly ONE replica warms per tick. The result lands in shared Redis, so a
// second replica recomputing it buys nothing and doubles the upstream load.
//
// Each loop uses the same dedicated patient OpenCost client (CostWarmTimeout,
// default 240s) rather than the user-facing one (20s): OpenCost's own
// allocation/compute call slows down under Mimir CPU throttling (observed 34s
// at 1d window, >60s at 7d/30d), and the warmer must be able to ride that out
// so the cache gets populated off the user path. Users keep the 20s fail-fast
// client.
//
// Every window/step below runs SEQUENTIALLY within its loop (a plain for loop,
// no per-window goroutine fan-out) so slow OpenCost/Mimir windows do not pile
// parallel load onto an already-throttled upstream and compound each other's
// latency.
//
// The initial warm runs INSIDE the goroutine, never synchronously: a cold or
// slow OpenCost made a synchronous first warm block boot ~76s across windows,
// past the liveness probe budget, crash-looping the backend (nginx 503 for every
// authed user). Do NOT move the first warm back onto the startup path.
func (h *Handler) StartCostCacheWarmer(ctx context.Context) {
	if h.opencost == nil || !h.cache.Enabled() {
		return
	}
	warmClient := opencost.NewWithTimeout(h.cfg.OpenCostURL, h.cfg.CostWarmTimeout)

	h.startCostWarmLoop(ctx, "cost-warm-fast", lockKeyCostWarmFast, h.cfg.CostWarmInterval,
		func(ctx context.Context) { h.warmFastCost(ctx, warmClient) })
	h.startCostWarmLoop(ctx, "cost-warm-slow", lockKeyCostWarmSlow, h.cfg.CostSlowWarmInterval,
		func(ctx context.Context) { h.warmSlowCost(ctx, warmClient) })
}

// startCostWarmLoop runs tick immediately and then on an interval, gated so
// that the whole deployment performs one pass per interval no matter how many
// replicas are running.
//
// Three guards, each covering a case the others miss:
//
//   - The Redis claim is the rate gate. Replicas tick on independent timers and
//     drift apart in phase, so a mutual-exclusion lock alone never sees
//     contention and every replica ends up doing a full pass — measured on prod
//     as double the intended OpenCost load with two replicas. The claim lives
//     for slightly less than the interval so ticker jitter cannot swallow a
//     whole cycle.
//   - The advisory lock covers a tick that outlives its own claim (degraded
//     OpenCost); without it the expired claim would let a second replica pile
//     on exactly when upstream is already struggling.
//   - The in-process atomic guard stops one pod from starting a second tick
//     while its own is still running: the advisory lock is re-entrant within a
//     session and would not catch that.
func (h *Handler) startCostWarmLoop(ctx context.Context, name string, lockKey int64, interval time.Duration, tick func(context.Context)) {
	claimTTL := interval - interval/10
	var running atomic.Bool
	run := func() {
		if !running.CompareAndSwap(false, true) {
			log.Warn().Str("loop", name).Msg("cost warmer: previous tick still running, skipping this tick")
			return
		}
		defer running.Store(false)
		if !h.cache.TryClaim(ctx, "cost:warm:claim:"+name, claimTTL) {
			log.Debug().Str("loop", name).Msg("cost warmer: another replica warmed within this interval, skipping this tick")
			return
		}
		if !runWithAdvisoryLock(ctx, h.pool, lockKey, name, tick) {
			log.Debug().Str("loop", name).Msg("cost warmer: another replica holds the lock, skipping this tick")
		}
	}
	go func() {
		run()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				run()
			}
		}
	}()
}

// warmFastCost refreshes everything whose upstream cost is small enough to pay
// every CostWarmInterval: the short namespace windows, the admin pod window,
// the billing snapshot, and per-project consumptions.
func (h *Handler) warmFastCost(ctx context.Context, warmClient *opencost.Client) {
	start := time.Now()
	durations := make(map[string]time.Duration, len(fastCostWindows)+len(adminCostsWindows)+2)
	okWindows, failedWindows := h.warmAllocWindows(ctx, warmClient, fastCostWindows, durations)

	for _, w := range adminCostsWindows {
		wStart := time.Now()
		wctx, cancel := context.WithTimeout(ctx, h.cfg.CostWarmTimeout)
		podAllocs, err := warmClient.Compute(wctx, w, "pod", "")
		cancel()
		if err != nil {
			failedWindows++
			log.Warn().Err(err).Str("window", w).Dur("elapsed", time.Since(wStart)).Msg("cost warmer: OpenCost admin pod compute failed")
			continue
		}
		cache.Store(ctx, h.cache, "cost:admin:pod:"+w, h.cfg.CacheCostTTL, podAllocs)
		okWindows++
		durations["admin:pod:"+w] = time.Since(wStart)
	}

	snapStart := time.Now()
	h.warmBillingSnapshot(ctx, warmClient)
	durations["billing:snapshot"] = time.Since(snapStart)

	pcStart := time.Now()
	okProjects, failedProjects, failedProjectIDs := h.warmProjectConsumptions(ctx)
	durations["project:consumptions"] = time.Since(pcStart)

	log.Info().
		Str("loop", "cost-warm-fast").
		Dur("total", time.Since(start)).
		Int("ok_windows", okWindows).
		Int("failed_windows", failedWindows).
		Int("ok_projects", okProjects).
		Int("failed_projects", failedProjects).
		Strs("failed_project_ids", failedProjectIDs).
		Interface("durations", durations).
		Msg("cost warmer: tick complete")
}

// warmSlowCost refreshes the long namespace windows only. It is deliberately
// the whole body of the slow loop: nothing else the warmer does is expensive
// enough upstream to be worth delaying by half an hour.
func (h *Handler) warmSlowCost(ctx context.Context, warmClient *opencost.Client) {
	start := time.Now()
	durations := make(map[string]time.Duration, len(slowCostWindows))
	okWindows, failedWindows := h.warmAllocWindows(ctx, warmClient, slowCostWindows, durations)

	log.Info().
		Str("loop", "cost-warm-slow").
		Dur("total", time.Since(start)).
		Int("ok_windows", okWindows).
		Int("failed_windows", failedWindows).
		Interface("durations", durations).
		Msg("cost warmer: tick complete")
}

// warmAllocWindows computes and caches the cluster-wide namespace allocation
// set for each window, recording per-window elapsed time into durations.
// Each window is stored under its own TTL (costCacheTTL), so the loop that
// warms a window and the request that reads it agree on how long it lives.
func (h *Handler) warmAllocWindows(ctx context.Context, warmClient *opencost.Client, windows []string, durations map[string]time.Duration) (ok, failed int) {
	for _, w := range windows {
		wStart := time.Now()
		wctx, cancel := context.WithTimeout(ctx, h.cfg.CostWarmTimeout)
		allocs, err := warmClient.Compute(wctx, w, "namespace", "")
		cancel()
		if err != nil {
			failed++
			log.Warn().Err(err).Str("window", w).Dur("elapsed", time.Since(wStart)).Msg("cost warmer: OpenCost compute failed")
			continue
		}
		cache.Store(ctx, h.cache, "cost:allocs:"+w, h.costCacheTTL(w), allocs)
		ok++
		durations["allocs:"+w] = time.Since(wStart)
	}
	return ok, failed
}

// projectNamespaces returns the k8s namespaces of a project's environments,
// mapped to their environment name. Mirrors the namespace scoping used by the
// logs and metrics read paths.
func (h *Handler) projectNamespaces(ctx context.Context, projectID uuid.UUID) (map[string]string, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT name, namespace FROM environments
		 WHERE project_id = $1 AND runtime = 'k8s' AND namespace <> ''`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var name, ns string
		if err := rows.Scan(&name, &ns); err != nil {
			return nil, err
		}
		out[strings.TrimSpace(ns)] = name
	}
	return out, rows.Err()
}
