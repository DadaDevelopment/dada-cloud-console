package api

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/dada-tuda/console/backend/internal/cache"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// newReadinessPod builds a pod carrying the dada.io/app label plus whatever
// Ready condition and container state the case needs, mirroring the shape
// podAppLabels actually reads off a real cluster.
func newReadinessPod(name, appName string, ready bool, waitingReason string, restarts int32) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	cs := corev1.ContainerStatus{Name: "app", RestartCount: restarts}
	if waitingReason != "" {
		cs.State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: waitingReason}}
	} else {
		cs.State = corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
			Labels:    map[string]string{"dada.io/app": appName},
		},
		Status: corev1.PodStatus{
			Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
			ContainerStatuses: []corev1.ContainerStatus{cs},
		},
	}
}

// TestPodAppLabelsReadsCrashLoopingPodsAsNotReadyToGrow pins the exact bug
// this file exists to fix: reels-tracker-deploy sat in CrashLoopBackOff since
// it was created, never went Ready, and the watcher grew it five times
// anyway because podAppLabels carried nothing about pod health. It now must.
func TestPodAppLabelsReadsCrashLoopingPodsAsNotReadyToGrow(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(
		newReadinessPod("reels-tracker-deploy-abc", "reels-tracker-deploy", false, "CrashLoopBackOff", 47),
	)
	w := &appAutoscaleWatcher{clientset: cs}

	out := w.podAppLabels(context.Background(), "ns")
	got, ok := out["reels-tracker-deploy-abc"]
	if !ok {
		t.Fatalf("pod not found in result")
	}
	if got.Ready {
		t.Errorf("Ready = true, want false for a pod that never passed its readiness probe")
	}
	if !got.CrashLooping {
		t.Errorf("CrashLooping = false, want true, container is waiting on CrashLoopBackOff")
	}
	if got.RestartCount != 47 {
		t.Errorf("RestartCount = %d, want 47", got.RestartCount)
	}
}

// TestPodAppLabelsReadsAHealthyPodAsReady is the fonbet-value shape: a pod
// that is genuinely Ready and running, no crashloop, so the grow path must
// still see it as eligible.
func TestPodAppLabelsReadsAHealthyPodAsReady(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(
		newReadinessPod("fonbet-value-xyz", "fonbet-value", true, "", 0),
	)
	w := &appAutoscaleWatcher{clientset: cs}

	out := w.podAppLabels(context.Background(), "ns")
	got, ok := out["fonbet-value-xyz"]
	if !ok {
		t.Fatalf("pod not found in result")
	}
	if !got.Ready {
		t.Errorf("Ready = false, want true for a healthy running pod")
	}
	if got.CrashLooping {
		t.Errorf("CrashLooping = true, want false")
	}
}

// TestPodAppLabelsReadsARecoveredPodAsReadyDespitePastRestarts guards against
// the tempting-but-wrong alternative gate: refusing on RestartCount alone.
// An app that crashed once, recovered, and is Ready now must still be
// eligible to grow under real pressure.
func TestPodAppLabelsReadsARecoveredPodAsReadyDespitePastRestarts(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(
		newReadinessPod("flaky-app-1", "flaky-app", true, "", 3),
	)
	w := &appAutoscaleWatcher{clientset: cs}

	out := w.podAppLabels(context.Background(), "ns")
	got := out["flaky-app-1"]
	if !got.Ready || got.CrashLooping {
		t.Fatalf("a recovered, currently-Ready pod must read as eligible, got %+v", got)
	}
}

func testAutoscaleReadinessPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping app-autoscale readiness-gate DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAutoscaleReadinessFixture creates a throwaway project, environment and
// sized App snapshot, i.e. the minimum maybeResize needs to reach its
// readiness gate without hitting adoptEnvelope or a namespace quota lookup.
func seedAutoscaleReadinessFixture(t *testing.T, pool *pgxpool.Pool, appName string) (projectID, envID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"autoscale-ready-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+suffix,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	summary, err := json.Marshal(map[string]any{
		"image": "nexus.dada-tuda.ru/dada/" + appName + ":1",
		"resources": map[string]string{
			"cpu_request": "10m", "cpu_limit": "250m",
			"memory_request": "128Mi", "memory_limit": "256Mi",
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Ready', $4, NOW())`,
		projectID, envID, appName, summary); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	return projectID, envID
}

func newAutoscaleReadinessWatcher(pool *pgxpool.Pool, mr *miniredis.Miniredis) *appAutoscaleWatcher {
	return &appAutoscaleWatcher{
		clientset: k8sfake.NewSimpleClientset(),
		h:         &Handler{pool: pool, cache: cache.New(mr.Addr())},
	}
}

// cleanupAutoscaleEvent deletes the (namespace, app_name) row maybeResize's
// cooldown claim writes. app_autoscale_events carries no project_id and no
// foreign key, so nothing cascades it away when the test's throwaway project
// is deleted -- left in place, it silently fails the SAME test's next run
// against a reused local database, since claimAppAutoscaleSlot reads it as
// "already resized within the last 6h".
func cleanupAutoscaleEvent(t *testing.T, pool *pgxpool.Pool, namespace, appName string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app_autoscale_events WHERE namespace = $1 AND app_name = $2`, namespace, appName)
	})
}

// TestMaybeResizeRefusesACrashloopingPodThatWasNeverReady is the M2 proof for
// the live incident: a pod in CrashLoopBackOff, never Ready, must not be
// grown even though it is in the starved set, and the refusal must be
// visible in the audit trail with reason=app_not_ready rather than silently
// dropped.
func TestMaybeResizeRefusesACrashloopingPodThatWasNeverReady(t *testing.T) {
	pool := testAutoscaleReadinessPool(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	suffix := uuid.NewString()[:8]
	appName := "reels-tracker-deploy-" + suffix
	projectID, _ := seedAutoscaleReadinessFixture(t, pool, appName)
	namespace := "ns-crashloop-" + suffix
	cleanupAutoscaleEvent(t, pool, namespace, appName)
	w := newAutoscaleReadinessWatcher(pool, mr)

	s := starvedPod{Namespace: namespace, Pod: "reels-tracker-deploy-abc", Reason: "cpu", Ratio: 0.9}
	pod := podApp{App: appName, Ready: false, CrashLooping: true, RestartCount: 47}

	w.maybeResize(context.Background(), projectID, namespace, appName, s, pod)

	var opCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM operations WHERE project_id = $1 AND resource_name = $2`,
		projectID, appName,
	).Scan(&opCount); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if opCount != 0 {
		t.Fatalf("a never-ready, crashlooping pod must not be resized, got %d ResizeApp operations", opCount)
	}

	outcome, meta := fetchAutoscaleAudit(t, pool, projectID)
	if outcome != auditOutcomeFailure {
		t.Fatalf("refusal must be outcome=failure, got %q", outcome)
	}
	if meta["refusal"] != "app_not_ready" {
		t.Fatalf("expected refusal reason app_not_ready, got %v", meta["refusal"])
	}
	if meta["crashlooping"] != true {
		t.Fatalf("expected crashlooping=true in the audit row, got %v", meta["crashlooping"])
	}
}

// TestMaybeResizeStillGrowsAReadyPodUnderPressure is the regression guard:
// the fonbet-value case (a genuinely Ready app, really starved) must still
// grow exactly as before the readiness gate was added.
func TestMaybeResizeStillGrowsAReadyPodUnderPressure(t *testing.T) {
	pool := testAutoscaleReadinessPool(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	suffix := uuid.NewString()[:8]
	appName := "fonbet-value-" + suffix
	projectID, _ := seedAutoscaleReadinessFixture(t, pool, appName)
	namespace := "ns-healthy-" + suffix
	cleanupAutoscaleEvent(t, pool, namespace, appName)
	w := newAutoscaleReadinessWatcher(pool, mr)

	s := starvedPod{Namespace: namespace, Pod: "fonbet-value-xyz", Reason: "cpu", Ratio: 0.637}
	pod := podApp{App: appName, Ready: true, CrashLooping: false, RestartCount: 0}

	w.maybeResize(context.Background(), projectID, namespace, appName, s, pod)

	var opCount int
	var payloadRaw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM operations WHERE project_id = $1 AND resource_name = $2 AND action = 'ResizeApp'`,
		projectID, appName,
	).Scan(&opCount); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if opCount != 1 {
		t.Fatalf("a Ready, genuinely starved pod must still be grown, got %d ResizeApp operations", opCount)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT payload FROM operations WHERE project_id = $1 AND resource_name = $2 AND action = 'ResizeApp'`,
		projectID, appName,
	).Scan(&payloadRaw); err != nil {
		t.Fatalf("read operation payload: %v", err)
	}
	var payload struct {
		Resources struct {
			CPULimit string `json:"cpu_limit"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Resources.CPULimit != "500m" {
		t.Fatalf("cpu limit = %q, want doubled from 250m to 500m", payload.Resources.CPULimit)
	}
}
