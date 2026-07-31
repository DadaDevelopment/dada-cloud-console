package box

import (
	"context"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/boxcatalog"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// countBoxObjects returns how many pods and PVCs carry the box label in ns, so a
// test can assert "nothing left behind" without naming a specific box id.
func countBoxObjects(t *testing.T, cs *fake.Clientset, ns string) (pods, pvcs int) {
	t.Helper()
	pl, err := cs.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{LabelSelector: labelBox + "=true"})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	cl, err := cs.CoreV1().PersistentVolumeClaims(ns).List(context.Background(), metav1.ListOptions{LabelSelector: labelBox + "=true"})
	if err != nil {
		t.Fatalf("list pvcs: %v", err)
	}
	return len(pl.Items), len(cl.Items)
}

// TestWarmFailedPullLeavesNoOrphan pins the bug the 2026-07-31 incident found:
// a warm attempt whose pod never goes Ready (ImagePullBackOff in production, an
// eternally-Pending pod here) must not leave its Pod or its PVC behind. Before
// this fix that only held if the calling goroutine survived long enough to see
// waitReady fail against the full 20-minute Warm budget; ReadyTimeout shrinks
// that wait so the test — and a real process that gets restarted — do not have
// to wait it out.
func TestWarmFailedPullLeavesNoOrphan(t *testing.T) {
	cs := fake.NewSimpleClientset()
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	rt.ReadyTimeout = 30 * time.Millisecond

	pool := NewMemoryPool()
	err := rt.Warm(context.Background(), pool, boxcatalog.DefaultImage().Name, "ru-1", 1)
	if err == nil {
		t.Fatal("Warm succeeded against a pod that never went ready; want an error")
	}

	pods, pvcs := countBoxObjects(t, cs, "dada-boxes")
	if pods != 0 || pvcs != 0 {
		t.Fatalf("failed warm left pods=%d pvcs=%d behind, want 0 and 0", pods, pvcs)
	}
	if pool.Available(boxcatalog.DefaultImage().Name, "ru-1") != 0 {
		t.Error("a failed warm must not park a broken instance")
	}
}

// TestReapOrphansRemovesPreexistingOrphan is the reconcile half of the fix: a
// pod and PVC left behind by a PREVIOUS process — the case createParked's own
// cleanup cannot reach, because that process is gone — must still be found and
// removed by a fresh sweep.
func TestReapOrphansRemovesPreexistingOrphan(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "box-w1",
				Namespace: "dada-boxes",
				Labels: map[string]string{
					labelBox:      "true",
					labelBoxID:    "w1",
					labelBoxPhase: phaseParked,
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		},
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "box-w1-workspace",
				Namespace: "dada-boxes",
				Labels: map[string]string{
					labelBox:   "true",
					labelBoxID: "w1",
				},
			},
		},
	)
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)

	reaped, err := rt.ReapOrphans(context.Background())
	if err != nil {
		t.Fatalf("ReapOrphans: %v", err)
	}
	if reaped != 1 {
		t.Errorf("reaped %d orphans, want 1", reaped)
	}

	pods, pvcs := countBoxObjects(t, cs, "dada-boxes")
	if pods != 0 || pvcs != 0 {
		t.Fatalf("orphan pod/pvc survived the sweep: pods=%d pvcs=%d", pods, pvcs)
	}
}

// TestReapOrphansLeavesReadyPodsAlone is the guard against the sweep eating a
// healthy warm pool: a pod that has already gone Ready — parked, waiting to be
// claimed, exactly what a successful Warm leaves behind — must survive no
// matter how long it has existed.
func TestReapOrphansLeavesReadyPodsAlone(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "box-w2",
				Namespace: "dada-boxes",
				Labels: map[string]string{
					labelBox:      "true",
					labelBoxID:    "w2",
					labelBoxPhase: phaseParked,
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionTrue},
				},
			},
		},
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "box-w2-workspace",
				Namespace: "dada-boxes",
				Labels: map[string]string{
					labelBox:   "true",
					labelBoxID: "w2",
				},
			},
		},
	)
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)

	reaped, err := rt.ReapOrphans(context.Background())
	if err != nil {
		t.Fatalf("ReapOrphans: %v", err)
	}
	if reaped != 0 {
		t.Errorf("reaped %d, want 0: a Ready parked pod is a healthy warm slot, not an orphan", reaped)
	}

	pods, pvcs := countBoxObjects(t, cs, "dada-boxes")
	if pods != 1 || pvcs != 1 {
		t.Fatalf("healthy warm pod/pvc was removed: pods=%d pvcs=%d, want 1 and 1", pods, pvcs)
	}
}
