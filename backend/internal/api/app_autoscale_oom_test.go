package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// newOOMPod builds the shape a container killed by the kernel actually leaves
// behind: the restart is already in flight, so the kill sits in
// lastTerminationState while the container waits on its backoff.
func newOOMPod(name, appName, reason string, finishedAgo time.Duration) *corev1.Pod {
	cs := corev1.ContainerStatus{
		Name:         "app",
		RestartCount: 3,
		State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason:     reason,
			ExitCode:   137,
			FinishedAt: metav1.NewTime(time.Now().Add(-finishedAgo)),
		}},
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
			Labels:    map[string]string{"dada.io/app": appName},
		},
		Status: corev1.PodStatus{
			Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
			ContainerStatuses: []corev1.ContainerStatus{cs},
		},
	}
}

// TestPodAppLabelsReadsARecentOOMKill is the leadgen/prod/lead-gen shape: a
// headless browser killed at its 256Mi limit mid-scan. The kill is the whole
// diagnosis, so podAppLabels has to carry it off the same list call.
func TestPodAppLabelsReadsARecentOOMKill(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(newOOMPod("lead-gen-abc", "lead-gen", "OOMKilled", time.Minute))
	w := &appAutoscaleWatcher{clientset: cs}

	got, ok := w.podAppLabels(context.Background(), "ns")["lead-gen-abc"]
	if !ok {
		t.Fatalf("pod not found in result")
	}
	if !got.OOMKilled {
		t.Errorf("OOMKilled = false, want true for a container the kernel killed a minute ago")
	}
}

// TestPodAppLabelsIgnoresAnOldOOMKill holds the other pole. Kubernetes keeps
// lastTerminationState for the life of the pod, so a single kill last week must
// not read as current starvation and grow the app once per cooldown forever.
func TestPodAppLabelsIgnoresAnOldOOMKill(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(newOOMPod("lead-gen-old", "lead-gen", "OOMKilled", 7*24*time.Hour))
	w := &appAutoscaleWatcher{clientset: cs}

	if w.podAppLabels(context.Background(), "ns")["lead-gen-old"].OOMKilled {
		t.Errorf("OOMKilled = true for a kill a week old, want false")
	}
}

// TestPodAppLabelsIgnoresANonOOMTermination guards the discrimination the whole
// override rests on: an app that exits 1 on a missing variable crash-loops the
// same way and must stay refused, because memory is not its problem.
func TestPodAppLabelsIgnoresANonOOMTermination(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(newOOMPod("broken-app-1", "broken-app", "Error", time.Minute))
	w := &appAutoscaleWatcher{clientset: cs}

	if w.podAppLabels(context.Background(), "ns")["broken-app-1"].OOMKilled {
		t.Errorf("OOMKilled = true for a container that exited with Error, want false")
	}
}

func TestOOMStarvedPodsAreMemoryStarvationAtRatioOne(t *testing.T) {
	got := oomStarvedPods("ns", map[string]podApp{
		"b-pod":    {App: "b", OOMKilled: true},
		"a-pod":    {App: "a", OOMKilled: true},
		"calm-pod": {App: "c"},
		"nolabel":  {OOMKilled: true},
	})
	if len(got) != 2 {
		t.Fatalf("got %d starved pods, want the 2 that were killed and carry an app: %+v", len(got), got)
	}
	if got[0].Pod != "a-pod" || got[1].Pod != "b-pod" {
		t.Errorf("order = %q, %q, want pod name order so a tick is repeatable", got[0].Pod, got[1].Pod)
	}
	if got[0].Reason != "memory" || got[0].Ratio != 1 {
		t.Errorf("got %+v, want memory starvation at ratio 1", got[0])
	}
}

func TestOOMOverridesReadinessOnlyForMemory(t *testing.T) {
	killed := podApp{App: "lead-gen", OOMKilled: true}
	if !oomOverridesReadiness(killed, starvedPod{Reason: "memory"}) {
		t.Errorf("an OOM-killed pod must be growable on the memory dimension")
	}
	if oomOverridesReadiness(killed, starvedPod{Reason: "cpu"}) {
		t.Errorf("an OOM kill says nothing about CPU and must not unlock a CPU grow")
	}
	if oomOverridesReadiness(podApp{App: "broken"}, starvedPod{Reason: "memory"}) {
		t.Errorf("a crash-looping pod with no OOM kill must stay refused")
	}
}

// TestMaybeResizeGrowsAnOOMKilledPodDespiteTheReadinessGate is the M2 proof for
// 2026-08-25: lead-gen was killed at its memory limit, which left it not Ready
// and crash-looping, which is exactly the state the readiness gate refuses. The
// app stayed down until a human edited argo-infra by hand, and every lever the
// product offered topped out at 1Gi.
func TestMaybeResizeGrowsAnOOMKilledPodDespiteTheReadinessGate(t *testing.T) {
	pool := testAutoscaleReadinessPool(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	suffix := uuid.NewString()[:8]
	appName := "lead-gen-" + suffix
	projectID, _ := seedAutoscaleReadinessFixture(t, pool, appName)
	namespace := "ns-oom-" + suffix
	cleanupAutoscaleEvent(t, pool, namespace, appName)
	w := newAutoscaleReadinessWatcher(pool, mr)

	s := starvedPod{Namespace: namespace, Pod: "lead-gen-abc", Reason: "memory", Ratio: 1}
	pod := podApp{App: appName, Ready: false, CrashLooping: true, RestartCount: 3, OOMKilled: true}

	w.maybeResize(context.Background(), projectID, namespace, appName, s, pod)

	var payloadRaw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT payload FROM operations WHERE project_id = $1 AND resource_name = $2 AND action = 'ResizeApp'`,
		projectID, appName,
	).Scan(&payloadRaw); err != nil {
		t.Fatalf("an OOM-killed app must be grown, no ResizeApp operation found: %v", err)
	}
	var payload struct {
		Resources struct {
			MemoryLimit string `json:"memory_limit"`
			CPULimit    string `json:"cpu_limit"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Resources.MemoryLimit != "512Mi" {
		t.Fatalf("memory limit = %q, want doubled from 256Mi to 512Mi", payload.Resources.MemoryLimit)
	}
	if payload.Resources.CPULimit != "250m" {
		t.Fatalf("cpu limit = %q, want it left alone: the kill was about memory", payload.Resources.CPULimit)
	}
}
