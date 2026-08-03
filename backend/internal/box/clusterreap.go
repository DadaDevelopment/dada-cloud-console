package box

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LiveObjects is the control plane's answer to the only question this sweep asks:
// what in the box namespace does the database still claim.
//
// It is a snapshot of ownership, not of health. Everything the cluster holds that
// is NOT named here has no row behind it, which means no product surface can ever
// reach it, no customer can ever delete it, and nothing but this sweep will ever
// stop paying for it.
//
// Complete is the safety interlock and the reason this is a struct rather than
// three maps. An empty live set is indistinguishable from "the database is
// unreachable", and acting on that reading would delete the entire fleet — every
// running customer box — the first time the shared postgres blinks. The caller
// sets Complete only after every query behind these maps succeeded, and
// ReapUnclaimed refuses to touch anything without it.
type LiveObjects struct {
	InstanceRefs map[string]struct{}
	BoxNames     map[string]struct{}
	Crystals     map[string]struct{}
	Complete     bool
}

// UnclaimedReport counts what one sweep removed, split by kind so a log line says
// which leak was found rather than only that something was.
type UnclaimedReport struct {
	Pods        int
	Claims      int
	Deployments int
	Services    int
	Ingresses   int
}

// Total is how many objects the sweep deleted.
func (r UnclaimedReport) Total() int {
	return r.Pods + r.Claims + r.Deployments + r.Services + r.Ingresses
}

// String renders the report for a log field.
func (r UnclaimedReport) String() string {
	return fmt.Sprintf("pods=%d claims=%d deployments=%d services=%d ingresses=%d",
		r.Pods, r.Claims, r.Deployments, r.Services, r.Ingresses)
}

// unclaimedGrace is how long an object may exist without a database row before it
// counts as garbage.
//
// It exists because the two writes are never one transaction and never in the
// same order. A cold-start spawn creates the pod before it writes the row; a
// crystallization creates its Deployment, Service and Ingress across several
// seconds while its row already exists but its vm_name may not yet be readable by
// another replica. Thirty minutes is far longer than any of those windows and far
// shorter than a leak anybody would notice on a bill.
const unclaimedGrace = 30 * time.Minute

// ReapUnclaimed deletes every object in the box namespace that no live database
// row accounts for.
//
// THIS IS THE PASS THAT WAS MISSING, and the gap it closes was not theoretical:
// the box pool held 15.6% of the platform's monthly bill at zero external demand,
// and the objects doing the holding were ours. ReapOrphans, the sweep that already
// existed, only ever looked at PODS, and only at two shapes of pod — a warm create
// that never went Ready, and a claim that never went live. Everything else a box
// leaves behind was invisible to it:
//
//   - the Deployment, Service, Ingress and permanent PVC a crystallization
//     creates, which survive a promotion that failed or was rolled back;
//   - the Service and Ingress an exposure creates, which survive a box that was
//     deleted while a port was published;
//   - the workspace PVC of a box whose row is a tombstone, which is a disk that
//     is metered forever for a box the customer was told is gone.
//
// The rule is ownership, not age or health: an object is garbage when nothing in
// the database names it. That is the only rule that finds a leak whose shape
// nobody predicted, which is exactly the class of leak this is.
//
// Warm pool pods are exempt by construction. A parked pod is the POOL's property
// and has no row by design — it belongs to no tenant until it is claimed — so
// judging it by the database would delete the warm pool on every tick. Its own
// sweep (ReapOrphans) and the fill loop's trim own its lifetime instead.
//
// Deletes are per-object and non-fatal: one Ingress that will not go must not stop
// the twenty megabytes of PVC behind it from being reclaimed, so failures are
// collected and the sweep continues.
func (c *ClusterRuntime) ReapUnclaimed(ctx context.Context, live LiveObjects) (UnclaimedReport, error) {
	var report UnclaimedReport
	if !live.Complete {
		return report, fmt.Errorf("box: refusing to sweep on an incomplete live set")
	}
	cutoff := c.clock.Now().Add(-unclaimedGrace)
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	pods, err := c.clientset.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelBox + "=true",
	})
	if err != nil {
		return report, fmt.Errorf("list box pods: %w", err)
	}
	livePods := map[string]struct{}{}
	for i := range pods.Items {
		pod := pods.Items[i]
		if pod.Labels[labelBoxPhase] == phaseParked {
			livePods[pod.Name] = struct{}{}
			continue
		}
		if !pod.CreationTimestamp.Time.Before(cutoff) {
			livePods[pod.Name] = struct{}{}
			continue
		}
		if _, claimed := live.InstanceRefs[pod.Name]; claimed {
			livePods[pod.Name] = struct{}{}
			continue
		}
		if err := c.Destroy(ctx, &Instance{ID: pod.Labels[labelBoxID], InstanceRef: pod.Name}); err != nil {
			fail(fmt.Errorf("destroy unclaimed box %s: %w", pod.Name, err))
			livePods[pod.Name] = struct{}{}
			continue
		}
		report.Pods++
	}

	claims, err := c.clientset.CoreV1().PersistentVolumeClaims(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelBox + "=true",
	})
	if err != nil {
		return report, fmt.Errorf("list box claims: %w", err)
	}
	for i := range claims.Items {
		pvc := claims.Items[i]
		if !pvc.CreationTimestamp.Time.Before(cutoff) {
			continue
		}
		ref := strings.TrimSuffix(pvc.Name, "-workspace")
		if _, claimed := live.InstanceRefs[ref]; claimed {
			continue
		}
		if _, hasPod := livePods[ref]; hasPod {
			continue
		}
		if err := c.deleteClaim(ctx, pvc.Name); err != nil {
			fail(err)
			continue
		}
		report.Claims++
	}

	crystalKept := func(obj metav1.Object) bool {
		vm := obj.GetLabels()[labelCrystal]
		if vm == "" {
			return true
		}
		if !obj.GetCreationTimestamp().Time.Before(cutoff) {
			return true
		}
		_, kept := live.Crystals[vm]
		return kept
	}
	exposureKept := func(obj metav1.Object) bool {
		name := obj.GetLabels()[labelBoxName]
		if name == "" {
			return true
		}
		if !obj.GetCreationTimestamp().Time.Before(cutoff) {
			return true
		}
		_, kept := live.BoxNames[name]
		return kept
	}

	deps, err := c.clientset.AppsV1().Deployments(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelCrystal,
	})
	if err != nil {
		return report, fmt.Errorf("list crystal deployments: %w", err)
	}
	for i := range deps.Items {
		dep := deps.Items[i]
		if crystalKept(&dep) {
			continue
		}
		if err := c.clientset.AppsV1().Deployments(c.Namespace).Delete(ctx, dep.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			fail(fmt.Errorf("delete unclaimed crystal deployment %s: %w", dep.Name, err))
			continue
		}
		report.Deployments++
	}

	svcs, err := c.clientset.CoreV1().Services(c.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return report, fmt.Errorf("list box services: %w", err)
	}
	for i := range svcs.Items {
		svc := svcs.Items[i]
		if crystalKept(&svc) && exposureKept(&svc) {
			continue
		}
		if err := c.clientset.CoreV1().Services(c.Namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			fail(fmt.Errorf("delete unclaimed service %s: %w", svc.Name, err))
			continue
		}
		report.Services++
	}

	ings, err := c.clientset.NetworkingV1().Ingresses(c.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return report, fmt.Errorf("list box ingresses: %w", err)
	}
	for i := range ings.Items {
		ing := ings.Items[i]
		if crystalKept(&ing) && exposureKept(&ing) {
			continue
		}
		if err := c.clientset.NetworkingV1().Ingresses(c.Namespace).Delete(ctx, ing.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			fail(fmt.Errorf("delete unclaimed ingress %s: %w", ing.Name, err))
			continue
		}
		report.Ingresses++
	}

	crystalClaims, err := c.clientset.CoreV1().PersistentVolumeClaims(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelCrystal,
	})
	if err != nil {
		return report, fmt.Errorf("list crystal claims: %w", err)
	}
	for i := range crystalClaims.Items {
		pvc := crystalClaims.Items[i]
		if crystalKept(&pvc) {
			continue
		}
		if err := c.deleteClaim(ctx, pvc.Name); err != nil {
			fail(err)
			continue
		}
		report.Claims++
	}

	return report, firstErr
}
