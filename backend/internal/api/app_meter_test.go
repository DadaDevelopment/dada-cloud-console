package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/costengine"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestFoldHourSeriesCountsMissingSamplesAsZero is the test the whole
// QueryRange-instead-of-avg_over_time decision exists for: an app present for
// half the hour must fold to half its footprint. avg_over_time would have
// returned the full footprint, and the customer would have been billed for an
// hour they did not have.
func TestFoldHourSeriesCountsMissingSamplesAsZero(t *testing.T) {
	full := make([]prometheus.Point, appMeterStepsPerHour)
	for i := range full {
		full[i] = prometheus.Point{V: 2}
	}
	half := make([]prometheus.Point, appMeterStepsPerHour/2)
	for i := range half {
		half[i] = prometheus.Point{V: 2}
	}

	got := foldHourSeries([]prometheus.Series{
		{Metric: map[string]string{"namespace": "ns-a", "app": "whole"}, Points: full},
		{Metric: map[string]string{"namespace": "ns-a", "app": "halfway"}, Points: half},
	})

	if v := got[appUsageKey{namespace: "ns-a", app: "whole"}]; v != 2 {
		t.Fatalf("app present all hour: want 2, got %v", v)
	}
	if v := got[appUsageKey{namespace: "ns-a", app: "halfway"}]; v != 1 {
		t.Fatalf("app present half the hour: want 1, got %v", v)
	}
}

// TestFoldHourSeriesDropsUnattributableKeepsZero pins both halves of the fold's
// contract: a series the join failed to label has no one to bill and is
// dropped, while an app measured at zero is KEPT -- used_* has to be able to
// say "burned nothing" (0) as distinct from "no metrics" (NULL), and an idle
// bot averages to exactly zero CPU.
func TestFoldHourSeriesDropsUnattributableKeepsZero(t *testing.T) {
	points := make([]prometheus.Point, appMeterStepsPerHour)
	for i := range points {
		points[i] = prometheus.Point{V: 1}
	}
	zeros := make([]prometheus.Point, appMeterStepsPerHour)

	got := foldHourSeries([]prometheus.Series{
		{Metric: map[string]string{"namespace": "ns-a"}, Points: points},
		{Metric: map[string]string{"app": "orphan"}, Points: points},
		{Metric: map[string]string{"namespace": "ns-a", "app": "idle"}, Points: zeros},
		{Metric: map[string]string{"namespace": "ns-a", "app": "real"}, Points: points},
	})

	if len(got) != 2 {
		t.Fatalf("want the two attributable apps, got %d entries: %v", len(got), got)
	}
	if v := got[appUsageKey{namespace: "ns-a", app: "real"}]; v != 1 {
		t.Fatalf("surviving app: want 1, got %v", v)
	}
	if v, ok := got[appUsageKey{namespace: "ns-a", app: "idle"}]; !ok || v != 0 {
		t.Fatalf("measured zero must survive as 0, got %v (present=%v)", v, ok)
	}
}

// TestHoursInMonthTracksCalendar guards the hourly divisor against the
// hardcoded 720 that would overcharge February by 7% and undercharge every
// 31-day month.
func TestHoursInMonthTracksCalendar(t *testing.T) {
	cases := []struct {
		when time.Time
		want float64
	}{
		{time.Date(2026, 1, 17, 4, 0, 0, 0, time.UTC), 744},
		{time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC), 672},
		{time.Date(2024, 2, 3, 0, 0, 0, 0, time.UTC), 696},
		{time.Date(2026, 4, 30, 23, 0, 0, 0, time.UTC), 720},
	}
	for _, c := range cases {
		if got := hoursInMonth(c.when); got != c.want {
			t.Fatalf("hoursInMonth(%s): want %v, got %v", c.when.Format("2006-01"), c.want, got)
		}
	}
}

// TestAppMeterExprsScopeToRunningUserNamespaces pins the two properties that
// keep the ledger honest: it never leaves the namespaces the DB says are
// customer environments, and it never bills a pod that is not Running (a
// Pending pod carries full resource requests, so without the phase filter a
// failed image pull would arrive as a bill).
func TestAppMeterExprsScopeToRunningUserNamespaces(t *testing.T) {
	namespaces := []string{"tenant-a", "tenant-b"}
	exprs := appMeterExprs(namespaces, podPhaseRunning)
	all := append(exprs[:], appMeterStorageExpr(namespaces))

	for i, expr := range all {
		if !strings.Contains(expr, `namespace=~"tenant-a|tenant-b"`) {
			t.Fatalf("expr %d not scoped to user namespaces: %s", i, expr)
		}
		if !strings.Contains(expr, "label_dada_io_app") {
			t.Fatalf("expr %d does not join on the app label: %s", i, expr)
		}
	}
	for i := 0; i < 3; i++ {
		if !strings.Contains(all[i], `phase="Running"`) {
			t.Fatalf("billing-basis expr %d bills non-Running pods: %s", i, all[i])
		}
	}
}

// TestAppNamespaceMatcherEscapes keeps a namespace name from breaking out of
// its own label matcher.
func TestAppNamespaceMatcherEscapes(t *testing.T) {
	got := appNamespaceMatcher([]string{`ns"evil`})
	if strings.Contains(got, `"ns"evil"`) {
		t.Fatalf("namespace value not escaped: %s", got)
	}
	if !strings.Contains(got, `ns\"evil`) {
		t.Fatalf("want escaped quote in matcher, got %s", got)
	}
}

// TestAppMeterNamespacesSorted keeps the generated PromQL stable across ticks
// instead of reshuffled by Go's map iteration order.
func TestAppMeterNamespacesSorted(t *testing.T) {
	got := appMeterNamespaces(map[string]appMeterTarget{"zeta": {}, "alpha": {}, "mid": {}})
	if strings.Join(got, ",") != "alpha,mid,zeta" {
		t.Fatalf("want sorted namespaces, got %v", got)
	}
}

// TestAppUsageCostRubDividesMonthlyPrice pins the hourly row against the
// monthly price every other consumption surface shows: a full month of rows
// must sum back to the monthly figure, not to some independently invented one.
func TestAppUsageCostRubDividesMonthlyPrice(t *testing.T) {
	h := &Handler{billingUnit: costengine.UnitCost{PerVCPU: 744, PerGBRAM: 74.4, PerGBStorage: 7.44}}
	p := consumptionPricing{markup: 1}

	if got := h.appUsageCostRub(1, 0, 0, p, 744); got != 1 {
		t.Fatalf("one vCPU-hour in a 744h month: want 1, got %v", got)
	}
	if got := h.appUsageCostRub(0, 10, 0, p, 744); got != 1 {
		t.Fatalf("ten GB-hours of RAM: want 1, got %v", got)
	}
	if got := h.appUsageCostRub(1, 0, 0, p, 0); got != 0 {
		t.Fatalf("zero hours must not divide by zero, got %v", got)
	}
}

func TestAppUsageCostRubAppliesCommonMarkup(t *testing.T) {
	h := &Handler{billingUnit: costengine.UnitCost{PerVCPU: 744}}
	plain := consumptionPricing{markup: 1}
	markedUp := consumptionPricing{markup: 1.5}

	base := h.appUsageCostRub(1, 0, 0, plain, 744)
	got := h.appUsageCostRub(1, 0, 0, markedUp, 744)
	if want := base * 1.5; got != want {
		t.Fatalf("common markup: want %v, got %v", want, got)
	}
}

func appUsagePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping app-usage DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	ensureAppUsageTable(t, pool)
	return pool
}

// ensureAppUsageTable applies migration 101 before the DB tests run.
//
// The test database is shared with the running console, which applies
// migrations at boot -- so on the build that INTRODUCES the table, CI would run
// these tests against a schema that does not have it yet. The migration is
// idempotent (IF NOT EXISTS throughout), so applying it here is exactly what
// the deploy does minutes later, not a divergent test-only schema.
func ensureAppUsageTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, m := range []string{"101_app_usage.sql", "102_app_usage_source.sql"} {
		sql, err := os.ReadFile("../../migrations/" + m)
		if err != nil {
			t.Fatalf("read migration %s: %v", m, err)
		}
		if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
			t.Fatalf("apply migration %s: %v", m, err)
		}
	}
}

// seedAppUsageEnv creates a project with one k8s environment and one box
// environment, returning the k8s namespace, the box namespace and the ids.
func seedAppUsageEnv(t *testing.T, pool *pgxpool.Pool) (projectID, envID uuid.UUID, orgID, k8sNS, boxNS string) {
	t.Helper()
	ctx := context.Background()
	orgID = "org-appusage-" + uuid.NewString()[:8]
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"appusage-"+uuid.NewString()[:8], orgID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	k8sNS = "appusage-k8s-" + uuid.NewString()[:8]
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime)
		 VALUES ($1, 'prod', $2, 'prod', 'k8s') RETURNING id`,
		projectID, k8sNS,
	).Scan(&envID); err != nil {
		t.Fatalf("seed k8s environment: %v", err)
	}
	boxNS = "appusage-box-" + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime)
		 VALUES ($1, 'devbox', $2, 'dev', 'box')`,
		projectID, boxNS,
	); err != nil {
		t.Fatalf("seed box environment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_usage WHERE project_id = $1`, projectID)
	})
	return projectID, envID, orgID, k8sNS, boxNS
}

// TestAppMeterTargetsCoversOnlyK8sEnvironments proves the DB join IS the
// "user apps only" filter: a box environment, and every cluster namespace with
// no environment row at all, cannot become a ledger row.
func TestAppMeterTargetsCoversOnlyK8sEnvironments(t *testing.T) {
	pool := appUsagePool(t)
	projectID, envID, orgID, k8sNS, boxNS := seedAppUsageEnv(t, pool)

	h := &Handler{pool: pool}
	targets, err := h.appMeterTargets(context.Background())
	if err != nil {
		t.Fatalf("appMeterTargets: %v", err)
	}

	got, ok := targets[k8sNS]
	if !ok {
		t.Fatalf("k8s namespace %s missing from meter targets", k8sNS)
	}
	if got.envID != envID || got.projectID != projectID || got.orgID != orgID {
		t.Fatalf("tenancy mismatch: %+v", got)
	}
	if _, ok := targets[boxNS]; ok {
		t.Fatalf("box namespace %s must not be metered as an app", boxNS)
	}
	for _, infra := range []string{"kube-system", "longhorn-system", "monitoring"} {
		if _, ok := targets[infra]; ok {
			t.Fatalf("infrastructure namespace %s must not be metered as an app", infra)
		}
	}
}

// TestUpsertAppUsageIdempotent proves the property migration 101 leans on
// instead of an advisory lock: two backend replicas metering the same hour
// leave one row, not two, and the later write wins.
func TestUpsertAppUsageIdempotent(t *testing.T) {
	pool := appUsagePool(t)
	projectID, envID, orgID, k8sNS, _ := seedAppUsageEnv(t, pool)
	ctx := context.Background()

	h := &Handler{pool: pool}
	key := appUsageKey{namespace: k8sNS, app: "web"}
	target := appMeterTarget{envID: envID, projectID: projectID, orgID: orgID}
	hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	used := 0.5

	if !h.upsertAppUsage(ctx, key, target, hour, appUsageKindPod, 0.25, 0.5, 0, 1, &used, &used, 1.5, appUsageSourceMeter) {
		t.Fatal("first upsert failed")
	}
	if !h.upsertAppUsage(ctx, key, target, hour, appUsageKindPod, 0.5, 1, 0, 2, &used, &used, 3, appUsageSourceMeter) {
		t.Fatal("second upsert failed")
	}
	if !h.upsertAppUsage(ctx, key, target, hour, appUsageKindVolume, 0, 0, 20, 0, nil, nil, 0.4, appUsageSourceMeter) {
		t.Fatal("volume upsert failed")
	}

	var rows int
	var vcpu, replicas, cost float64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM app_usage WHERE environment_id = $1 AND app_name = $2 AND hour_start = $3`,
		envID, "web", hour).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("want exactly one pod row and one volume row, got %d", rows)
	}
	if err := pool.QueryRow(ctx,
		`SELECT vcpu, replicas, cost_rub FROM app_usage
		  WHERE environment_id = $1 AND app_name = $2 AND hour_start = $3 AND kind = $4`,
		envID, "web", hour, appUsageKindPod).Scan(&vcpu, &replicas, &cost); err != nil {
		t.Fatalf("read pod row: %v", err)
	}
	if vcpu != 0.5 || replicas != 2 || cost != 3 {
		t.Fatalf("second write must win: vcpu=%v replicas=%v cost=%v", vcpu, replicas, cost)
	}
}

// TestPruneAppUsageKeepsRetentionWindow proves the meter enforces the 30-day
// horizon it promises -- the ledger must not become the next table to fill the
// disk the database already died on once.
func TestPruneAppUsageKeepsRetentionWindow(t *testing.T) {
	pool := appUsagePool(t)
	projectID, envID, orgID, k8sNS, _ := seedAppUsageEnv(t, pool)
	ctx := context.Background()

	h := &Handler{pool: pool}
	key := appUsageKey{namespace: k8sNS, app: "web"}
	target := appMeterTarget{envID: envID, projectID: projectID, orgID: orgID}
	now := time.Now().UTC().Truncate(time.Hour)
	fresh := now.Add(-24 * time.Hour)
	stale := now.Add(-time.Duration(appMeterRetentionDays+1) * 24 * time.Hour)

	h.upsertAppUsage(ctx, key, target, fresh, appUsageKindPod, 0.25, 0.5, 0, 1, nil, nil, 1, appUsageSourceMeter)
	h.upsertAppUsage(ctx, key, target, stale, appUsageKindPod, 0.25, 0.5, 0, 1, nil, nil, 1, appUsageSourceMeter)

	h.pruneAppUsage(ctx, now)

	var hours []time.Time
	rows, err := pool.Query(ctx,
		`SELECT hour_start FROM app_usage WHERE environment_id = $1 ORDER BY hour_start`, envID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h time.Time
		if err := rows.Scan(&h); err != nil {
			t.Fatalf("scan: %v", err)
		}
		hours = append(hours, h)
	}
	if len(hours) != 1 || !hours[0].Equal(fresh) {
		t.Fatalf("want only the in-window row %s, got %v", fresh, hours)
	}
}

// TestAppHourAlreadyMeteredSeesWrittenHour covers the skip check the tick loop
// uses to avoid re-querying Prometheus for hours it already has.
//
// It probes a FUTURE hour on purpose: the check is cluster-wide ("was this hour
// metered at all"), and the test database is shared with a running meter, so
// any past hour may legitimately already hold rows.
func TestAppHourAlreadyMeteredSeesWrittenHour(t *testing.T) {
	pool := appUsagePool(t)
	projectID, envID, orgID, k8sNS, _ := seedAppUsageEnv(t, pool)
	ctx := context.Background()

	h := &Handler{pool: pool}
	hour := time.Now().UTC().Truncate(time.Hour).Add(100 * time.Hour)

	before, err := h.appHourAlreadyMetered(ctx, hour)
	if err != nil {
		t.Fatalf("probe empty hour: %v", err)
	}
	if before {
		t.Fatalf("hour %s reported as metered before anything was written", hour)
	}

	h.upsertAppUsage(ctx, appUsageKey{namespace: k8sNS, app: "web"},
		appMeterTarget{envID: envID, projectID: projectID, orgID: orgID},
		hour, appUsageKindPod, 0.25, 0.5, 0, 1, nil, nil, 1, appUsageSourceMeter)

	after, err := h.appHourAlreadyMetered(ctx, hour)
	if err != nil {
		t.Fatalf("probe written hour: %v", err)
	}
	if !after {
		t.Fatalf("hour %s not seen after a row was written", hour)
	}
}
