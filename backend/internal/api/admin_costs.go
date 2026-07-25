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

// adminCostsTopLossLimit bounds the top-loss-makers summary shown on both the
// admin costs page and the overview money card.
const adminCostsTopLossLimit = 5

// adminCostSampleWindow is the SHORT OpenCost window the platform-economics
// view samples per-pod allocations over. A wide aggregate=pod window (7d/30d)
// fetches a multi-megabyte per-pod set from CPU-throttled Mimir (measured
// 38-85s+, intermittently truncating the response body -> the client decodes
// "unexpected end of JSON input"), so the cache never populated and the page
// failed open with "cost data temporarily unavailable". The 24h sample returns
// in ~7s / ~3MB. Per-client/project cost PROPORTIONS are window-agnostic, so
// buildAdminCostSummary scales the sample up to the reporting window with no
// loss of economic meaning. Do NOT widen this back to the reporting window.
const adminCostSampleWindow = "24h"

// adminCostSampleDays is adminCostSampleWindow expressed in days, the divisor
// that grosses a sample-window cost up to the reporting window.
const adminCostSampleDays = 1.0

// adminCostsWindows is the set of admin pod-allocation windows the background
// cost-cache warmer (cost.go) pre-populates off the user request path. Only the
// short sample window is fetched from OpenCost now; the 7d/30d reporting toggle
// is a pure scale factor applied on read, not a second heavy aggregation.
var adminCostsWindows = []string{adminCostSampleWindow}

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
	Revenue   float64 `json:"revenue"`
	Margin    float64 `json:"margin"`
	MarginPct float64 `json:"margin_pct"`
}

type adminCostProject struct {
	ProjectID   string              `json:"project_id"`
	ProjectName string              `json:"project_name"`
	Cost        float64             `json:"cost"`
	Revenue     float64             `json:"revenue"`
	Margin      float64             `json:"margin"`
	MarginPct   float64             `json:"margin_pct"`
	Resources   []adminCostResource `json:"resources"`
}

type adminCostClient struct {
	ClientID   string             `json:"client_id"`
	ClientName string             `json:"client_name"`
	Cost       float64            `json:"cost"`
	Revenue    float64            `json:"revenue"`
	Margin     float64            `json:"margin"`
	MarginPct  float64            `json:"margin_pct"`
	Projects   []adminCostProject `json:"projects"`
}

type adminCostLossMaker struct {
	ClientName string  `json:"client_name"`
	Margin     float64 `json:"margin"`
}

// adminCostsAccumulator is the mutable working set GetAdminCosts builds while
// walking the OpenCost pod allocation set (cost) and the billing snapshot
// (revenue), before it is flattened into the response tree and sorted.
// resources is a client -> project -> resource-name POSITION index (not a
// pointer cache): Go slice appends can reallocate a project's Resources backing
// array, so a cached *adminCostResource can go stale across a later append.
// Positions are append-stable, so ensureResource re-derives the pointer from
// the current backing array on every call and uses it immediately.
type adminCostsAccumulator struct {
	clients   map[string]*adminCostClient
	resources map[string]map[string]map[string]int
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

	summary := h.buildAdminCostSummary(c.Request.Context(), days)
	if !summary.Available {
		c.JSON(http.StatusOK, gin.H{
			"available": false,
			"note":      summary.Note,
			"days":      summary.Days,
			"window":    summary.Window,
			"currency":  costCurrency,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"available":           true,
		"days":                summary.Days,
		"window":              summary.Window,
		"currency":            costCurrency,
		"hardware_source":     summary.HardwareSource,
		"hardware_total_cost": round2(summary.HardwareTotal),
		"hardware":            summary.HardwareBreakdown,
		"opencost_raw_total":  round2(summary.RawTotal),
		"scale_factor":        round2(summary.Scale),
		"total_cost":          round2(summary.TotalCost),
		"total_revenue":       round2(summary.TotalRevenue),
		"total_margin":        round2(summary.TotalRevenue - summary.TotalCost),
		"unallocated":         summary.Unallocated,
		"top_loss_makers":     summary.TopLossMakers,
		"clients":             summary.Clients,
		"agent_tokens":        h.adminAgentTokenEconomics(c.Request.Context(), days),
	})
}

// adminCostSummary is the fully-computed client -> project -> resource
// economics tree plus its aggregates, shared between GetAdminCosts (full
// tree) and overviewMoney (admin_overview.go: just the business totals for
// the "Деньги" card). Building it is pure in-memory work over the cached
// OpenCost/DB/Beget snapshots (adminCostPodAllocs, adminCostNamespaceOwners,
// billingSnapshot, resolveHardwareCost all read-through the shared
// cache), so computing it twice per overview request is cheap.
type adminCostSummary struct {
	Available         bool
	Note              string
	Days              int
	Window            string
	HardwareSource    string
	HardwareTotal     float64
	HardwareBreakdown []adminCostHardwareGroup
	RawTotal          float64
	Scale             float64
	TotalCost         float64
	TotalRevenue      float64
	Unallocated       adminCostResource
	TopLossMakers     []adminCostLossMaker
	Clients           []*adminCostClient
}

// buildAdminCostSummary computes the platform cost/revenue/margin economics
// for a `days`-day window. Per-pod allocations are SAMPLED from OpenCost over
// the short adminCostSampleWindow (a wide aggregate=pod window is too slow and
// truncates) and scaled to `days`; see adminCostSampleWindow. Fail-open: any
// missing dependency (OpenCost unconfigured, aggregation failure, namespace-
// owner lookup failure) yields Available=false with Note set, never an error --
// callers on both the admin costs page and the overview money card must degrade
// gracefully instead of failing the whole request.
func (h *Handler) buildAdminCostSummary(ctx context.Context, days int) adminCostSummary {
	window := strconv.Itoa(days) + "d"
	out := adminCostSummary{Days: days, Window: window}

	if h.opencost == nil {
		out.Note = "OpenCost not configured"
		return out
	}

	podAllocs, err := h.adminCostPodAllocs(ctx, adminCostSampleWindow)
	if err != nil {
		log.Warn().Err(err).Msg("admin costs: OpenCost pod aggregation failed")
		out.Note = "cost data temporarily unavailable"
		return out
	}

	nsMap, err := h.adminCostNamespaceOwners(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("admin costs: failed to load namespace->owner map")
		out.Note = "cost data temporarily unavailable"
		return out
	}

	rawTotal, unallocatedRaw := adminCostsRawTotal(podAllocs)
	hardwareTotal, hardwareSource, hardwareBreakdown := h.resolveHardwareCost(ctx, days)
	scale := float64(days) / adminCostSampleDays
	if (hardwareSource == "beget_api" || hardwareSource == "beget_manual_config") && rawTotal > 0 {
		scale = hardwareTotal / rawTotal
	} else {
		hardwareTotal = rawTotal * scale
	}

	acc := &adminCostsAccumulator{
		clients:   map[string]*adminCostClient{},
		resources: map[string]map[string]map[string]int{},
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

	revWindowScale := float64(days) / billingMonthDays
	snap := h.billingSnapshot()
	for key, alloc := range snap.appCost {
		ns, app := key, ""
		if i := strings.IndexByte(key, '/'); i >= 0 {
			ns, app = key[:i], key[i+1:]
		}
		if app == "" {
			continue
		}
		clientID, clientName, projectID, projectName := adminCostOwnerOf(ns, nsMap)
		r := acc.ensureResource(clientID, clientName, projectID, projectName, app, "app")
		r.Revenue += round2(snap.pricing.price(alloc.CPUCost, alloc.RAMCost, alloc.PVCost) * revWindowScale)
	}

	clients := make([]*adminCostClient, 0, len(acc.clients))
	for _, cl := range acc.clients {
		for i := range cl.Projects {
			p := &cl.Projects[i]
			sort.Slice(p.Resources, func(a, b int) bool { return p.Resources[a].TotalCost > p.Resources[b].TotalCost })
			p.Cost, p.Revenue = 0, 0
			for j := range p.Resources {
				r := &p.Resources[j]
				r.TotalCost = round2(r.TotalCost)
				r.Revenue = round2(r.Revenue)
				r.Margin = round2(r.Revenue - r.TotalCost)
				r.MarginPct = marginPct(r.Revenue, r.TotalCost)
				p.Cost += r.TotalCost
				p.Revenue += r.Revenue
			}
			p.Cost = round2(p.Cost)
			p.Revenue = round2(p.Revenue)
			p.Margin = round2(p.Revenue - p.Cost)
			p.MarginPct = marginPct(p.Revenue, p.Cost)
			cl.Cost += p.Cost
			cl.Revenue += p.Revenue
		}
		cl.Cost = round2(cl.Cost)
		cl.Revenue = round2(cl.Revenue)
		cl.Margin = round2(cl.Revenue - cl.Cost)
		cl.MarginPct = marginPct(cl.Revenue, cl.Cost)
		sort.Slice(cl.Projects, func(a, b int) bool { return cl.Projects[a].Cost > cl.Projects[b].Cost })
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

	out.Available = true
	out.HardwareSource = hardwareSource
	out.HardwareTotal = hardwareTotal
	out.HardwareBreakdown = hardwareBreakdown
	out.RawTotal = rawTotal
	out.Scale = scale
	out.TotalCost = totalCost
	out.TotalRevenue = totalRevenue
	out.Unallocated = unallocated
	out.TopLossMakers = lossMakers
	out.Clients = clients
	return out
}

// ensureResource returns a live pointer to the client -> project -> resource
// node, creating each level on first sight. It re-derives the pointer from the
// current Resources backing array on every call via an append-stable position
// index (resources[client][project][name] = slot), so callers must use the
// returned pointer immediately and never cache it across another ensureResource
// call: a later append can reallocate the backing array and invalidate it.
func (acc *adminCostsAccumulator) ensureResource(clientID, clientName, projectID, projectName, resourceName, kind string) *adminCostResource {
	cl, ok := acc.clients[clientID]
	if !ok {
		cl = &adminCostClient{ClientID: clientID, ClientName: clientName, Projects: []adminCostProject{}}
		acc.clients[clientID] = cl
		acc.resources[clientID] = map[string]map[string]int{}
	}

	projIdx, ok := acc.resources[clientID][projectID]
	if !ok {
		cl.Projects = append(cl.Projects, adminCostProject{
			ProjectID: projectID, ProjectName: projectName, Resources: []adminCostResource{},
		})
		projIdx = map[string]int{}
		acc.resources[clientID][projectID] = projIdx
	}

	pi := adminCostProjectIndex(cl, projectID)
	ri, ok := projIdx[resourceName]
	if !ok {
		cl.Projects[pi].Resources = append(cl.Projects[pi].Resources, adminCostResource{Name: resourceName, Kind: kind})
		ri = len(cl.Projects[pi].Resources) - 1
		projIdx[resourceName] = ri
	}
	return &cl.Projects[pi].Resources[ri]
}

// add records one pod allocation's scaled cost under client -> project ->
// resource, creating each level on first sight.
func (acc *adminCostsAccumulator) add(clientID, clientName, projectID, projectName, resourceName, kind string, a opencost.Allocation, scale float64) {
	r := acc.ensureResource(clientID, clientName, projectID, projectName, resourceName, kind)
	r.CPUCost += round2(nonNeg(a.CPUCost) * scale)
	r.RAMCost += round2(nonNeg(a.RAMCost) * scale)
	r.PVCost += round2(nonNeg(a.PVCost) * scale)
	r.TotalCost += round2(nonNeg(a.TotalCost) * scale)
}

// marginPct returns profit as a percentage of revenue, rounded to 2 dp. Zero
// when revenue is non-positive: with no charge there is no defined margin
// ratio, and returning 0 avoids a divide-by-zero while keeping cost-only rows
// at 0% instead of negative infinity.
func marginPct(revenue, cost float64) float64 {
	if revenue <= 0 {
		return 0
	}
	return round2((revenue - cost) / revenue * 100)
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
	bctx, cancel := withCostBudget(ctx)
	defer cancel()
	return cache.Fetch(bctx, h.cache,
		"cost:admin:pod:"+window, h.cfg.CacheCostTTL,
		func() (map[string]opencost.Allocation, error) {
			return h.opencost.Compute(bctx, window, "pod", "")
		})
}

// adminCostOwner is one project's ownership info for the cost drilldown.
type adminCostOwner struct {
	projectID, projectName string
	ownerID, ownerName     string
}

// adminCostNamespaceOwners maps every project namespace to its project and
// owning user, plus the owner join the cost drilldown needs to group
// projects into clients.
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
	Cluster       string  `json:"cluster"`
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

// begetHardwareCost fetches the live price_month total across every Beget
// managed-Kubernetes cluster selected by BegetK8SClusterSlug (24h cache --
// Beget's own pricing changes on the timescale of tariff updates, not
// requests), summed into one hardware bill. The platform runs on more than
// one Beget cluster (prod console cluster + the separate ArgoCD mgmt
// cluster), so an empty slug selects all of them by default. Returns
// ok=false on any failure (nil client, network error, no cluster matched)
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
	selected := beget.SelectClusters(clusters, h.cfg.BegetK8SClusterSlug)
	if len(selected) == 0 {
		log.Warn().Str("slug", h.cfg.BegetK8SClusterSlug).Msg("admin costs: beget token cannot see any configured cluster slug, falling back")
		return 0, nil, false
	}
	var total float64
	breakdown := make([]adminCostHardwareGroup, 0, len(selected)*2)
	for _, cl := range selected {
		total += cl.TotalMonthlyRUB()
		breakdown = append(breakdown, adminCostHardwareGroup{
			Name: "control plane", Cluster: cl.Slug, NodeCount: cl.MasterNodeCount, PriceMonthRUB: round2(cl.MasterPriceRUB),
		})
		for _, wg := range cl.WorkerGroups {
			breakdown = append(breakdown, adminCostHardwareGroup{
				Name: wg.DisplayName, Cluster: cl.Slug, NodeCount: wg.NodeCount, PriceMonthRUB: round2(wg.PriceMonth),
			})
		}
	}
	return total, breakdown, true
}
