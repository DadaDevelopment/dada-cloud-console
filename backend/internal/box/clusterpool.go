package box

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// phaseClaimed is a pod that has been handed to a tenant but not yet moved into
// tenant egress by ProgramNetwork. It exists so a claim is a single atomic
// transition out of the parked set: no NetworkPolicy selects it, so a pod caught
// in this phase can reach nothing, which is the right side to fail on.
const phaseClaimed = "claimed"

// annBoxClaimedAt records when a pod left the parked set, in RFC3339.
//
// The reaper needs it because a claimed pod is Ready and its creation time says
// nothing about how long it has been claimed: a pod may sit parked for hours
// before a tenant takes it. Without this stamp the only way to bound an
// abandoned claim would be the pod's age, which would reap healthy long-lived
// warm pods the moment they were handed out.
const annBoxClaimedAt = "dada.io/box-claimed-at"

// ClusterPool is the warm pool the production adapter claims from, and its state
// lives in the cluster rather than in this process.
//
// MemoryPool cannot be that pool, and the way it fails is expensive. Its parked
// set is a slice in one process, so every console restart — a deploy, a probe
// kill, an eviction — starts a new process that believes the pool is empty and
// warms a fresh pod, while the pods the old process warmed keep running, claimed
// by nobody and visible to nothing. The namespace fills up with warm pods
// belonging to dead processes until the fleet quota is exhausted, and then a
// customer creating a box is told `pool_exhausted` while six idle boxes sit
// there. Running two replicas doubles the rate and adds a worse failure: two
// in-memory pools cannot agree, so both can hand out the same pod, and two
// tenants share a body.
//
// So the parked set is the pods themselves: pods carrying dada.io/box-phase=parked
// that the kubelet has marked Ready. Nothing is remembered between calls, which
// is precisely why a restart costs nothing and why a second replica sees exactly
// what the first one sees. A claim is an atomic label transition — the API
// server's optimistic concurrency decides the winner — so a pod is handed out at
// most once no matter how many processes reach for it at the same instant.
type ClusterPool struct {
	rt *ClusterRuntime

	mu   sync.Mutex
	want map[poolKey]int
}

var _ ParkingPool = (*ClusterPool)(nil)

// NewClusterPool builds a pool that reads and claims through the given runtime.
func NewClusterPool(rt *ClusterRuntime) *ClusterPool {
	return &ClusterPool{rt: rt, want: map[poolKey]int{}}
}

// Add is a no-op: the warmer creates a real pod, so the instance is already in
// the pool the moment it is parked and Ready. Keeping a second copy here is what
// the whole type exists to avoid.
func (p *ClusterPool) Add(string, string, *Instance) {}

// SetTarget records how many free instances the controller aims to keep. The
// target is an intent, not state a restart can lose the meaning of, so it is the
// one thing this pool keeps in memory.
func (p *ClusterPool) SetTarget(image, region string, n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.want[poolKey{image, region}] = n
}

// Target reports the controller's goal for free instances.
func (p *ClusterPool) Target(image, region string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.want[poolKey{image, region}]
}

// Available counts parked pods that are ready to be claimed right now.
func (p *ClusterPool) Available(image, region string) int {
	pods, err := p.parked(context.Background(), image, region)
	if err != nil {
		return 0
	}
	return len(pods)
}

// Claim takes one parked pod by flipping its phase label, and the flip is the
// claim. Update carries the pod's resourceVersion, so a second claimer racing
// for the same pod is rejected by the API server instead of both succeeding;
// that claimer simply tries the next parked pod.
//
// An empty parked set is a slow claim, not a refusal. The pool target in
// production is one, so the SECOND person to create a box in the same minute
// finds it empty — and answering them `pool_exhausted` is telling a customer the
// product is full when the cluster has room. That answer is a dead end: nothing
// they can do makes it succeed, and the person it hits hardest is the outsider
// trying the product for the first time. So a miss creates a body and waits for
// it. The honesty the miss was protecting is kept where it belongs — in the
// measurement: the returned hit=false labels the spawn and its latency `miss`,
// so a cold start shows up as a cold start in the metric instead of quietly
// inflating the headline ready time.
func (p *ClusterPool) Claim(ctx context.Context, image, region string) (*Instance, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	pods, err := p.parked(ctx, image, region)
	if err != nil {
		return nil, false, fmt.Errorf("list parked boxes: %w", err)
	}
	for i := range pods {
		pod := pods[i]
		pod.Labels[labelBoxPhase] = phaseClaimed
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[annBoxClaimedAt] = p.rt.clock.Now().UTC().Format(time.RFC3339)
		updated, err := p.rt.clientset.CoreV1().Pods(p.rt.Namespace).Update(ctx, &pod, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsConflict(err) || apierrors.IsNotFound(err) {
				continue
			}
			return nil, false, fmt.Errorf("claim parked box %s: %w", pod.Name, err)
		}
		return &Instance{
			ID:          updated.Labels[labelBoxID],
			InstanceRef: updated.Name,
			NodeRef:     updated.Spec.NodeName,
			Image:       image,
			Region:      region,
			SSHHost:     updated.Status.PodIP,
			SSHPort:     p.rt.BrokerPort,
		}, true, nil
	}
	return p.coldStart(ctx, image, region)
}

// coldStart builds a body on the caller's request after the parked set came up
// empty. A failure here is still reported as ErrPoolExhausted so the spawn keeps
// its `pool_exhausted` label: from the customer's side there was no free box and
// making one did not work either, and splitting that into a second reason would
// split the alert that watches it.
func (p *ClusterPool) coldStart(ctx context.Context, image, region string) (*Instance, bool, error) {
	inst, err := p.rt.createClaimed(ctx, image, region)
	if err != nil {
		return nil, false, fmt.Errorf("%w: cold start: %v", ErrPoolExhausted, err)
	}
	return inst, false, nil
}

// parked lists the claimable pods for one image and region.
//
// Readiness is part of being claimable, not a detail: a parked pod is only
// useful once its startup probe has seen the door accept, and handing out one
// that is still pulling would move the cold start into the customer's request
// while reporting a pool hit.
func (p *ClusterPool) parked(ctx context.Context, image, region string) ([]corev1.Pod, error) {
	list, err := p.rt.clientset.CoreV1().Pods(p.rt.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelBox + "=true," + labelBoxPhase + "=" + phaseParked,
	})
	if err != nil {
		return nil, err
	}
	var out []corev1.Pod
	for i := range list.Items {
		pod := list.Items[i]
		if pod.DeletionTimestamp != nil || !clusterPodReady(&pod) {
			continue
		}
		if pod.Annotations["dada.io/box-image"] != image || pod.Annotations["dada.io/box-region"] != region {
			continue
		}
		out = append(out, pod)
	}
	return out, nil
}
