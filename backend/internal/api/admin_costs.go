package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/beget"
	"github.com/dada-tuda/console/backend/internal/cache"
	"github.com/dada-tuda/console/backend/internal/opencost"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// adminCostsTopLossLimit bounds the top-loss-makers summary the same way
// overviewTopProjectsLimit bounds the overview's top-cost list.
const adminCostsTopLossLimit = 5

// platformClientID / platformClientName is the pseudo-client every namespace
// not owned by a project (argocd, monitoring, databases, opensearch, ...)
// rolls up under, per the god-admin cost drilldown spec.
const platformClientID = "platform"
const platformClientName = "Platform (internal)"

// unallocatedResourceName is the line item for OpenCost cost with no namespace
// (idle capacity, unattributed cluster overhead) -- real spend, but not
// chargeable to any client or project.
const unallocatedResourceName = "unallocated / idle"

type adminCostResource struct {
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	CPUCost   float64 `json:"cpu_cost"`
	RAMCost   float64 `json:"ram_cost"`
	PVCost    float64 `json:"pv_cost"`
	TotalCost float64 `json:"total_cost"`
}

type adminCostProject struct {
	ProjectID   string              `json:"project_id"`
	ProjectName string              `json:"project_name"`
	Cost        float64             `json:"cost"`
	Revenue     float64             `json:"revenue"`
	Margin      float64             `json:"margin"`
	Resources   []adminCostResource `json:"resources"`
}

type adminCostClient struct {
	ClientID   string             `json:"client_id"`
	ClientName string             `json:"client_name"`
	Cost       float64            `json:"cost"`
	Revenue    float64            `json:"revenue"`
	Margin     float64            `json:"margin"`
	Projects   []adminCostProject `json:"projects"`
}

type adminCostLossMaker struct {
	ClientName string  `json:"client_name"`
	Margin     float64 `json:"margin"`
}

// adminCostsAccumulator is the mutable working set GetAdminCosts builds while
// walking the OpenCost pod allocation set, before it is flattened into the
// response tree and sorted. resources is a client/project/resource-name index
// kept alongside clients because Go slice appends can reallocate, which would
// invalidate any *adminCostProject/*adminCostResource held across iterations.
type adminCostsAccumulator struct {
	clients   map[string]*adminCostClient
	resources map[string]map[string]map[string]*adminCostResource
}

// GetAdminCosts returns the cost/revenue/margin drilldown tree for the
// god-admin economics view: clients (project owners) -> projects ->
// resources. Platform-admin only (isGod), same gate as /admin/overview.
//
// cost: OpenCost's per-namespace/per-workload proportions, scaled so the
// cluster-wide sum equals the real hardware bill for the window (see
// resolveHardwareCost). revenue: what our own consumption-pricing formula
// (billing_fullcost.go: raw OpenCost cost * per-type overhead factor *
// margin) would charge that project's apps over the window. margin =
// revenue - cost. Fail-open: an OpenCost outage returns available=false
// with the rest of the payload empty, never a 5xx.
//
// @ID          getAdminCosts
// @Summary     Platform cost/revenue/margin drilldown (platform-admin only)
// @Description Returns a client -> project -> resource cost tree: OpenCost proportions scaled to the real hardware bill (cost), our own consumption-pricing formula applied to the same usage (revenue), and their difference (margin). Platform-admin only (/platform-admins group); every other caller gets 403.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Param       days query    int false "Window length in days: 7 or 30 (default 30)"
// @Success     200 {object} map[string]interface{}
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Router      /admin/costs [get]
func (h *Handler) GetAdminCosts(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
		return
	}

	days := 30
	if v, err := strconv.Atoi(c.Query("days")); err == nil && (v == 7 || v == 30) {
		days = v
	}
	window := strconv.Itoa(days) + "d"

	ctx := c.Request.Context()

	if h.opencost == nil {
		c.JSON(http.StatusOK, gin.H{
			"available": false,
			"note":      "OpenCost not configured",
			"days":      days,
			"window":    window,
			"currency":  costCurrency,
		})
		return
	}

	podAllocs, err := h.adminCostPodAllocs(ctx, window)
	if err != nil {
		log.Warn().Err(err).Msg("admin costs: OpenCost pod aggregation failed")
		c.JSON(http.StatusOK, gin.H{
			"available": false,
			"note":      "cost data temporarily unavailable",
			"days":      days,
			"window":    window,
			"currency":  costCurrency,
		})
		return
	}

	nsMap, err := h.adminCostNamespaceOwners(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("admin costs: failed to load namespace->owner map")
		c.JSON(http.StatusOK, gin.H{
			"available": false,
			"note":      "cost data temporarily unavailable",
			"days":      days,
			"window":    window,
			"currency":  costCurrency,
		})
		return
	}

	revenueByNS := h.adminRevenueByNamespace(ctx)

	rawTotal, unallocatedRaw := adminCostsRawTotal(podAllocs)
	hardwareTotal, hardwareSource, hardwareBreakdown := h.resolveHardwareCost(ctx, days)
	scale := 1.0
	if (hardwareSource == "beget_api" || hardwareSource == "beget_manual_config") && rawTotal > 0 {
		scale = hardwareTotal / rawTotal
	} else {
		hardwareTotal = rawTotal
	}

	acc := &adminCostsAccumulator{
		clients:   map[string]*adminCostClient{},
		resources: map[string]map[string]map[string]*adminCostResource{},
	}

	for _, a := range podAllocs {
		ns := a.Properties.Namespace
		if ns == "" || strings.HasPrefix(ns, "__") {
			continue
		}
		clientID, clientName, projectID, projectName := adminCostOwnerOf(ns, nsMap)
		resourceName, kind := adminCostResourceKey(a)
		acc.add(clientID, clientName, projectID, projectName, resourceName, kind, a, scale)
	}

	clients := make([]*adminCostClient, 0, len(acc.clients))
	for _, cl := range acc.clients {
		for i := range cl.Projects {
			p := &cl.Projects[i]
			sort.Slice(p.Resources, func(i, j int) bool { return p.Resources[i].TotalCost > p.Resources[j].TotalCost })
			for _, r := range p.Resources {
				p.Cost += r.TotalCost
			}
			p.Revenue = round2(revenueByNS[p.ProjectID] * float64(days) / billingMonthDays)
			p.Margin = round2(p.Revenue - p.Cost)
			cl.Cost += p.Cost
			cl.Revenue += p.Revenue
		}
		cl.Cost = round2(cl.Cost)
		cl.Revenue = round2(cl.Revenue)
		cl.Margin = round2(cl.Revenue - cl.Cost)
		sort.Slice(cl.Projects, func(i, j int) bool { return cl.Projects[i].Cost > cl.Projects[j].Cost })
		clients = append(clients, cl)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].Cost > clients[j].Cost })

	var totalCost, totalRevenue float64
	lossMakers := make([]adminCostLossMaker, 0, len(clients))
	for _, cl := range clients {
		totalCost += cl.Cost
		totalRevenue += cl.Revenue
		if cl.ClientID != platformClientID {
			lossMakers = append(lossMakers, adminCostLossMaker{ClientName: cl.ClientName, Margin: cl.Margin})
		}
	}
	sort.Slice(lossMakers, func(i, j int) bool { return lossMakers[i].Margin < lossMakers[j].Margin })
	if len(lossMakers) > adminCostsTopLossLimit {
		lossMakers = lossMakers[:adminCostsTopLossLimit]
	}

	unallocated := adminCostResource{
		Name:      unallocatedResourceName,
		Kind:      "unallocated",
		TotalCost: round2(unallocatedRaw * scale),
	}
	totalCost += unallocated.TotalCost

	c.JSON(http.StatusOK, gin.H{
		"available":           true,
		"days":                days,
		"window":              window,
		"currency":            costCurrency,
		"hardware_source":     hardwareSource,
		"hardware_total_cost": round2(hardwareTotal),
		"hardware":            hardwareBreakdown,
		"opencost_raw_total":  round2(rawTotal),
		"scale_factor":        round2(scale),
		"total_cost":          round2(totalCost),
		"total_revenue":       round2(totalRevenue),
		"total_margin":        round2(totalRevenue - totalCost),
		"unallocated":         unallocated,
		"top_loss_makers":     lossMakers,
		"clients":             clients,
	})
}

// add records one pod allocation's scaled cost under client -> project ->
// resource, creating each level on first sight.
func (acc *adminCostsAccumulator) add(clientID, clientName, projectID, projectName, resourceName, kind string, a opencost.Allocation, scale float64) {
	cl, ok := acc.clients[clientID]
	if !ok {
		cl = &adminCostClient{ClientID: clientID, ClientName: clientName, Projects: []adminCostProject{}}
		acc.clients[clientID] = cl
		acc.resources[clientID] = map[string]map[string]*adminCostResource{}
	}

	projResources, ok := acc.resources[clientID][projectID]
	if !ok {
		cl.Projects = append(cl.Projects, adminCostProject{
			ProjectID: projectID, ProjectName: projectName, Resources: []adminCostResource{},
		})
		projResources = map[string]*adminCostResource{}
		acc.resources[clientID][projectID] = projResources
	}

	r, ok := projResources[resourceName]
	if !ok {
		pi := adminCostProjectIndex(cl, projectID)
		cl.Projects[pi].Resources = append(cl.Projects[pi].Resources, adminCostResource{Name: resourceName, Kind: kind})
		r = &cl.Projects[pi].Resources[len(cl.Projects[pi].Resources)-1]
		projResources[resourceName] = r
	}
	r.CPUCost += round2(nonNeg(a.CPUCost) * scale)
	r.RAMCost += round2(nonNeg(a.RAMCost) * scale)
	r.PVCost += round2(nonNeg(a.PVCost) * scale)
	r.TotalCost += round2(nonNeg(a.TotalCost) * scale)
}

// adminCostProjectIndex finds a client's project slot by ID.
func adminCostProjectIndex(cl *adminCostClient, projectID string) int {
	for i := range cl.Projects {
		if cl.Projects[i].ProjectID == projectID {
			return i
		}
	}
	return -1
}

// adminCostPodAllocs returns the cluster-wide OpenCost pod-level allocation
// set for a window, cached like clusterAllocsByWindow (cost.go) but keyed
// separately since the aggregation level differs ("pod" vs "namespace").
func (h *Handler) adminCostPodAllocs(ctx context.Context, window string) (map[string]opencost.Allocation, error) {
	return cache.Fetch(ctx, h.cache,
		"cost:admin:pod:"+window, h.cfg.CacheCostTTL,
		func() (map[string]opencost.Allocation, error) {
			return h.opencost.Compute(ctx, window, "pod", "")
		})
}

// adminCostOwner is one project's ownership info for the cost drilldown.
type adminCostOwner struct {
	projectID, projectName string
	ownerID, ownerName     string
}

// adminCostNamespaceOwners maps every project namespace to its project and
// owning user, mirroring allNamespaceProjects (admin_overview.go) plus the
// owner join the cost drilldown needs to group projects into clients.
func (h *Handler) adminCostNamespaceOwners(ctx context.Context) (map[string]adminCostOwner, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT e.namespace, p.id, p.display_name,
		       COALESCE(u.id::text, ''), COALESCE(NULLIF(u.email, ''), NULLIF(u.display_name, ''), '')
		FROM environments e
		JOIN projects p     ON p.id = e.project_id
		LEFT JOIN users u   ON u.id = p.owner_id
		WHERE e.runtime = 'k8s' AND e.namespace <> ''`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]adminCostOwner)
	for rows.Next() {
		var ns, projectID, projectName, ownerID, ownerName string
		if err := rows.Scan(&ns, &projectID, &projectName, &ownerID, &ownerName); err != nil {
			return nil, err
		}
		out[ns] = adminCostOwner{projectID: projectID, projectName: projectName, ownerID: ownerID, ownerName: ownerName}
	}
	return out, rows.Err()
}

// adminCostOwnerOf resolves a namespace to (clientID, clientName, projectID,
// projectName). Namespaces outside the project map are shared platform infra
// (argocd, monitoring, databases, opencost, ...) and roll up under the
// platform pseudo-client, one synthetic "project" per real namespace so the
// breakdown stays legible.
func adminCostOwnerOf(ns string, nsMap map[string]adminCostOwner) (clientID, clientName, projectID, projectName string) {
	owner, ok := nsMap[ns]
	if !ok {
		return platformClientID, platformClientName, "ns:" + ns, ns
	}
	if owner.ownerID == "" {
		return "unowned:" + owner.projectID, owner.projectName + " (no owner)", owner.projectID, owner.projectName
	}
	name := owner.ownerName
	if name == "" {
		name = owner.ownerID
	}
	return owner.ownerID, name, owner.projectID, owner.projectName
}

// adminCostResourceKey mirrors appLabelKeys (billing_fullcost.go): a pod's
// resource identity is its console app label when present, else the raw pod
// name. Kind is a coarse classification for the UI icon/badge only.
func adminCostResourceKey(a opencost.Allocation) (name, kind string) {
	for _, lk := range appLabelKeys {
		if v := a.Properties.Labels[lk]; v != "" {
			return v, adminCostKindOf(a.Properties.Pod)
		}
	}
	if a.Properties.Pod != "" {
		return a.Properties.Pod, adminCostKindOf(a.Properties.Pod)
	}
	return "(unlabeled)", "workload"
}

// adminCostKindOf classifies a pod's shared-infra role by its pod-name prefix.
func adminCostKindOf(pod string) string {
	switch {
	case strings.HasPrefix(pod, "postgresql"):
		return "database"
	case strings.HasPrefix(pod, "powerdns"):
		return "dns"
	default:
		return "app"
	}
}

// adminCostsRawTotal sums every pod allocation's TotalCost, split into the
// namespaced total (real, attributable spend) and the idle/unallocated total
// (Properties.Namespace empty or an OpenCost internal "__..." bucket).
func adminCostsRawTotal(podAllocs map[string]opencost.Allocation) (namespacedTotal, unallocatedTotal float64) {
	for _, a := range podAllocs {
		ns := a.Properties.Namespace
		if ns == "" || strings.HasPrefix(ns, "__") {
			unallocatedTotal += nonNeg(a.TotalCost)
			continue
		}
		namespacedTotal += nonNeg(a.TotalCost)
	}
	return namespacedTotal + unallocatedTotal, unallocatedTotal
}

// adminCostHardwareGroup is one line of the hardware-cost breakdown: the
// cluster control plane or one worker node pool, with Beget's own computed
// monthly price for every node in the group combined (not per-node).
type adminCostHardwareGroup struct {
	Name          string  `json:"name"`
	NodeCount     int     `json:"node_count"`
	PriceMonthRUB float64 `json:"price_month_rub"`
}

// resolveHardwareCost returns the real hardware spend for a `days`-day
// window, how it was sourced, and (when available) a per-node-pool
// breakdown. Fail-open chain, most authoritative first:
//
//  1. beget_api -- live GET /v1/k8s/cluster against api.beget.com
//     (internal/beget), cached 24h. Beget computes master + per-worker-group
//     price_month itself; this is the real invoice, not a model.
//  2. beget_manual_config -- HARDWARE_MONTHLY_COST_RUB, operator-typed
//     fallback for when the token or API is unavailable.
//  3. opencost_only -- OpenCost's own raw total (scale factor 1), so the
//     response always has a number instead of hanging on money the platform
//     cannot currently source.
//
// Both 1 and 2 are linearly scaled from a full month to the requested
// window; the breakdown figures stay at their natural monthly value (they
// are informational, not summed into anything else).
func (h *Handler) resolveHardwareCost(ctx context.Context, days int) (float64, string, []adminCostHardwareGroup) {
	if total, breakdown, ok := h.begetHardwareCost(ctx); ok {
		return total * float64(days) / billingMonthDays, "beget_api", breakdown
	}
	if h.cfg.HardwareMonthlyCostRUB > 0 {
		return h.cfg.HardwareMonthlyCostRUB * float64(days) / billingMonthDays, "beget_manual_config", nil
	}
	return 0, "opencost_only", nil
}

// begetHardwareCost fetches the configured cluster's live price_month total
// from the Beget managed-Kubernetes billing API (24h cache -- Beget's own
// pricing changes on the timescale of tariff updates, not requests). Returns
// ok=false on any failure (nil client, network error, cluster not found)
// so resolveHardwareCost falls through to the next source; a Beget outage
// must never surface as a 5xx here.
func (h *Handler) begetHardwareCost(ctx context.Context) (float64, []adminCostHardwareGroup, bool) {
	if h.beget == nil {
		return 0, nil, false
	}
	clusters, err := cache.Fetch(ctx, h.cache, "beget:clusters", 24*time.Hour,
		func() ([]beget.Cluster, error) { return h.beget.ListClusters(ctx) })
	if err != nil {
		log.Warn().Err(err).Msg("admin costs: beget cluster list failed, falling back")
		return 0, nil, false
	}
	cl, ok := beget.FindClusterBySlug(clusters, h.cfg.BegetK8SClusterSlug)
	if !ok {
		log.Warn().Str("slug", h.cfg.BegetK8SClusterSlug).Msg("admin costs: beget token cannot see configured cluster slug, falling back")
		return 0, nil, false
	}
	breakdown := []adminCostHardwareGroup{
		{Name: "control plane", NodeCount: cl.MasterNodeCount, PriceMonthRUB: round2(cl.MasterPriceRUB)},
	}
	for _, wg := range cl.WorkerGroups {
		breakdown = append(breakdown, adminCostHardwareGroup{
			Name: wg.DisplayName, NodeCount: wg.NodeCount, PriceMonthRUB: round2(wg.PriceMonth),
		})
	}
	return cl.TotalMonthlyRUB(), breakdown, true
}

// adminRevenueByNamespace prices every project namespace's app allocations
// through the same consumption-pricing formula as GetProjectConsumption
// (billing_fullcost.go: raw OpenCost cost * per-type overhead factor *
// margin), summed per namespace at the snapshot's fixed monthly figure --
// callers scale it down to their requested window themselves. This is "what
// we would charge", not the real hardware cost -- it deliberately does NOT
// use the Beget-scaled numbers computed for the cost side. Best-effort: an
// empty/failed snapshot yields all zeros (the snapshot itself is fail-open,
// see billingSnapshot).
func (h *Handler) adminRevenueByNamespace(ctx context.Context) map[string]float64 {
	out := map[string]float64{}
	snap := h.billingSnapshot(ctx)
	for key, alloc := range snap.appCost {
		ns := key
		if i := strings.IndexByte(key, '/'); i >= 0 {
			ns = key[:i]
		}
		out[ns] += snap.pricing.price(alloc.CPUCost, alloc.RAMCost, alloc.PVCost)
	}
	return out
}
