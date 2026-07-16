package api

import (
	"context"
	"net/http"
	"strings"
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

// allowedCostWindows whitelists the OpenCost window values the UI may request.
// Restricting to a fixed set keeps the value out of any injection surface and
// bounds the query cost. Note the on-cluster Prometheus retains only ~7d, so
// windows beyond that fill in from OpenCost's own history as it accumulates.
var allowedCostWindows = map[string]bool{
	"24h": true,
	"7d":  true,
	"14d": true,
	"30d": true,
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
	if !allowedCostWindows[window] {
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
	return cache.Fetch(ctx, h.cache,
		"cost:allocs:"+window, h.cfg.CacheCostTTL,
		func() (map[string]opencost.Allocation, error) {
			return h.opencost.Compute(ctx, window, "namespace", "")
		})
}

// StartCostCacheWarmer refreshes the cost cache for every allowed window on an
// interval so the expensive OpenCost aggregation is paid by this background loop,
// never by a user request. It also keeps OpenCost's own compute cache warm.
// No-op unless both OpenCost and the Redis cache are configured; interval must be
// shorter than CacheCostTTL so the entries never expire between refreshes.
//
// It uses a dedicated patient OpenCost client (120s) rather than the user-facing
// one (20s): a cold aggregation while OpenCost's ETL is still warming up can
// exceed 20s, and the warmer must be able to complete it so the cache gets
// populated off the user path. Users keep the 20s fail-fast client.
//
// The initial warm runs INSIDE the goroutine, never synchronously: a cold or
// slow OpenCost made a synchronous first warm block boot ~76s across windows,
// past the liveness probe budget, crash-looping the backend (nginx 503 for every
// authed user). Do NOT move the first warm() back onto the startup path.
func (h *Handler) StartCostCacheWarmer(ctx context.Context, interval time.Duration) {
	if h.opencost == nil || !h.cache.Enabled() {
		return
	}
	warmClient := opencost.NewWithTimeout(h.cfg.OpenCostURL, 120*time.Second)
	warm := func() {
		for w := range allowedCostWindows {
			wctx, cancel := context.WithTimeout(ctx, 120*time.Second)
			allocs, err := warmClient.Compute(wctx, w, "namespace", "")
			cancel()
			if err != nil {
				log.Warn().Err(err).Str("window", w).Msg("cost warmer: OpenCost compute failed")
				continue
			}
			cache.Store(ctx, h.cache, "cost:allocs:"+w, h.cfg.CacheCostTTL, allocs)
		}
	}
	go func() {
		warm()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				warm()
			}
		}
	}()
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
