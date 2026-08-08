package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/cache"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// consumptionResource is one line item in the informational consumption
// breakdown. cpu_cores / ram_gb / storage_gb are pointers so a dimension the
// resource does not expose serializes as JSON null (contributing 0 to cost),
// distinct from a real measured 0.
type consumptionResource struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	CPUCores  *float64 `json:"cpu_cores"`
	RAMGB     *float64 `json:"ram_gb"`
	StorageGB *float64 `json:"storage_gb"`
	CostRub   float64  `json:"cost_rub"`
	Basis     string   `json:"basis"`
}

const (
	basisActual   = "actual"
	basisEstimate = "estimate"
)

// accountSummaryBudget is the hard wall-clock cap GetAccountSummary spends
// summing consumption across every project the caller can read. A god caller
// (platform-admins) can read every project on the platform, so this loops
// over N projects instead of one; costRequestBudget (cost.go, 2s) is sized
// for a single OpenCost read and is too tight here. Projects not reached
// within the budget are skipped and the response is marked partial rather
// than the request hanging.
const accountSummaryBudget = 5 * time.Second

// accountSummaryPerProjectBudget caps how long GetAccountSummary's loop will
// wait on any single project's consumption lookup. Defense in depth on top of
// accountSummaryBudget: with billingSnapshot now serving from cache only (see
// billing_fullcost.go), a project's own work is a few fast DB/Redis round
// trips and should never approach this, but nothing about a single pathological
// project (locked row, dead connection, whatever) should be able to consume
// the entire shared accountSummaryBudget and starve every other project in
// the same request the way one previously could.
const accountSummaryPerProjectBudget = 800 * time.Millisecond

// projectConsumption is the money-equivalent estimate for one project over the
// current calendar month. Informational only ("оценка по нашим тарифам"), never
// a bill.
type projectConsumption struct {
	PeriodStart string
	PeriodEnd   string
	TotalRub    float64
	Resources   []consumptionResource
}

// round2 rounds a rub amount to two decimal places.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// costRub applies the unit costs + markup to a resource's usage. A nil term
// contributes 0. Result is rounded to two decimals.
func (h *Handler) costRub(cpuCores, ramGB, storageGB *float64) float64 {
	var total float64
	if cpuCores != nil {
		total += *cpuCores * h.billingUnit.PerVCPU
	}
	if ramGB != nil {
		total += *ramGB * h.billingUnit.PerGBRAM
	}
	if storageGB != nil {
		total += *storageGB * h.billingUnit.PerGBStorage
	}
	return round2(total * h.billingMarkup)
}

// monthStart returns the first instant of the current UTC calendar month.
func monthStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// computeProjectConsumption assembles the informational money-equivalent
// consumption for one project over the current calendar month. It NEVER returns
// an error for missing/failed metrics — a resource whose usage cannot be sourced
// simply contributes 0 (logged at warn). The only errors are hard DB failures on
// the resource enumeration itself.
func (h *Handler) computeProjectConsumption(ctx context.Context, projectID uuid.UUID, withUsage bool) (projectConsumption, error) {
	now := time.Now().UTC()
	start := monthStart(now)

	out := projectConsumption{
		PeriodStart: start.Format(time.RFC3339),
		PeriodEnd:   now.Format(time.RFC3339),
		Resources:   []consumptionResource{},
	}

	snap := h.billingSnapshot()

	apps, err := h.consumptionApps(ctx, projectID, start, now, snap, withUsage)
	if err != nil {
		return projectConsumption{}, err
	}
	out.Resources = append(out.Resources, apps...)

	dbs, err := h.consumptionDatabases(ctx, projectID, snap)
	if err != nil {
		return projectConsumption{}, err
	}
	out.Resources = append(out.Resources, dbs...)

	dns := h.consumptionDNS(ctx, projectID, snap)
	out.Resources = append(out.Resources, dns...)

	boxes, err := h.consumptionBoxes(ctx, projectID, start, now)
	if err != nil {
		return projectConsumption{}, err
	}
	out.Resources = append(out.Resources, boxes...)

	var total float64
	for _, r := range out.Resources {
		total += r.CostRub
	}
	out.TotalRub = round2(total)
	return out, nil
}

// consumptionApps enumerates the project's App resources across its
// non-ephemeral environments and estimates each app's average CPU (cores) and
// RAM (GB) over the period from Prometheus (shown for context). The cost is the
// app's real OpenCost allocation from the cached cluster snapshot (CPU+RAM+PV,
// priced at our tariffs), and falls back to the metrics-derived estimate only
// when OpenCost cannot attribute the app. Failures degrade to nil usage / 0 cost,
// never an error.
//
// Ephemeral (PR-preview) environments are excluded: previews are a feature the
// platform gives for free, so an ignored PR that spawns pr-6/pr-7 copies of an
// app must not multiply the customer's own consumption estimate. Their cost is
// carried in the platform own-infrastructure bucket instead (see adminCostOwnerOf).
//
// Box environments (runtime='box') are excluded for a different reason: a box is
// metered per active minute by its own path, so anything running INSIDE a box
// that happens to leave an App snapshot behind would be counted twice — once as a
// box-minute and once as a monthly app estimate. The exclusion is here from the
// moment 'box' became a possible runtime rather than added later with the meter,
// because the double count would have been silent and would have overstated a
// customer's consumption.
func (h *Handler) consumptionApps(ctx context.Context, projectID uuid.UUID, start, end time.Time, snap *billingCostSnapshot, withUsage bool) ([]consumptionResource, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT rs.name, e.runtime, e.namespace, COALESCE(rs.summary_json->>'image', '')
		   FROM resource_snapshots rs
		   JOIN environments e ON e.id = rs.environment_id
		  WHERE rs.project_id = $1 AND rs.kind = 'App' AND NOT e.is_ephemeral
		    AND e.runtime <> 'box' AND `+notOrphanedSnapshot+`
		  ORDER BY rs.name`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type appRow struct {
		name      string
		runtime   string
		namespace string
		image     string
	}
	var appRows []appRow
	for rows.Next() {
		var a appRow
		if err := rows.Scan(&a.name, &a.runtime, &a.namespace, &a.image); err != nil {
			return nil, err
		}
		appRows = append(appRows, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]consumptionResource, 0, len(appRows))
	for _, a := range appRows {
		res := consumptionResource{
			Kind: "app",
			Name: a.name,
		}
		if withUsage {
			res.CPUCores, res.RAMGB = h.appAvgUsage(ctx, a.runtime, a.namespace, a.image, a.name, start, end)
		}
		if oc, ok := snap.appCost[a.namespace+"/"+a.name]; ok {
			res.CostRub = snap.pricing.price(oc.CPUCost, oc.RAMCost, oc.PVCost)
		}
		res.Basis = basisActual
		out = append(out, res)
	}
	return out, nil
}

// consumptionBoxes prices the project's boxes from the per-minute ledger.
//
// Basis is ALWAYS basisActual, with no estimate fallback anywhere in this function,
// and that is the substantive difference from every other consumption source in this
// file. Apps fall back to a metrics-derived estimate when OpenCost cannot attribute
// them; databases fall back to estimateFootprintDB. A box needs neither, because
// box_usage is not a model of what a box probably consumed — it is a row per minute
// the box was actually billed for, written at the time, with the price frozen into
// it (migration 063). There is nothing to estimate and therefore nothing that could
// silently become an estimate.
//
// The two consequences are worth stating because they look like bugs and are not:
//
//   - A box with no rows in the window contributes NOTHING, not a zero-cost line.
//     An idle box writes no rows at all, so the honest rendering of "this box cost
//     you nothing" is its absence from the breakdown, matching the ledger exactly.
//   - The cpu/ram/storage columns are the sums of what was BILLED, i.e. footprint
//     x minutes, not an average utilisation. They are the dimensions the money was
//     derived from, which is the only reading under which the numbers add up.
//
// Boxes are NOT double counted against consumptionApps: that query already carries
// `AND e.runtime <> 'box'` (added when 'box' became a possible runtime, precisely so
// this could never overlap), so an App snapshot left behind by something running
// inside a box is excluded from the monthly estimator. Verified rather than
// re-added — a second copy of that predicate would be the same bug in a new place.
func (h *Handler) consumptionBoxes(ctx context.Context, projectID uuid.UUID, start, end time.Time) ([]consumptionResource, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT b.name,
		        SUM(u.vcpu)       AS vcpu_minutes,
		        SUM(u.ram_gb)     AS ram_minutes,
		        SUM(u.storage_gb) AS storage_minutes,
		        SUM(u.cost_rub)   AS cost_rub,
		        COUNT(*) FILTER (WHERE u.kind = $4) AS active_minutes
		   FROM box_usage u
		   JOIN boxes b ON b.id = u.box_id
		  WHERE b.project_id = $1 AND u.minute_start >= $2 AND u.minute_start < $3
		  GROUP BY b.name
		  ORDER BY b.name`,
		projectID, start, end, boxUsageKindActive,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []consumptionResource
	for rows.Next() {
		var name string
		var vcpuMinutes, ramMinutes, storageMinutes, cost float64
		var activeMinutes int
		if err := rows.Scan(&name, &vcpuMinutes, &ramMinutes, &storageMinutes, &cost, &activeMinutes); err != nil {
			return nil, err
		}
		// Reported as the average over the billed minutes, so the three dimensions
		// read in the same units as every other line in this breakdown (cores, GB)
		// instead of in core-minutes, which nothing else here uses.
		minutes := float64(activeMinutes)
		cpu, ram, storage := vcpuMinutes, ramMinutes, storageMinutes
		if minutes > 0 {
			cpu, ram, storage = vcpuMinutes/minutes, ramMinutes/minutes, storageMinutes/minutes
		}
		out = append(out, consumptionResource{
			Kind:      "box",
			Name:      name,
			CPUCores:  &cpu,
			RAMGB:     &ram,
			StorageGB: &storage,
			CostRub:   round2(cost),
			Basis:     basisActual,
		})
	}
	return out, rows.Err()
}

// appUsage is the cached per-app usage average (nil dimensions preserved).
type appUsage struct {
	CPU *float64 `json:"cpu"`
	RAM *float64 `json:"ram"`
}

// appAvgUsage returns the average CPU (cores) and RAM (GB) for one app over the
// window. It caches the reusable unit -- the two Prometheus subqueries -- not
// the endpoint, keyed by (billing month, runtime, namespace, image, app). The
// key deliberately omits the exact window end (which moves every second) and
// uses the billing month instead, so repeat views within CacheCostTTL reuse one
// result instead of each firing a fresh pair of avg_over_time subqueries. A
// monthly average barely moves over the TTL, so the staleness is immaterial.
func (h *Handler) appAvgUsage(ctx context.Context, runtime, namespace, image, appName string, start, end time.Time) (*float64, *float64) {
	if h.prometheus == nil {
		return nil, nil
	}
	key := fmt.Sprintf("usage:%s:%s:%s:%s:%s", start.Format("2006-01"), runtime, namespace, image, appName)
	u, _ := cache.Fetch(ctx, h.cache, key, h.cfg.CacheCostTTL, func() (appUsage, error) {
		cpu, ram := h.computeAppAvgUsage(ctx, runtime, namespace, image, appName, start, end)
		return appUsage{CPU: cpu, RAM: ram}, nil
	})
	return u.CPU, u.RAM
}

// computeAppAvgUsage runs the two Prometheus subqueries behind appAvgUsage,
// mirroring GetAppMetrics' PromQL: k8s apps scope by namespace+image, compose/VM
// apps by the docker-compose service label (== app name). Any missing/failed
// query yields nil for that dimension (0 cost, warn logged).
func (h *Handler) computeAppAvgUsage(ctx context.Context, runtime, namespace, image, appName string, start, end time.Time) (*float64, *float64) {
	window := fmt.Sprintf("%ds", int(end.Sub(start).Seconds()))
	var cpuExpr, ramExpr string
	if runtime == "k8s" && namespace != "" && image != "" {
		ns := prometheus.EscapeLabelValue(namespace)
		img := prometheus.EscapeLabelValue(image)
		cpuExpr = fmt.Sprintf(`avg_over_time((sum(rate(container_cpu_usage_seconds_total{namespace="%s",image="%s",container!=""}[5m])))[%s:5m])`, ns, img, window)
		ramExpr = fmt.Sprintf(`avg_over_time((sum(container_memory_working_set_bytes{namespace="%s",image="%s",container!=""}))[%s:5m])`, ns, img, window)
	} else {
		svc := prometheus.EscapeLabelValue(appName)
		cpuExpr = fmt.Sprintf(`avg_over_time((sum(rate(container_cpu_usage_seconds_total{container_label_com_docker_compose_service="%s"}[5m])))[%s:5m])`, svc, window)
		ramExpr = fmt.Sprintf(`avg_over_time((sum(container_memory_working_set_bytes{container_label_com_docker_compose_service="%s"}))[%s:5m])`, svc, window)
	}

	cpu := h.instantScalar(ctx, cpuExpr, end, appName, "cpu")
	ramBytes := h.instantScalar(ctx, ramExpr, end, appName, "ram")
	var ramGB *float64
	if ramBytes != nil {
		g := *ramBytes / (1024 * 1024 * 1024)
		ramGB = &g
	}
	return cpu, ramGB
}

// instantScalar runs an instant query expected to return a single scalar-ish
// sample and returns its value, or nil on error / no series. Best-effort: warns
// on query failure and returns nil so the endpoint still succeeds.
func (h *Handler) instantScalar(ctx context.Context, expr string, ts time.Time, appName, dim string) *float64 {
	samples, err := h.prometheus.QueryInstant(ctx, expr, ts, "")
	if err != nil {
		log.Warn().Err(err).Str("app", appName).Str("dim", dim).Msg("billing consumption: metric query failed")
		return nil
	}
	if len(samples) == 0 {
		return nil
	}
	v := samples[0].Point.V
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// consumptionDatabases enumerates the project's managed databases and prices
// each one as its share of the SHARED Postgres pod. Every managed DB lives in
// one postgresql-* pod, so OpenCost cannot split compute per-DB; instead the
// pod's real cost (CPU+RAM+PV, from OpenCost) is divided across all databases in
// proportion to their on-disk size (pg_database_size_bytes), then priced through
// the same per-type overhead + margin as apps. Best-effort: when the pod cost or
// sizes are unavailable it falls back to the storage-only estimate.
func (h *Handler) consumptionDatabases(ctx context.Context, projectID uuid.UUID, snap *billingCostSnapshot) ([]consumptionResource, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT name, summary_json
		   FROM resource_snapshots
		  WHERE project_id = $1 AND kind = 'ServiceDatabaseV2'
		  ORDER BY name`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type dbRow struct {
		name    string
		datname string
	}
	var dbRows []dbRow
	for rows.Next() {
		var name string
		var summaryJSON []byte
		if err := rows.Scan(&name, &summaryJSON); err != nil {
			return nil, err
		}
		datname := datnameFromSummary(summaryJSON)
		dbRows = append(dbRows, dbRow{name: name, datname: datname})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sizeByDatname := h.dbSizesByDatname(ctx)
	var totalBytes float64
	for _, b := range sizeByDatname {
		totalBytes += b
	}
	podCost := snap.postgres

	out := make([]consumptionResource, 0, len(dbRows))
	for _, d := range dbRows {
		var storageGB *float64
		var dbBytes float64
		if d.datname != "" {
			if b, ok := sizeByDatname[d.datname]; ok {
				g := b / (1024 * 1024 * 1024)
				storageGB = &g
				dbBytes = b
			}
		}
		res := consumptionResource{
			Kind:      "database",
			Name:      d.name,
			StorageGB: storageGB,
		}
		if totalBytes > 0 && dbBytes > 0 && podCost.TotalCost > 0 {
			frac := dbBytes / totalBytes
			res.CostRub = snap.pricing.price(podCost.CPUCost*frac, podCost.RAMCost*frac, podCost.PVCost*frac)
			res.Basis = basisActual
		} else {
			res.CostRub = h.estimateCost(estimateFootprintDB, snap.pricing)
			res.Basis = basisEstimate
		}
		out = append(out, res)
	}
	return out, nil
}

// consumptionDNS prices the project's managed DNS zones as their share of the
// shared PowerDNS pod (the two nameservers ns1/ns2). PowerDNS serves every
// delegated zone from one pod, so its real OpenCost cost is split evenly across
// all active managed zones cluster-wide; the project pays for the zones it owns,
// priced through the same per-type overhead + margin. One line item per zone
// (apex) for transparency. Best-effort: returns nil on any failure.
func (h *Handler) consumptionDNS(ctx context.Context, projectID uuid.UUID, snap *billingCostSnapshot) []consumptionResource {
	rows, err := h.pool.Query(ctx,
		`SELECT mz.apex FROM managed_zones mz
		   JOIN domain_authorizations da ON da.id = mz.authorization_id
		  WHERE da.project_id = $1 AND mz.status = 'active'
		  ORDER BY mz.apex`,
		projectID,
	)
	if err != nil {
		log.Warn().Err(err).Str("project", projectID.String()).Msg("billing consumption: managed zones query failed")
		return nil
	}
	defer rows.Close()
	var apexes []string
	for rows.Next() {
		var apex string
		if err := rows.Scan(&apex); err != nil {
			return nil
		}
		apexes = append(apexes, apex)
	}
	if len(apexes) == 0 {
		return nil
	}

	var totalZones int
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM managed_zones WHERE status = 'active'`).Scan(&totalZones); err != nil || totalZones == 0 {
		return nil
	}

	dns := snap.powerdns
	share := 1.0 / float64(totalZones)
	basis := basisActual
	if dns.CPUCost+dns.RAMCost+dns.PVCost <= 0 {
		basis = basisEstimate
	}

	out := make([]consumptionResource, 0, len(apexes))
	for _, apex := range apexes {
		out = append(out, consumptionResource{
			Kind:    "dns",
			Name:    apex,
			CostRub: snap.pricing.price(dns.CPUCost*share, dns.RAMCost*share, dns.PVCost*share),
			Basis:   basis,
		})
	}
	return out
}

// datnameFromSummary extracts spec.database (the Postgres datname) from a
// ServiceDatabaseV2 snapshot summary. Returns "" when absent/unparseable.
func datnameFromSummary(summaryJSON []byte) string {
	if len(summaryJSON) == 0 {
		return ""
	}
	var summary map[string]any
	if err := json.Unmarshal(summaryJSON, &summary); err != nil {
		return ""
	}
	spec, ok := summary["spec"].(map[string]any)
	if !ok {
		return ""
	}
	datname, _ := spec["database"].(string)
	return datname
}

// projectConsumptionCacheTTL bounds how long a project's no-usage consumption
// total is reused before recompute. Shares billingSnapshotTTL's class: the
// figure only moves as fast as the underlying OpenCost snapshot it is priced
// from, so a shorter TTL would just recompute the same number.
const projectConsumptionCacheTTL = billingSnapshotTTL

// projectConsumptionCacheKey is the cache-aside key for one project's
// no-usage consumption total (the variant summed by GetAccountSummary and
// pre-populated by the background warmer).
func projectConsumptionCacheKey(projectID uuid.UUID) string {
	return "billing:consumption:" + projectID.String()
}

// projectConsumptionCached serves the no-usage consumption total for one
// project from the cache-aside layer, falling through to
// computeProjectConsumption on a miss. GetAccountSummary calls this instead
// of computeProjectConsumption directly so a god caller summing every project
// on the platform reads N warm cache hits instead of recomputing N times per
// request; the background warmer (warmProjectConsumptions) keeps every
// project's entry populated so misses are rare.
func (h *Handler) projectConsumptionCached(ctx context.Context, projectID uuid.UUID) (projectConsumption, error) {
	return cache.Fetch(ctx, h.cache, projectConsumptionCacheKey(projectID), projectConsumptionCacheTTL,
		func() (projectConsumption, error) { return h.computeProjectConsumption(ctx, projectID, false) })
}

// warmProjectConsumptions proactively recomputes and stores the no-usage
// consumption total for every project on the platform, called from the
// background cost-cache warmer (cost.go). billingSnapshot now serves from
// memory only (see billing_fullcost.go) and never blocks on OpenCost, so a
// project's remaining work here is a handful of fast DB/Redis round trips; the
// per-project deadline is sized for that, not for tolerating a live upstream
// call. It returns per-tick counts and the IDs that failed so the caller
// (cost.go) can log which specific projects did not warm.
func (h *Handler) warmProjectConsumptions(ctx context.Context) (ok, failed int, failedProjectIDs []string) {
	ids, err := h.allProjectIDs(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("cost warmer: list projects for consumption warm failed")
		return 0, 0, nil
	}
	for _, pid := range ids {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		pc, err := h.computeProjectConsumption(pctx, pid, false)
		cancel()
		if err != nil {
			failed++
			failedProjectIDs = append(failedProjectIDs, pid.String())
			log.Warn().Err(err).Str("project", pid.String()).Msg("cost warmer: project consumption warm failed")
			continue
		}
		cache.Store(ctx, h.cache, projectConsumptionCacheKey(pid), projectConsumptionCacheTTL, pc)
		ok++
	}
	return ok, failed, failedProjectIDs
}

// allProjectIDs returns every project UUID on the platform, for the
// background warmer to iterate (unlike readableProjectIDs, no caller scoping:
// the warmer pre-populates the shared cache every request reads from,
// regardless of which caller ends up summing which subset).
func (h *Handler) allProjectIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := h.pool.Query(ctx, `SELECT id FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// dbSizesByDatname returns the live pg_database_size_bytes keyed by datname.
// Best-effort: returns an empty map when Prometheus is unset or the query fails.
func (h *Handler) dbSizesByDatname(ctx context.Context) map[string]float64 {
	if h.prometheus == nil {
		return map[string]float64{}
	}
	out, _ := cache.Fetch(ctx, h.cache, "billing:dbsizes", h.cfg.CacheCostTTL,
		func() (map[string]float64, error) { return h.queryDBSizesByDatname(ctx) })
	return out
}

func (h *Handler) queryDBSizesByDatname(ctx context.Context) (map[string]float64, error) {
	out := map[string]float64{}
	samples, err := h.prometheus.QueryInstant(ctx, "pg_database_size_bytes", time.Time{}, "")
	if err != nil {
		log.Warn().Err(err).Msg("billing consumption: pg_database_size query failed")
		return out, nil
	}
	for _, s := range samples {
		if dn := s.Metric["datname"]; dn != "" {
			out[dn] = s.Point.V
		}
	}
	return out, nil
}

// consumptionJSON renders a projectConsumption as the frozen response contract.
func consumptionJSON(pc projectConsumption) gin.H {
	resources := make([]gin.H, 0, len(pc.Resources))
	for _, r := range pc.Resources {
		resources = append(resources, gin.H{
			"kind":       r.Kind,
			"name":       r.Name,
			"cpu_cores":  r.CPUCores,
			"ram_gb":     r.RAMGB,
			"storage_gb": r.StorageGB,
			"cost_rub":   r.CostRub,
			"basis":      r.Basis,
		})
	}
	return gin.H{
		"period": gin.H{
			"start": pc.PeriodStart,
			"end":   pc.PeriodEnd,
		},
		"currency":  "RUB",
		"total_rub": pc.TotalRub,
		"resources": resources,
	}
}

// GetProjectConsumption returns the informational real-consumption +
// money-equivalent estimate for a project over the current calendar month.
//
// @ID          getProjectConsumption
// @Summary     Get project consumption (informational money-equivalent)
// @Description Returns real resource consumption (per-app avg CPU/RAM from Prometheus, per-database on-disk size) for the current calendar month, priced at our tariffs. Informational transparency ("оценка по нашим тарифам") — NOT a bill. Always available to any project member (viewer+). Robust: missing metrics contribute 0, the endpoint still returns 200.
// @Tags        billing
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "period, currency, total_rub and per-resource breakdown"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/billing/consumption [get]
func (h *Handler) GetProjectConsumption(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	if !h.requireProjectMember(c, projectID) {
		return
	}
	pc, err := h.computeProjectConsumption(c.Request.Context(), projectID, true)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to compute consumption")
		return
	}
	c.JSON(http.StatusOK, consumptionJSON(pc))
}

// GetAccountSummary returns the caller's org-level informational spend summary
// for the current calendar month.
//
// @ID          getAccountSummary
// @Summary     Get account spend summary (informational)
// @Description Returns the org's plan, current-month informational spend and its plan-quota usage, summed across every project the caller can read. Money-equivalent estimate at our tariffs — NOT a bill. quotas/quota_grace_until are null for exempt orgs and when billing is disabled. Robust: metric gaps contribute 0.
// @Tags        billing
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{} "currency, plan, period_spend_rub and balance_rub"
// @Failure     401 {object} map[string]string
// @Failure     500 {object} map[string]string
// @Router      /billing/account/summary [get]
func (h *Handler) GetAccountSummary(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	ctx := c.Request.Context()

	planName := "free"
	var quotaUsage map[string]gin.H
	var graceUntil *time.Time
	exempt := false
	if org := h.callerOrg(claims); org != "" {
		if plan, err := h.planFor(ctx, org); err == nil {
			if plan.Name != "" {
				planName = plan.Name
			}
			quotaUsage, graceUntil, exempt = h.quotaSnapshot(ctx, org, plan)
		}
	}

	projectIDs, err := h.readableProjectIDs(ctx, claims)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list projects")
		return
	}

	budgetCtx, cancel := context.WithTimeout(ctx, accountSummaryBudget)
	defer cancel()

	var spend float64
	partial := false
	for _, pid := range projectIDs {
		if budgetCtx.Err() != nil {
			partial = true
			log.Warn().Int("total_projects", len(projectIDs)).Msg("billing account summary: budget exceeded, spend is partial")
			break
		}
		pctx, pcancel := context.WithTimeout(budgetCtx, accountSummaryPerProjectBudget)
		pc, err := h.projectConsumptionCached(pctx, pid)
		pcancel()
		if err != nil {
			partial = true
			log.Warn().Err(err).Str("project", pid.String()).Msg("billing account summary: project consumption failed, skipping")
			continue
		}
		spend += pc.TotalRub
	}

	c.JSON(http.StatusOK, gin.H{
		"currency":          "RUB",
		"plan":              planName,
		"period_spend_rub":  round2(spend),
		"balance_rub":       0,
		"partial":           partial,
		"quotas":            quotaUsage,
		"quota_grace_until": graceUntil,
		"quota_exempt":      exempt,
	})
}

// quotaSnapshot returns what the caller's org has used against what its plan
// allows, plus the grandfathering deadline after which those limits start
// blocking.
//
// It exists so the shell can show the free tier BEFORE the user runs into it.
// Until now the only surface that spoke about limits was the 403 raised at the
// moment of creation: a user learned their plan had a ceiling by hitting it,
// which is both a bad first impression and a bad way to sell an upgrade.
//
// Every failure degrades to nil rather than an error — this rides on the spend
// widget, which is deliberately non-load-bearing chrome, so a quota lookup must
// never be able to fail the summary request.
func (h *Handler) quotaSnapshot(ctx context.Context, orgID string, plan pricing.Plan) (map[string]gin.H, *time.Time, bool) {
	if !h.cfg.BillingEnabled || h.quotaExempt(orgID) {
		return nil, nil, h.quotaExempt(orgID)
	}
	usage, err := h.buildUsage(ctx, orgID, plan)
	if err != nil {
		log.Warn().Err(err).Str("org", orgID).Msg("billing account summary: quota usage skipped")
		return nil, nil, false
	}
	var graceUntil *time.Time
	if err := h.pool.QueryRow(ctx,
		`SELECT quota_grace_until FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&graceUntil); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Warn().Err(err).Str("org", orgID).Msg("billing account summary: grace lookup skipped")
	}
	if graceUntil != nil && !graceUntil.After(time.Now().UTC()) {
		graceUntil = nil
	}
	return usage, graceUntil, false
}

// callerOrg resolves the caller's primary org from claims for the plan lookup:
// the first org where they are Owner/Admin, else "". Mirrors how other
// org-scoped surfaces resolve an org from native claims.
func (h *Handler) callerOrg(claims *auth.Claims) string {
	if claims == nil {
		return ""
	}
	orgs := adminOrgIDs(claims)
	if len(orgs) > 0 {
		return orgs[0]
	}
	return ""
}

// readableProjectIDs returns every project UUID the caller can read: explicit
// project grants plus every project in an org where they are Owner/Admin, plus
// all projects for god. Mirrors ListProjects' visibility query.
func (h *Handler) readableProjectIDs(ctx context.Context, claims *auth.Claims) ([]uuid.UUID, error) {
	god := isGod(claims)
	explicitIDs := claimProjectIDs(claims)
	adminOrgs := adminOrgIDs(claims)

	rows, err := h.pool.Query(ctx,
		`SELECT id FROM projects
		  WHERE $1 OR id = ANY($2) OR org_id = ANY($3)`,
		god, explicitIDs, adminOrgs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
