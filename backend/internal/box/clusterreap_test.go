package box

import (
	"context"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/boxcatalog"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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

// readyOnCreate makes every pod the fake clientset accepts come back Ready, which
// is what lets a test exercise the SUCCESSFUL warm path: the real readiness signal
// is a startup probe against the box's own door, and there is no door in a fake.
func readyOnCreate(cs *fake.Clientset) {
	cs.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pod, ok := action.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		if !ok {
			return false, nil, nil
		}
		pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		})
		return false, nil, nil
	})
}

// TestWarmReconcilesToTheTarget is the fix for the defect that made the warm pool
// a one-shot: Warm used to CREATE n pods on every call, so the only safe place to
// call it was boot, so a pool emptied by the first customer stayed empty until
// somebody restarted the console — and a create that failed at boot for a reason
// that later passed (a tag not pushed yet, a momentarily full quota) failed
// forever. Warm now fills only the shortfall, which is what makes it safe to run
// on a ticker, and the ticker is the thing that refills after a claim and retries
// after a bad boot.
func TestWarmReconcilesToTheTarget(t *testing.T) {
	cs := fake.NewSimpleClientset()
	readyOnCreate(cs)
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	pool := NewMemoryPool()
	image := boxcatalog.DefaultImage().Name
	ctx := context.Background()

	if err := rt.Warm(ctx, pool, image, "ru-1", 2); err != nil {
		t.Fatalf("warm to 2: %v", err)
	}
	if got := pool.Available(image, "ru-1"); got != 2 {
		t.Fatalf("available=%d after warming to 2, want 2", got)
	}
	if pods, _ := countBoxObjects(t, cs, "dada-boxes"); pods != 2 {
		t.Fatalf("warming to 2 created %d pods, want 2", pods)
	}

	if err := rt.Warm(ctx, pool, image, "ru-1", 2); err != nil {
		t.Fatalf("second warm to 2: %v", err)
	}
	if pods, _ := countBoxObjects(t, cs, "dada-boxes"); pods != 2 {
		t.Fatalf("a warm against a full pool built bodies nobody asked for: pods=%d, want 2", pods)
	}

	if _, _, err := pool.Claim(ctx, image, "ru-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := rt.Warm(ctx, pool, image, "ru-1", 2); err != nil {
		t.Fatalf("refill after claim: %v", err)
	}
	if got := pool.Available(image, "ru-1"); got != 2 {
		t.Fatalf("available=%d after the refill, want 2: a claimed slot has to come back", got)
	}
	if pods, _ := countBoxObjects(t, cs, "dada-boxes"); pods != 3 {
		t.Fatalf("refill left %d pods, want 3 (two warm plus the replacement for the claimed one)", pods)
	}
	if got := pool.Target(image, "ru-1"); got != 2 {
		t.Fatalf("target=%d, want 2: a refill must not rewrite the target to its own shortfall", got)
	}
}

// TestDestroyFromDatabaseRowDeletesTheWorkspace pins the leak that made a
// customer-visible delete cost money forever: the control plane rebuilds an
// Instance from the boxes table, where ID is the platform uuid and the runtime's
// own id survives only inside InstanceRef. Deriving the claim name from ID names
// a claim that never existed, so the pod went away and its 20Gi volume did not.
func TestDestroyFromDatabaseRowDeletesTheWorkspace(t *testing.T) {
	cs := fake.NewSimpleClientset()
	readyOnCreate(cs)
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	pool := NewMemoryPool()

	if err := rt.Warm(context.Background(), pool, boxcatalog.DefaultImage().Name, "ru1", 1); err != nil {
		t.Fatalf("warm: %v", err)
	}
	inst, _, err := pool.Claim(context.Background(), boxcatalog.DefaultImage().Name, "ru1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	fromRow := &Instance{ID: "a53392a6-45aa-4ac7-b8c3-f57b1280f7f3", InstanceRef: inst.InstanceRef}
	if err := rt.Destroy(context.Background(), fromRow); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if pods, pvcs := countBoxObjects(t, cs, "dada-boxes"); pods != 0 || pvcs != 0 {
		t.Fatalf("destroy from a database row left %d pods and %d pvcs behind", pods, pvcs)
	}
}

// TestSuspendKeepsTheWorkspaceAndResumeReattachesIt pins the promise the API
// makes about sleep: it is not a delete. The body goes away, the claim does not,
// and a resume rebuilds a pod on the same name so the same volume comes back.
func TestSuspendKeepsTheWorkspaceAndResumeReattachesIt(t *testing.T) {
	cs := fake.NewSimpleClientset()
	readyOnCreate(cs)
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	pool := NewMemoryPool()
	image := boxcatalog.DefaultImage().Name

	if err := rt.Warm(context.Background(), pool, image, "ru1", 1); err != nil {
		t.Fatalf("warm: %v", err)
	}
	inst, _, err := pool.Claim(context.Background(), image, "ru1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	podName := inst.InstanceRef

	fromRow := &Instance{ID: "a53392a6-45aa-4ac7-b8c3-f57b1280f7f3", InstanceRef: podName, Image: image, Region: "ru1"}
	if err := rt.Suspend(context.Background(), fromRow); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	pods, pvcs := countBoxObjects(t, cs, "dada-boxes")
	if pods != 0 {
		t.Fatalf("suspend left %d pods running", pods)
	}
	if pvcs != 1 {
		t.Fatalf("suspend destroyed the workspace: %d claims left", pvcs)
	}

	if err := rt.Resume(context.Background(), fromRow, Spec{Image: image, Region: "ru1"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if fromRow.InstanceRef != podName {
		t.Fatalf("resume moved the box to pod %q, so it would mount a different volume than %q", fromRow.InstanceRef, podName)
	}
	if pods, pvcs := countBoxObjects(t, cs, "dada-boxes"); pods != 1 || pvcs != 1 {
		t.Fatalf("after resume: %d pods, %d pvcs", pods, pvcs)
	}
}

// TestReapOrphansRemovesAbandonedClaim closes the second way a pod is abandoned.
// A claim is a label flip out of the parked set, and Spawn moves the pod on to
// live seconds later; a pod still claimed long afterwards belongs to a spawn that
// died in between. It is Ready, so the not-Ready rule cannot see it, and nothing
// will ever come back for it — it just holds a slot of the fleet quota until a
// paying customer's create is answered with pool_exhausted.
func TestReapOrphansRemovesAbandonedClaim(t *testing.T) {
	start := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	cs := fake.NewSimpleClientset(parkedPod("box-w1", "warm-v1", "", true))
	rt := newClusterRuntime(cs, nil, "dada-boxes", clock)
	pool := NewClusterPool(rt)

	if _, hit, err := pool.Claim(context.Background(), "warm-v1", ""); !hit || err != nil {
		t.Fatalf("Claim = (hit %v, %v), want a pool hit to abandon", hit, err)
	}

	clock.Advance(clusterClaimOrphanAfter / 2)
	if reaped, err := rt.ReapOrphans(context.Background()); err != nil || reaped != 0 {
		t.Fatalf("reaped %d (%v) while a spawn could still be in flight, want 0", reaped, err)
	}

	clock.Advance(clusterClaimOrphanAfter)
	reaped, err := rt.ReapOrphans(context.Background())
	if err != nil {
		t.Fatalf("ReapOrphans: %v", err)
	}
	if reaped != 1 {
		t.Fatalf("reaped %d abandoned claims, want 1", reaped)
	}
	if pods, _ := countBoxObjects(t, cs, "dada-boxes"); pods != 0 {
		t.Errorf("%d abandoned pods survived the sweep", pods)
	}
}

// TestReapOrphansLeavesAnOldParkedPodAlone is the guard the claim rule needs: a
// warm pod's age says nothing, so the sweep must key off the claim stamp and not
// off creation. Reaping by age here would empty the warm pool every time it got
// old enough to be useful.
func TestReapOrphansLeavesAnOldParkedPodAlone(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
	cs := fake.NewSimpleClientset(parkedPod("box-w1", "warm-v1", "", true))
	rt := newClusterRuntime(cs, nil, "dada-boxes", clock)

	clock.Advance(72 * time.Hour)
	if reaped, err := rt.ReapOrphans(context.Background()); err != nil || reaped != 0 {
		t.Fatalf("reaped %d parked pods (%v) after three days, want 0", reaped, err)
	}
}
