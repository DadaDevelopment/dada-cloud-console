package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
}

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
func (h *Handler) computeProjectConsumption(ctx context.Context, projectID uuid.UUID) (projectConsumption, error) {
	now := time.Now().UTC()
	start := monthStart(now)

	out := projectConsumption{
		PeriodStart: start.Format(time.RFC3339),
		PeriodEnd:   now.Format(time.RFC3339),
		Resources:   []consumptionResource{},
	}

	apps, err := h.consumptionApps(ctx, projectID, start, now)
	if err != nil {
		return projectConsumption{}, err
	}
	out.Resources = append(out.Resources, apps...)

	dbs, err := h.consumptionDatabases(ctx, projectID)
	if err != nil {
		return projectConsumption{}, err
	}
	out.Resources = append(out.Resources, dbs...)

	var total float64
	for _, r := range out.Resources {
		total += r.CostRub
	}
	out.TotalRub = round2(total)
	return out, nil
}

// opencostAppCosts returns each app's real infra cost (raw RUB, no markup) over
// the window, sourced from the OpenCost Allocation API and keyed by
// "namespace/appName". Apps are identified by the dada.io/app pod label (stamped
// by project-defaults), so the key's app segment is the exact console app name.
// Best-effort: returns an empty map when OpenCost is unset or the query fails, so
// callers transparently fall back to the metrics-derived estimate.
func (h *Handler) opencostAppCosts(ctx context.Context, projectID uuid.UUID, start, end time.Time) map[string]float64 {
	out := map[string]float64{}
	if h.opencost == nil {
		return out
	}
	nsToEnv, err := h.projectNamespaces(ctx, projectID)
	if err != nil || len(nsToEnv) == 0 {
		return out
	}
	quoted := make([]string, 0, len(nsToEnv))
	for ns := range nsToEnv {
		quoted = append(quoted, `"`+ns+`"`)
	}
	filter := "namespace:" + strings.Join(quoted, ",")
	window := start.Format(time.RFC3339) + "," + end.Format(time.RFC3339)

	allocs, err := h.opencost.Compute(ctx, window, "namespace,label:dada_io_app", filter)
	if err != nil {
		log.Warn().Err(err).Str("project", projectID.String()).Msg("billing consumption: opencost app cost query failed")
		return out
	}
	for key, a := range allocs {
		out[key] = a.TotalCost
	}
	return out
}

// consumptionApps enumerates the project's App resources across its
// environments and estimates each app's average CPU (cores) and RAM (GB) over
// the period from Prometheus (shown for context). The cost is the app's real
// OpenCost allocation over the period (CPU+RAM+PV, priced at our tariffs), and
// falls back to the metrics-derived estimate only when OpenCost cannot attribute
// the app. Failures degrade to nil usage / 0 cost, never an error.
func (h *Handler) consumptionApps(ctx context.Context, projectID uuid.UUID, start, end time.Time) ([]consumptionResource, error) {
	ocCosts := h.opencostAppCosts(ctx, projectID, start, end)
	rows, err := h.pool.Query(ctx,
		`SELECT rs.name, e.runtime, e.namespace, COALESCE(rs.summary_json->>'image', '')
		   FROM resource_snapshots rs
		   JOIN environments e ON e.id = rs.environment_id
		  WHERE rs.project_id = $1 AND rs.kind = 'App'
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
		cpu, ram := h.appAvgUsage(ctx, a.runtime, a.namespace, a.image, a.name, start, end)
		res := consumptionResource{
			Kind:     "app",
			Name:     a.name,
			CPUCores: cpu,
			RAMGB:    ram,
		}
		if raw, ok := ocCosts[a.namespace+"/"+a.name]; ok {
			res.CostRub = round2(raw * h.billingMarkup)
		} else {
			res.CostRub = h.costRub(cpu, ram, nil)
		}
		out = append(out, res)
	}
	return out, nil
}

// appAvgUsage returns the average CPU (cores) and RAM (GB) for one app over the
// window, mirroring GetAppMetrics' PromQL: k8s apps scope by namespace+image,
// compose/VM apps by the docker-compose service label (== app name). Any
// missing/failed query yields nil for that dimension (0 cost, warn logged).
func (h *Handler) appAvgUsage(ctx context.Context, runtime, namespace, image, appName string, start, end time.Time) (*float64, *float64) {
	if h.prometheus == nil {
		return nil, nil
	}
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

// consumptionDatabases enumerates the project's managed databases and sources
// each database's on-disk size (pg_database_size_bytes, keyed by the CR's
// spec.database == datname). CPU/RAM are null for databases. Size lookups are
// best-effort: a missing series yields nil storage (0 cost).
func (h *Handler) consumptionDatabases(ctx context.Context, projectID uuid.UUID) ([]consumptionResource, error) {
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

	out := make([]consumptionResource, 0, len(dbRows))
	for _, d := range dbRows {
		var storageGB *float64
		if d.datname != "" {
			if bytesSize, ok := sizeByDatname[d.datname]; ok {
				g := bytesSize / (1024 * 1024 * 1024)
				storageGB = &g
			}
		}
		res := consumptionResource{
			Kind:      "database",
			Name:      d.name,
			StorageGB: storageGB,
		}
		res.CostRub = h.costRub(nil, nil, storageGB)
		out = append(out, res)
	}
	return out, nil
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

// dbSizesByDatname returns the live pg_database_size_bytes keyed by datname.
// Best-effort: returns an empty map when Prometheus is unset or the query fails.
func (h *Handler) dbSizesByDatname(ctx context.Context) map[string]float64 {
	out := map[string]float64{}
	if h.prometheus == nil {
		return out
	}
	samples, err := h.prometheus.QueryInstant(ctx, "pg_database_size_bytes", time.Time{}, "")
	if err != nil {
		log.Warn().Err(err).Msg("billing consumption: pg_database_size query failed")
		return out
	}
	for _, s := range samples {
		if dn := s.Metric["datname"]; dn != "" {
			out[dn] = s.Point.V
		}
	}
	return out
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
	pc, err := h.computeProjectConsumption(c.Request.Context(), projectID)
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
// @Description Returns the org's plan and current-month informational spend, summed across every project the caller can read. Money-equivalent estimate at our tariffs — NOT a bill. balance_rub is always 0 (payments not built yet). Robust: metric gaps contribute 0.
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
	if org := h.callerOrg(claims); org != "" {
		if plan, err := h.planFor(ctx, org); err == nil && plan.Name != "" {
			planName = plan.Name
		}
	}

	projectIDs, err := h.readableProjectIDs(ctx, claims)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list projects")
		return
	}

	var spend float64
	for _, pid := range projectIDs {
		pc, err := h.computeProjectConsumption(ctx, pid)
		if err != nil {
			log.Warn().Err(err).Str("project", pid.String()).Msg("billing account summary: project consumption failed")
			continue
		}
		spend += pc.TotalRub
	}

	c.JSON(http.StatusOK, gin.H{
		"currency":         "RUB",
		"plan":             planName,
		"period_spend_rub": round2(spend),
		// balance_rub is hardcoded 0: YooKassa-backed prepaid balance lands later.
		"balance_rub": 0,
	})
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
