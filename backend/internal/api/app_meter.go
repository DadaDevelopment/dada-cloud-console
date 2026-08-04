package api

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// The two kinds of row the app meter writes. See migration 101 for why there is
// no CHECK constraint pinning them in the schema.
const (
	appUsageKindPod    = "pod"
	appUsageKindVolume = "volume"
)

// appMeterStep is the resolution the meter integrates an hour at, and
// appMeterStepsPerHour is how many such samples a whole hour contains.
//
// The pair is the whole reason the meter uses QueryRange rather than a
// subquery: avg_over_time averages only the samples that EXIST, so an app that
// ran for ten minutes of the hour would average to its full footprint instead of
// a sixth of it, and the customer would be billed for an hour they did not have.
// Summing the returned points and dividing by the constant 12 counts every
// missing sample as the zero it actually was.
const (
	appMeterStep         = 5 * time.Minute
	appMeterStepsPerHour = 12
)

// appMeterMaxLagHours bounds how far back a single tick will look for hours it
// has not written yet. A pod that was down for a while comes back and fills in
// what it missed, up to this many hours; beyond that the metrics store (3 days
// local Prometheus) is the real limit anyway, and re-metering a week of history
// on every boot would be a lot of Prometheus load for rows that already exist.
const appMeterMaxLagHours = 6

// appMeterRetentionDays is how long raw hourly rows are kept. Thirty days is the
// owner's answer and it is also the shape of the product: overage is settled per
// calendar month, so a month of raw rows is exactly enough to defend a bill and
// no more. Rollups, if they are ever needed, are a separate table -- pruning is
// not the place to invent one.
const appMeterRetentionDays = 30

// appUsageKey identifies one app in the metrics store. The pair (namespace,
// app) is what the dada.io/app pod label plus its namespace give us, and it is
// resolved to environment/project/org through the environments table before
// anything is written -- which is also the filter that keeps the ledger to
// "тупо юзерские аппы": a namespace with no environment row (kube-system,
// longhorn-system, databases, monitoring) never becomes a ledger row.
type appUsageKey struct {
	namespace string
	app       string
}

// appUsageSample is one app's measured hour, before it is priced.
//
// held* is what the cluster reserved and could not sell to anyone else; used* is
// what the app actually burned. They are deliberately separate fields rather
// than one "usage" number: held is the billing basis, used is the margin and
// oversell signal, and collapsing them would silently make one of the two
// answers unavailable.
type appUsageSample struct {
	heldVCPU  float64
	heldRAMGB float64
	replicas  float64
	storageGB float64

	usedVCPU  *float64
	usedRAMGB *float64
}

// appMeterTarget is the tenancy an appUsageKey resolves to.
type appMeterTarget struct {
	envID     uuid.UUID
	projectID uuid.UUID
	orgID     string
}

// StartAppUsageMeter runs the hourly app usage meter until ctx is cancelled.
//
// It is deliberately NOT gated on BillingEnabled. The ledger is measurement, and
// measurement has to be running before billing is switched on -- a plan+overage
// model needs a month of history to set the included allowance against, and
// history is the one thing that cannot be produced retroactively. It is gated on
// the Prometheus client instead, because with no metrics there is nothing to
// measure and the loop would only write empty ticks.
//
// The loop meters CLOSED hours only, never the hour in progress. A partial hour
// would be written as a whole one, then overwritten by the same key later with a
// different number, and anything that read it in between would have read a bill
// that was quietly wrong rather than merely absent.
func (h *Handler) StartAppUsageMeter(ctx context.Context) {
	if h.prometheus == nil {
		log.Warn().Msg("app usage meter NOT started: no Prometheus client; per-app consumption will not be recorded")
		return
	}
	go func() {
		h.RunAppUsageMeterTick(ctx)
		for {
			timer := time.NewTimer(NextMeterDelay(time.Now(), time.Hour))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				h.RunAppUsageMeterTick(ctx)
			}
		}
	}()
	log.Info().Int("retention_days", appMeterRetentionDays).Msg("app usage meter started")
}

// RunAppUsageMeterTick meters every closed hour that has no rows yet, up to
// appMeterMaxLagHours back, then prunes past the retention horizon. Exported so
// the startup path and tests can drive one tick without the loop.
func (h *Handler) RunAppUsageMeterTick(ctx context.Context) {
	now := h.nowUTC()
	for i := appMeterMaxLagHours; i >= 1; i-- {
		hourStart := now.Truncate(time.Hour).Add(-time.Duration(i) * time.Hour)
		metered, err := h.appHourAlreadyMetered(ctx, hourStart)
		if err != nil {
			log.Warn().Err(err).Time("hour", hourStart).Msg("app usage meter: cannot tell whether hour is metered")
			continue
		}
		if metered {
			continue
		}
		if err := h.MeterAppHour(ctx, hourStart); err != nil {
			log.Warn().Err(err).Time("hour", hourStart).Msg("app usage meter: hour failed")
		}
	}
	h.pruneAppUsage(ctx, now)
}

// nowUTC is the meter's clock, overridable in tests through the handler's own
// now hook.
func (h *Handler) nowUTC() time.Time {
	if h.now != nil {
		return h.now().UTC()
	}
	return time.Now().UTC()
}

// appHourAlreadyMetered reports whether any row exists for the hour.
//
// It is a skip optimisation only: the upsert is idempotent, so a false negative
// costs a handful of Prometheus queries and nothing else. It is deliberately
// cluster-wide rather than per-app, which means an hour whose write partially
// failed will not be retried -- the honest trade for not re-running six range
// queries per tick for hours already on disk. Partial writes are logged at warn
// by upsertAppUsage, so the gap is visible rather than silent.
func (h *Handler) appHourAlreadyMetered(ctx context.Context, hourStart time.Time) (bool, error) {
	var exists bool
	err := h.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM app_usage WHERE hour_start = $1)`, hourStart).Scan(&exists)
	return exists, err
}

// MeterAppHour measures one closed hour and writes its rows. Missing metrics
// degrade to fewer rows, never to invented ones.
func (h *Handler) MeterAppHour(ctx context.Context, hourStart time.Time) error {
	targets, err := h.appMeterTargets(ctx)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	samples := h.measureAppHour(ctx, hourStart, appMeterNamespaces(targets))
	pricing := h.billingSnapshot().pricing
	hours := hoursInMonth(hourStart)

	var written int
	for key, s := range samples {
		target, ok := targets[key.namespace]
		if !ok {
			continue
		}
		if s.heldVCPU > 0 || s.heldRAMGB > 0 || s.replicas > 0 {
			cost := h.appUsageCostRub(s.heldVCPU, s.heldRAMGB, 0, pricing, hours)
			if h.upsertAppUsage(ctx, key, target, hourStart, appUsageKindPod, s.heldVCPU, s.heldRAMGB, 0, s.replicas, s.usedVCPU, s.usedRAMGB, cost) {
				written++
			}
		}
		if s.storageGB > 0 {
			cost := h.appUsageCostRub(0, 0, s.storageGB, pricing, hours)
			if h.upsertAppUsage(ctx, key, target, hourStart, appUsageKindVolume, 0, 0, s.storageGB, 0, nil, nil, cost) {
				written++
			}
		}
	}
	log.Info().Time("hour", hourStart).Int("rows", written).Int("apps", len(samples)).Msg("app usage meter: hour recorded")
	return nil
}

// appMeterTargets maps every k8s environment namespace to its tenancy. This map
// IS the definition of a user app for the ledger: metrics carry pods from the
// whole cluster, and only the ones landing in a namespace we know is a customer
// environment become billable rows.
func (h *Handler) appMeterTargets(ctx context.Context) (map[string]appMeterTarget, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT e.id, e.namespace, e.project_id, COALESCE(p.org_id, '')
		   FROM environments e
		   JOIN projects p ON p.id = e.project_id
		  WHERE e.runtime = 'k8s' AND e.namespace <> ''
		  ORDER BY e.namespace, e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]appMeterTarget{}
	for rows.Next() {
		var t appMeterTarget
		var ns string
		if err := rows.Scan(&t.envID, &ns, &t.projectID, &t.orgID); err != nil {
			return nil, err
		}
		if _, seen := out[ns]; seen {
			continue
		}
		out[ns] = t
	}
	return out, rows.Err()
}

// appMeterNamespaces returns the sorted namespace list for the PromQL matcher.
// Sorted so the generated query is stable and cacheable rather than reshuffled
// by Go's map iteration on every tick.
func appMeterNamespaces(targets map[string]appMeterTarget) []string {
	out := make([]string, 0, len(targets))
	for ns := range targets {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// appPodLabelJoin is the PromQL fragment that turns the dada.io/app pod label
// into a joinable "app" label. Everything the meter measures hangs off this one
// join, which is why it exists once instead of being retyped per query.
func appPodLabelJoin(namespaces []string) string {
	return fmt.Sprintf(`label_replace(kube_pod_labels{%s,label_dada_io_app!=""}, "app", "$1", "label_dada_io_app", "(.*)")`,
		appNamespaceMatcher(namespaces))
}

// appNamespaceMatcher builds namespace=~"a|b|c" from the known environment
// namespaces, each value escaped.
func appNamespaceMatcher(namespaces []string) string {
	escaped := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		escaped = append(escaped, prometheus.EscapeLabelValue(ns))
	}
	return fmt.Sprintf(`namespace=~"%s"`, strings.Join(escaped, "|"))
}

// appMeterExprs returns the five expressions the meter integrates over the hour,
// in the order (heldCPU, heldRAM, replicas, usedCPU, usedRAM). Storage is a
// sixth with a different join shape and is built separately.
//
// The running-phase factor on the first three is not decoration: a Pending pod
// already has resource requests and would otherwise be billed as if it were
// serving traffic, and a customer whose image fails to pull would receive a bill
// for the pod that never started.
func appMeterExprs(namespaces []string) [5]string {
	join := appPodLabelJoin(namespaces)
	ns := appNamespaceMatcher(namespaces)
	running := fmt.Sprintf(`(kube_pod_status_phase{%s,phase="Running"} == 1)`, ns)
	return [5]string{
		fmt.Sprintf(`sum by (namespace, app) (kube_pod_container_resource_requests{%s,resource="cpu"} * on (namespace, pod) group_left(app) %s * on (namespace, pod) group_left() %s)`, ns, join, running),
		fmt.Sprintf(`sum by (namespace, app) (kube_pod_container_resource_requests{%s,resource="memory"} * on (namespace, pod) group_left(app) %s * on (namespace, pod) group_left() %s) / 1073741824`, ns, join, running),
		fmt.Sprintf(`count by (namespace, app) (%s * on (namespace, pod) group_left(app) %s)`, running, join),
		fmt.Sprintf(`sum by (namespace, app) (rate(container_cpu_usage_seconds_total{%s,container!=""}[5m]) * on (namespace, pod) group_left(app) %s)`, ns, join),
		fmt.Sprintf(`sum by (namespace, app) (container_memory_working_set_bytes{%s,container!=""} * on (namespace, pod) group_left(app) %s) / 1073741824`, ns, join),
	}
}

// appMeterStorageExpr sums the PROVISIONED size of every PVC mounted by the
// app's pods, in GiB.
//
// Provisioned, not used: Longhorn reserves the whole volume whether the customer
// wrote a byte into it or not, and billing the filled fraction would mean the
// platform eats the difference on every half-empty disk. The inner max-by
// collapses the pod dimension first, so a two-replica app sharing one RWX claim
// is charged for one volume rather than two.
func appMeterStorageExpr(namespaces []string) string {
	join := appPodLabelJoin(namespaces)
	ns := appNamespaceMatcher(namespaces)
	claims := fmt.Sprintf(`max by (namespace, persistentvolumeclaim, app) (kube_pod_spec_volumes_persistentvolumeclaims_info{%s} * on (namespace, pod) group_left(app) %s)`, ns, join)
	return fmt.Sprintf(`sum by (namespace, app) (max by (namespace, persistentvolumeclaim, app) (kube_persistentvolumeclaim_resource_requests_storage_bytes{%s} * on (namespace, persistentvolumeclaim) group_left(app) (%s))) / 1073741824`, ns, claims)
}

// measureAppHour runs the six range queries and folds them into one sample per
// app. Any single query failing costs its own dimension and nothing else.
func (h *Handler) measureAppHour(ctx context.Context, hourStart time.Time, namespaces []string) map[appUsageKey]*appUsageSample {
	out := map[appUsageKey]*appUsageSample{}
	if len(namespaces) == 0 {
		return out
	}
	exprs := appMeterExprs(namespaces)

	held := h.appHourAverages(ctx, exprs[0], hourStart, "held_cpu")
	heldRAM := h.appHourAverages(ctx, exprs[1], hourStart, "held_ram")
	replicas := h.appHourAverages(ctx, exprs[2], hourStart, "replicas")
	usedCPU := h.appHourAverages(ctx, exprs[3], hourStart, "used_cpu")
	usedRAM := h.appHourAverages(ctx, exprs[4], hourStart, "used_ram")
	storage := h.appHourAverages(ctx, appMeterStorageExpr(namespaces), hourStart, "storage")

	at := func(key appUsageKey) *appUsageSample {
		if s, ok := out[key]; ok {
			return s
		}
		s := &appUsageSample{}
		out[key] = s
		return s
	}
	for key, v := range held {
		at(key).heldVCPU = v
	}
	for key, v := range heldRAM {
		at(key).heldRAMGB = v
	}
	for key, v := range replicas {
		at(key).replicas = v
	}
	for key, v := range storage {
		at(key).storageGB = v
	}
	for key, v := range usedCPU {
		value := v
		at(key).usedVCPU = &value
	}
	for key, v := range usedRAM {
		value := v
		at(key).usedRAMGB = &value
	}
	return out
}

// appHourAverages runs one range query across the hour and returns the
// time-weighted average per app. Failure is logged and returns nothing, so the
// dimension is absent rather than zero.
func (h *Handler) appHourAverages(ctx context.Context, expr string, hourStart time.Time, dim string) map[appUsageKey]float64 {
	end := hourStart.Add(time.Hour - appMeterStep)
	series, err := h.prometheus.QueryRange(ctx, expr, hourStart, end, appMeterStep, "")
	if err != nil {
		log.Warn().Err(err).Str("dim", dim).Time("hour", hourStart).Msg("app usage meter: range query failed")
		return nil
	}
	return foldHourSeries(series)
}

// foldHourSeries averages each series over the whole hour, counting samples the
// series does not have as zero (see appMeterStepsPerHour). Series the join
// failed to attribute to a (namespace, app) pair are dropped -- an unattributed
// footprint has no one to bill and must not be smeared over someone else.
//
// A measured zero is KEPT rather than dropped. It matters for the used_*
// columns, where migration 101 distinguishes "metrics had no answer" (NULL)
// from "the app burned nothing" (0), and an idle bot really does average to
// zero CPU. Rows with a zero billable footprint are suppressed one level up, at
// the write, which is the only place that decision belongs.
func foldHourSeries(series []prometheus.Series) map[appUsageKey]float64 {
	out := map[appUsageKey]float64{}
	for _, s := range series {
		key := appUsageKey{namespace: s.Metric["namespace"], app: s.Metric["app"]}
		if key.namespace == "" || key.app == "" {
			continue
		}
		var sum float64
		for _, p := range s.Points {
			sum += p.V
		}
		out[key] += sum / appMeterStepsPerHour
	}
	return out
}

// hoursInMonth returns the number of hours in the calendar month containing t.
// The hourly price is the monthly price divided by this, so a full month of
// rows sums back to exactly the monthly figure the console shows -- including
// February, which a hardcoded 720 would overcharge by 7%.
func hoursInMonth(t time.Time) float64 {
	start := monthStart(t.UTC())
	next := start.AddDate(0, 1, 0)
	return next.Sub(start).Hours()
}

// appUsageCostRub prices one hour of a footprint at the same unit costs,
// overhead factors and margin as every other consumption surface, then divides
// the monthly figure down to the hour. Sharing consumptionPricing is the point:
// the ledger and the console's monthly estimate must not be two independent
// pieces of arithmetic that drift apart.
func (h *Handler) appUsageCostRub(vcpu, ramGB, storageGB float64, p consumptionPricing, hours float64) float64 {
	if hours <= 0 {
		return 0
	}
	monthly := p.price(vcpu*h.billingUnit.PerVCPU, ramGB*h.billingUnit.PerGBRAM, storageGB*h.billingUnit.PerGBStorage)
	return monthly / hours
}

// upsertAppUsage writes one ledger row, idempotent on the primary key. Reports
// whether the write succeeded so the caller can log an honest row count.
func (h *Handler) upsertAppUsage(
	ctx context.Context,
	key appUsageKey,
	target appMeterTarget,
	hourStart time.Time,
	kind string,
	vcpu, ramGB, storageGB, replicas float64,
	usedVCPU, usedRAMGB *float64,
	costRub float64,
) bool {
	_, err := h.pool.Exec(ctx, `
		INSERT INTO app_usage (
			environment_id, app_name, hour_start, kind,
			org_id, project_id, namespace,
			vcpu, ram_gb, storage_gb, replicas,
			used_vcpu, used_ram_gb, cost_rub
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (environment_id, app_name, hour_start, kind) DO UPDATE
		   SET org_id      = EXCLUDED.org_id,
		       project_id  = EXCLUDED.project_id,
		       namespace   = EXCLUDED.namespace,
		       vcpu        = EXCLUDED.vcpu,
		       ram_gb      = EXCLUDED.ram_gb,
		       storage_gb  = EXCLUDED.storage_gb,
		       replicas    = EXCLUDED.replicas,
		       used_vcpu   = EXCLUDED.used_vcpu,
		       used_ram_gb = EXCLUDED.used_ram_gb,
		       cost_rub    = EXCLUDED.cost_rub,
		       recorded_at = now()
	`,
		target.envID, key.app, hourStart, kind,
		target.orgID, target.projectID, key.namespace,
		vcpu, ramGB, storageGB, replicas,
		usedVCPU, usedRAMGB, costRub,
	)
	if err != nil {
		log.Warn().Err(err).Str("app", key.app).Str("kind", kind).Time("hour", hourStart).Msg("app usage meter: upsert failed")
		return false
	}
	return true
}

// pruneAppUsage drops rows past the retention horizon.
func (h *Handler) pruneAppUsage(ctx context.Context, now time.Time) {
	cutoff := now.Add(-time.Duration(appMeterRetentionDays) * 24 * time.Hour)
	tag, err := h.pool.Exec(ctx, `DELETE FROM app_usage WHERE hour_start < $1`, cutoff)
	if err != nil {
		log.Warn().Err(err).Msg("app usage meter: retention prune failed")
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Info().Int64("rows", n).Time("cutoff", cutoff).Msg("app usage meter: pruned past retention")
	}
}
