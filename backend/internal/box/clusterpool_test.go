package box

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/boxcatalog"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// parkedPod builds a pod exactly as createParked leaves one behind: labelled
// parked, annotated with its image and region, and marked Ready by the kubelet.
func parkedPod(name, image, region string, ready bool) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "dada-boxes",
			Labels: map[string]string{
				labelBox:      "true",
				labelBoxID:    name,
				labelBoxPhase: phaseParked,
			},
			Annotations: map[string]string{
				"dada.io/box-image":  image,
				"dada.io/box-region": region,
			},
		},
		Status: corev1.PodStatus{
			PodIP:      "10.244.0.7",
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
		},
	}
}

// TestClusterPoolAdoptsPodsThisProcessDidNotCreate is the fix for the leak that
// filled the fleet quota in production. Every console restart used to start with
// an empty in-memory pool, warm a fresh pod, and abandon the pods the previous
// process had warmed — until six idle warm boxes held the whole quota and a real
// customer got pool_exhausted.
//
// A pool whose state IS the cluster cannot have that bug: a brand new process
// sees the pods that are already there.
func TestClusterPoolAdoptsPodsThisProcessDidNotCreate(t *testing.T) {
	cs := fake.NewSimpleClientset(
		parkedPod("box-w1", "warm-v1", "", true),
		parkedPod("box-w2", "warm-v1", "", true),
	)
	pool := NewClusterPool(newClusterRuntime(cs, nil, "dada-boxes", nil))

	if got := pool.Available("warm-v1", ""); got != 2 {
		t.Fatalf("available = %d in a fresh process, want 2: parked pods left by a previous process are the pool", got)
	}
	inst, hit, err := pool.Claim(context.Background(), "warm-v1", "")
	if err != nil || !hit {
		t.Fatalf("Claim = (%v, %v, %v), want a pool hit", inst, hit, err)
	}
	if inst.InstanceRef == "" || inst.SSHHost != "10.244.0.7" {
		t.Errorf("claimed instance = %+v, want the pod's name and address", inst)
	}
}

// TestClusterPoolDoesNotHandOutAPodTwice pins the property two console replicas
// depend on: the claim is an atomic label transition, so the API server picks
// one winner per pod. Two tenants in one body is the worst failure this
// subsystem has.
//
// The seven losers each fall through to a cold start, which is the fallback
// working and not what is under test here, so ReadyTimeout is cut to nothing:
// their creates give up at once instead of waiting out the real timeout seven
// times over. A cold start returns hit=false, so it can never be miscounted as
// a second winner.
func TestClusterPoolDoesNotHandOutAPodTwice(t *testing.T) {
	cs := fake.NewSimpleClientset(parkedPod("box-w1", "warm-v1", "", true))
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	rt.ReadyTimeout = time.Millisecond
	pool := NewClusterPool(rt)

	var mu sync.Mutex
	hits := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, hit, err := pool.Claim(context.Background(), "warm-v1", ""); hit && err == nil {
				mu.Lock()
				hits++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if hits != 1 {
		t.Fatalf("%d claimers got the same parked pod, want exactly 1", hits)
	}
	if got := pool.Available("warm-v1", ""); got != 0 {
		t.Errorf("available = %d after the only parked pod was claimed, want 0", got)
	}
}

// TestClusterPoolLosesTheRaceAndTakesTheNextPod is the property the previous
// test cannot prove on its own: the fake API server does not enforce optimistic
// concurrency, so the conflict a real one returns is injected here.
//
// Losing a race is not an error. Two replicas listing the same parked set at the
// same instant is the normal case, and a claimer that gave up on the first 409
// would report pool_exhausted with warm pods sitting right there.
func TestClusterPoolLosesTheRaceAndTakesTheNextPod(t *testing.T) {
	cs := fake.NewSimpleClientset(
		parkedPod("box-w1", "warm-v1", "", true),
		parkedPod("box-w2", "warm-v1", "", true),
	)
	var once sync.Once
	cs.PrependReactor("update", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		lost := false
		once.Do(func() { lost = true })
		if lost {
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Resource: "pods"}, "box-w1", errors.New("another claimer won"))
		}
		return false, nil, nil
	})
	pool := NewClusterPool(newClusterRuntime(cs, nil, "dada-boxes", nil))

	inst, hit, err := pool.Claim(context.Background(), "warm-v1", "")
	if err != nil || !hit {
		t.Fatalf("Claim = (%v, %v, %v), want the second parked pod after losing the first", inst, hit, err)
	}
	if inst.InstanceRef == "box-w1" {
		t.Error("claimed the pod whose update was rejected")
	}
}

// TestClusterPoolSkipsPodsThatAreNotClaimable: a pod still pulling its image is
// not a pool slot. Handing one out would move the cold start into the customer's
// request while reporting a pool hit, which is exactly the lie the pool-miss
// metric exists to prevent.
func TestClusterPoolSkipsPodsThatAreNotClaimable(t *testing.T) {
	cs := fake.NewSimpleClientset(
		parkedPod("box-w1", "warm-v1", "", false),
		parkedPod("box-w2", "other-image", "", true),
		parkedPod("box-w3", "warm-v1", "elsewhere", true),
	)
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	rt.ReadyTimeout = 30 * time.Millisecond
	pool := NewClusterPool(rt)

	if got := pool.Available("warm-v1", ""); got != 0 {
		t.Fatalf("available = %d, want 0: not-ready, wrong image and wrong region are all unclaimable", got)
	}
	inst, hit, err := pool.Claim(context.Background(), "warm-v1", "")
	if hit {
		t.Fatalf("Claim reported a pool hit against %v: none of those pods was claimable", inst)
	}
	if err == nil {
		t.Fatalf("Claim returned %v: the cold start had no pod that could become ready", inst)
	}
	for _, name := range []string{"box-w1", "box-w2", "box-w3"} {
		pod, getErr := cs.CoreV1().Pods("dada-boxes").Get(context.Background(), name, metav1.GetOptions{})
		if getErr != nil {
			t.Fatalf("get %s: %v", name, getErr)
		}
		if pod.Labels[labelBoxPhase] != phaseParked {
			t.Errorf("%s left the parked set as %q; an unclaimable pod must not be handed out", name, pod.Labels[labelBoxPhase])
		}
	}
}

// TestClaimColdStartsWhenTheParkedSetIsEmpty is the fix for a dead end the
// production target of one warm box makes routine: the second person to create a
// box in the same minute found the pool empty and was told `pool_exhausted` —
// a refusal they can do nothing about, on a cluster with room to spare.
func TestClaimColdStartsWhenTheParkedSetIsEmpty(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pod, ok := action.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		if !ok {
			return false, nil, nil
		}
		pod.Status.PodIP = "10.244.0.9"
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		return false, nil, nil
	})
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	image := boxcatalog.DefaultImage().Name
	pool := NewClusterPool(rt)

	inst, hit, err := pool.Claim(context.Background(), image, "")
	if err != nil {
		t.Fatalf("Claim against an empty pool: %v", err)
	}
	if hit {
		t.Error("a cold start reported a pool hit; it must be measured as a miss")
	}
	if inst.SSHHost == "" {
		t.Error("a cold-started box came back with no address to reach it on")
	}
	pod, err := cs.CoreV1().Pods("dada-boxes").Get(context.Background(), inst.InstanceRef, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cold-started pod: %v", err)
	}
	if pod.Labels[labelBoxPhase] != phaseClaimed {
		t.Errorf("cold-started pod is %q, want %q: born parked it could be stolen by another claimer",
			pod.Labels[labelBoxPhase], phaseClaimed)
	}
	if pod.Annotations[annBoxClaimedAt] == "" {
		t.Error("cold-started pod carries no claim stamp; the reaper cannot bound it")
	}
}

// TestClaimColdStartFailureIsStillPoolExhausted: when there is no free box AND
// making one fails, the customer's answer is unchanged and so is the alert that
// watches it. A second rejection reason would split that alert in half.
func TestClaimColdStartFailureIsStillPoolExhausted(t *testing.T) {
	cs := fake.NewSimpleClientset()
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	rt.ReadyTimeout = 30 * time.Millisecond
	pool := NewClusterPool(rt)

	_, hit, err := pool.Claim(context.Background(), boxcatalog.DefaultImage().Name, "")
	if hit || !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("Claim = (hit %v, %v), want an ErrPoolExhausted", hit, err)
	}
	pods, pvcs := countBoxObjects(t, cs, "dada-boxes")
	if pods != 0 || pvcs != 0 {
		t.Fatalf("a failed cold start left pods=%d pvcs=%d behind, want 0 and 0", pods, pvcs)
	}
}

// TestClusterPoolWarmCountsWhatIsAlreadyThere closes the loop with the warmer:
// Warm reconciles a deficit against Available, so a pool backed by the cluster
// must report the existing pods and create nothing.
func TestClusterPoolWarmCountsWhatIsAlreadyThere(t *testing.T) {
	cs := fake.NewSimpleClientset(parkedPod("box-w1", "warm-v1", "", true))
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	pool := NewClusterPool(rt)

	if err := rt.Warm(context.Background(), pool, "warm-v1", "", 1); err != nil {
		t.Fatalf("Warm against a pool that is already at target: %v", err)
	}
	pods, err := cs.CoreV1().Pods("dada-boxes").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("warm created %d extra pods, want none: the pool was already at target", len(pods.Items)-1)
	}
}

// TestWarmCountsBoxesThatAreStillComingUp pins the fix for an over-fill seen in
// production on 2026-08-01: with a target of one, two console replicas produced
// two warm pods. A cluster pod needs about ninety seconds to go Ready, and for
// that whole window both replicas asked Available — the CLAIMABLE count — saw
// zero, and each built one. Nothing trims, so the surplus held fleet quota until
// someone deleted it by hand.
func TestWarmCountsBoxesThatAreStillComingUp(t *testing.T) {
	image := boxcatalog.DefaultImage().Name
	cs := fake.NewSimpleClientset(parkedPod("box-w1", image, "", false))
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	rt.ReadyTimeout = 30 * time.Millisecond
	pool := NewClusterPool(rt)

	if err := rt.Warm(context.Background(), pool, image, "", 1); err != nil {
		t.Fatalf("Warm while the only warm box was still coming up: %v", err)
	}
	pods, err := cs.CoreV1().Pods("dada-boxes").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("warm built %d extra pods against a target of 1; a box that is not Ready yet still exists",
			len(pods.Items)-1)
	}
}

// TestWarmTrimsSurplusParkedBoxes is the other half: a pool that only grows is a
// leak. However the surplus arrived — a racing replica, a lowered target — it
// must go, because those pods hold quota a customer's box needs.
func TestWarmTrimsSurplusParkedBoxes(t *testing.T) {
	image := boxcatalog.DefaultImage().Name
	old := parkedPod("box-w1", image, "", true)
	old.CreationTimestamp = metav1.NewTime(time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC))
	mid := parkedPod("box-w2", image, "", true)
	mid.CreationTimestamp = metav1.NewTime(time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC))
	newest := parkedPod("box-w3", image, "", true)
	newest.CreationTimestamp = metav1.NewTime(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
	cs := fake.NewSimpleClientset(old, mid, newest)
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	pool := NewClusterPool(rt)

	if err := rt.Warm(context.Background(), pool, image, "", 1); err != nil {
		t.Fatalf("Warm against a pool over target: %v", err)
	}
	pods, err := cs.CoreV1().Pods("dada-boxes").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("%d warm boxes survived a target of 1", len(pods.Items))
	}
	if pods.Items[0].Name != "box-w3" {
		t.Errorf("trim kept %s, want box-w3: the oldest parked box has the least of its deadline left",
			pods.Items[0].Name)
	}
	if _, pvcs := countBoxObjects(t, cs, "dada-boxes"); pvcs != 0 {
		t.Errorf("trim left %d workspace claims behind", pvcs)
	}
}

// TestTrimNeverTakesABoxSomebodyIsUsing is the guard the whole subsystem is
// built around: Destroy takes the workspace PVC with the pod, so a trim that
// could reach a live box would erase a customer's work. Only a parked and Ready
// pod is a candidate — a pod still coming up belongs to a create still in flight.
func TestTrimNeverTakesABoxSomebodyIsUsing(t *testing.T) {
	image := boxcatalog.DefaultImage().Name
	live := parkedPod("box-live", image, "", true)
	live.Labels[labelBoxPhase] = phaseLive
	claimed := parkedPod("box-claimed", image, "", true)
	claimed.Labels[labelBoxPhase] = phaseClaimed
	coming := parkedPod("box-coming", image, "", false)
	cs := fake.NewSimpleClientset(live, claimed, coming, parkedPod("box-free", image, "", true))
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	pool := NewClusterPool(rt)

	trimmed, err := pool.Trim(context.Background(), image, "", 0)
	if err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if trimmed != 1 {
		t.Fatalf("trimmed %d boxes, want exactly the one free parked box", trimmed)
	}
	for _, name := range []string{"box-live", "box-claimed", "box-coming"} {
		if _, err := cs.CoreV1().Pods("dada-boxes").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
			t.Errorf("trim destroyed %s: %v", name, err)
		}
	}
}
