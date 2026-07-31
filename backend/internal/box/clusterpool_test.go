package box

import (
	"context"
	"errors"
	"sync"
	"testing"

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
func TestClusterPoolDoesNotHandOutAPodTwice(t *testing.T) {
	cs := fake.NewSimpleClientset(parkedPod("box-w1", "warm-v1", "", true))
	pool := NewClusterPool(newClusterRuntime(cs, nil, "dada-boxes", nil))

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
	pool := NewClusterPool(newClusterRuntime(cs, nil, "dada-boxes", nil))

	if got := pool.Available("warm-v1", ""); got != 0 {
		t.Fatalf("available = %d, want 0: not-ready, wrong image and wrong region are all unclaimable", got)
	}
	if _, hit, err := pool.Claim(context.Background(), "warm-v1", ""); hit || !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("Claim = (hit %v, %v), want ErrPoolExhausted", hit, err)
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
