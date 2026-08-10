package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// inPlaceResizeSettleTimeout bounds the wait for the kubelet to report the new
// envelope back in the pod status. A resize is applied by the API server
// immediately but actuated asynchronously, so the status lags by a scrape, and
// an accepted-but-not-yet-actuated resize must not be reported as a failure.
const inPlaceResizeSettleTimeout = 20 * time.Second

// inPlaceResizeOutcome is what one app's pods did with the new envelope.
//
// Resized and Pending are both successes from the caller's point of view: the
// API server accepted the new numbers and nothing restarted. Failed means the
// resize subresource itself refused, which is the only case where the git
// commit remains the app's sole path to the new size.
type inPlaceResizeOutcome struct {
	Resized int
	Pending int
	Failed  int
	Skipped int
}

func (o inPlaceResizeOutcome) String() string {
	return fmt.Sprintf("resized=%d pending=%d failed=%d skipped=%d", o.Resized, o.Pending, o.Failed, o.Skipped)
}

// Total counts the pods the resize was actually attempted on.
func (o inPlaceResizeOutcome) Total() int { return o.Resized + o.Pending + o.Failed }

// resizeLivePods grows an app's RUNNING pods to a new envelope without
// restarting them, using the pod resize subresource.
//
// This exists because the git path alone cannot deliver a restart-free resize
// and never could. Rewriting the resource scalars in values.yaml changes the
// Deployment's pod template, and a pod template change always rolls a new
// ReplicaSet -- measured on this cluster, k8s v1.35.2: patching only
// spec.template.spec.containers[0].resources replaced pod uid e756a07f with
// a126e442. So the autoscaler's answer to "your app is being throttled to death
// under load" was to restart it, which for the single-replica apps this platform
// runs is a visible outage caused by the platform, at the worst possible moment.
//
// The pod resize subresource has none of that cost: the same measurement patched
// a live pod from 64Mi to 96Mi with the uid unchanged and restartCount still 0.
// ArgoCD does not undo it either -- it tracks the Deployment, Service, Ingress,
// ConfigMap and PublicApi it renders from git, and never the pods underneath, so
// a pod-level resize produces no drift for selfHeal to revert.
//
// Both directions go through here. A shrink can be deferred by the kubelet when
// the container is still using more than the new limit, and that is the correct
// outcome rather than a failure: nothing is degraded by an app holding surplus,
// so the smaller numbers simply wait for the app's next natural rollout to be
// built from git.
//
// The git commit still happens. This makes the new size take effect now; the
// commit is what makes it survive the pod being replaced for some unrelated
// reason. Neither replaces the other.
func (w *appAutoscaleWatcher) resizeLivePods(ctx context.Context, namespace, appName string, to resourceEnvelope) inPlaceResizeOutcome {
	var out inPlaceResizeOutcome

	want, err := to.requirements()
	if err != nil {
		log.Printf("app-autoscale: %s/%s in-place resize skipped, unreadable envelope %s: %v", namespace, appName, to, err)
		return out
	}
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pods, err := w.clientset.CoreV1().Pods(namespace).List(listCtx, metav1.ListOptions{
		LabelSelector: "dada.io/app=" + appName,
	})
	if err != nil {
		log.Printf("app-autoscale: %s/%s in-place resize could not list pods: %v", namespace, appName, err)
		return out
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning || pod.DeletionTimestamp != nil {
			out.Skipped++
			continue
		}
		if len(pod.Spec.Containers) != 1 {
			out.Skipped++
			continue
		}
		body, err := json.Marshal(map[string]any{
			"spec": map[string]any{
				"containers": []map[string]any{{
					"name":      pod.Spec.Containers[0].Name,
					"resources": want,
				}},
			},
		})
		if err != nil {
			out.Failed++
			continue
		}
		patchCtx, patchCancel := context.WithTimeout(ctx, 15*time.Second)
		_, err = w.clientset.CoreV1().Pods(namespace).Patch(
			patchCtx, pod.Name, types.StrategicMergePatchType, body, metav1.PatchOptions{}, "resize",
		)
		patchCancel()
		if err != nil {
			log.Printf("app-autoscale: %s/%s in-place resize of pod %s refused: %v", namespace, appName, pod.Name, err)
			out.Failed++
			continue
		}
		if w.awaitResize(ctx, namespace, pod.Name, want) {
			out.Resized++
		} else {
			out.Pending++
		}
	}
	return out
}

// awaitResize waits for the kubelet to report the new envelope in the pod
// status, which is the only signal that the resize was actuated rather than
// merely recorded. A false return means the resize is still pending -- deferred
// behind node capacity, most often -- not that it failed.
func (w *appAutoscaleWatcher) awaitResize(ctx context.Context, namespace, pod string, want corev1.ResourceRequirements) bool {
	deadline := time.Now().Add(inPlaceResizeSettleTimeout)
	for {
		getCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		p, err := w.clientset.CoreV1().Pods(namespace).Get(getCtx, pod, metav1.GetOptions{})
		cancel()
		if err == nil && len(p.Status.ContainerStatuses) == 1 {
			if requirementsMatch(p.Status.ContainerStatuses[0].Resources, want) {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
}

// requirementsMatch reports whether an actuated container carries exactly the
// requested envelope. Quantity comparison is by value, so 1Gi and 1024Mi match.
func requirementsMatch(got *corev1.ResourceRequirements, want corev1.ResourceRequirements) bool {
	if got == nil {
		return false
	}
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		wl, ok := want.Limits[name]
		if ok {
			gl, has := got.Limits[name]
			if !has || gl.Cmp(wl) != 0 {
				return false
			}
		}
		wr, ok := want.Requests[name]
		if ok {
			gr, has := got.Requests[name]
			if !has || gr.Cmp(wr) != 0 {
				return false
			}
		}
	}
	return true
}

// exceeds reports whether this envelope is bigger than other in either CPU or
// memory limit. Unparseable numbers answer false: a resize is never worth
// attempting on a size nobody can read.
func (e resourceEnvelope) exceeds(other resourceEnvelope) bool {
	for _, pair := range [][2]string{
		{e.CPULimit, other.CPULimit},
		{e.MemoryLimit, other.MemoryLimit},
	} {
		mine, err := resource.ParseQuantity(pair[0])
		if err != nil {
			continue
		}
		theirs, err := resource.ParseQuantity(pair[1])
		if err != nil {
			continue
		}
		if mine.Cmp(theirs) > 0 {
			return true
		}
	}
	return false
}

// requirements turns an envelope into the k8s shape. Ephemeral storage is
// deliberately left out: it is not resizable in place, and including it makes
// the kubelet reject the whole patch.
func (e resourceEnvelope) requirements() (corev1.ResourceRequirements, error) {
	out := corev1.ResourceRequirements{
		Limits:   corev1.ResourceList{},
		Requests: corev1.ResourceList{},
	}
	for _, f := range []struct {
		raw  string
		into corev1.ResourceList
		name corev1.ResourceName
	}{
		{e.CPULimit, out.Limits, corev1.ResourceCPU},
		{e.MemoryLimit, out.Limits, corev1.ResourceMemory},
		{e.CPUReq, out.Requests, corev1.ResourceCPU},
		{e.MemoryReq, out.Requests, corev1.ResourceMemory},
	} {
		if f.raw == "" {
			return corev1.ResourceRequirements{}, fmt.Errorf("envelope is missing %s", f.name)
		}
		q, err := resource.ParseQuantity(f.raw)
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("parse %s %q: %w", f.name, f.raw, err)
		}
		f.into[f.name] = q
	}
	return out, nil
}

// patchDeploymentTemplateEnvelope writes an envelope straight into a
// Deployment's pod template, for the one case where nothing else can deliver
// it: an app with no pod at all.
//
// The git commit is normally what makes a size survive, and for a running app
// that is enough. It is not enough for an app wedged at FailedCreate, for two
// compounding reasons measured on this cluster:
//
//   - there is no pod, so resizeLivePods has nothing to patch (in_place
//     resized=0 pending=0 failed=0 skipped=0);
//   - the app's ArgoCD Application declares
//     ignoreDifferences[apps/Deployment].jqPathExpressions =
//     ".spec.template.spec.containers[].resources", so a values.yaml carrying
//     the corrected numbers produces no drift. ArgoCD reports Synced against
//     the very revision that fixes the app and applies nothing. That exclusion
//     is deliberate -- it is what stops selfHeal from reverting an in-place
//     resize -- but it also means the git path cannot reach a template.
//
// The result was a deadlock with no exit: no pod to resize, no sync to apply
// the fix, and the template keeps the oversized numbers that admission is
// rejecting. fonbet-value sat in it for two days.
//
// Only the repair path calls this, and only for an app with zero ready
// replicas, so the rollout it triggers costs nothing: there is no running pod
// to disturb. The patch is deliberately narrow -- one container's resources --
// so it cannot become a general-purpose Deployment writer.
func (w *appAutoscaleWatcher) patchDeploymentTemplateEnvelope(ctx context.Context, namespace, appName string, to resourceEnvelope) error {
	want, err := to.requirements()
	if err != nil {
		return fmt.Errorf("unreadable envelope %s: %w", to, err)
	}
	dep, err := w.singleAppDeployment(ctx, namespace, appName)
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []map[string]any{{
						"name":      dep.Spec.Template.Spec.Containers[0].Name,
						"resources": want,
					}},
				},
			},
		},
	})
	if err != nil {
		return err
	}
	patchCtx, patchCancel := context.WithTimeout(ctx, 15*time.Second)
	defer patchCancel()
	if _, err := w.clientset.AppsV1().Deployments(namespace).Patch(
		patchCtx, dep.Name, types.StrategicMergePatchType, body, metav1.PatchOptions{},
	); err != nil {
		return fmt.Errorf("patch deployment %s: %w", dep.Name, err)
	}
	return nil
}

// singleAppDeployment resolves an app to the one Deployment that carries its
// workload. Anything other than exactly one Deployment with exactly one
// container is an error rather than a best guess: the callers here write to
// what they find.
func (w *appAutoscaleWatcher) singleAppDeployment(ctx context.Context, namespace, appName string) (*appsv1.Deployment, error) {
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	deps, err := w.clientset.AppsV1().Deployments(namespace).List(listCtx, metav1.ListOptions{
		LabelSelector: "dada.io/app=" + appName,
	})
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	if len(deps.Items) != 1 {
		return nil, fmt.Errorf("expected exactly one Deployment labelled dada.io/app=%s, found %d", appName, len(deps.Items))
	}
	dep := &deps.Items[0]
	if len(dep.Spec.Template.Spec.Containers) != 1 {
		return nil, fmt.Errorf("expected exactly one container in %s, found %d", dep.Name, len(dep.Spec.Template.Spec.Containers))
	}
	return dep, nil
}

// envelopeFromRequirements reads a container's declared resources back into an
// envelope so the same clamping arithmetic can be applied to a live Deployment
// template as to a committed envelope. Ephemeral storage is not carried: this
// envelope only ever feeds clampToLimitRange and requirements(), and neither
// touches it.
func envelopeFromRequirements(r corev1.ResourceRequirements) (resourceEnvelope, bool) {
	out := resourceEnvelope{}
	for _, f := range []struct {
		from corev1.ResourceList
		name corev1.ResourceName
		into *string
	}{
		{r.Limits, corev1.ResourceCPU, &out.CPULimit},
		{r.Limits, corev1.ResourceMemory, &out.MemoryLimit},
		{r.Requests, corev1.ResourceCPU, &out.CPUReq},
		{r.Requests, corev1.ResourceMemory, &out.MemoryReq},
	} {
		q, ok := f.from[f.name]
		if !ok {
			return resourceEnvelope{}, false
		}
		*f.into = q.String()
	}
	return out, true
}

// repairWedgedDeploymentTemplate clamps a Deployment's own pod template down to
// its namespace LimitRange when the app has no pod at all.
//
// This is a separate pass from repairAppLimitRangeViolation on purpose, because
// the two read different sources and the committed one heals first.
// repairAppLimitRangeViolation clamps the envelope stored in the database and
// commits it to git; from the next tick onwards that envelope no longer exceeds
// the LimitRange, so clampToLimitRange reports no movement and the function
// returns before doing anything. The live Deployment template, meanwhile, still
// carries the oversized numbers -- git cannot deliver them, because the app's
// ArgoCD Application excludes container resources from its diff, and the
// in-place path cannot either, because there is no pod. Left to those two, an
// app is repaired in the database and stays down in the cluster forever, which
// is exactly what fonbet-value did.
//
// The zero-pod condition is the safety property, and it is stricter than the
// readiness check the committed path uses: a template patch rolls a new
// ReplicaSet, so it must never run against an app that has any pod, running or
// pending. An app with no pod has nothing to disturb, and the rollout it
// triggers is the point.
func (w *appAutoscaleWatcher) repairWedgedDeploymentTemplate(ctx context.Context, projectID uuid.UUID, namespace, appName string, max corev1.ResourceList) {
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pods, err := w.clientset.CoreV1().Pods(namespace).List(listCtx, metav1.ListOptions{
		LabelSelector: "dada.io/app=" + appName,
	})
	if err != nil {
		log.Printf("app-autoscale: %s/%s template repair could not list pods: %v", namespace, appName, err)
		return
	}
	if len(pods.Items) > 0 {
		return
	}

	dep, err := w.singleAppDeployment(ctx, namespace, appName)
	if err != nil {
		return
	}
	cur, readable := envelopeFromRequirements(dep.Spec.Template.Spec.Containers[0].Resources)
	if !readable {
		return
	}
	to, moved, err := clampToLimitRange(cur, max)
	if err != nil {
		log.Printf("app-autoscale: %s/%s has an unreadable Deployment template envelope (%s): %v", namespace, appName, cur, err)
		return
	}
	if !moved {
		return
	}

	if err := w.patchDeploymentTemplateEnvelope(ctx, namespace, appName, to); err != nil {
		log.Printf("app-autoscale: %s/%s has no pod and a template above its LimitRange (%s), correcting it to %s failed: %v", namespace, appName, cur, to, err)
		return
	}
	log.Printf("app-autoscale: corrected the Deployment template of %s/%s %s -> %s (no pod exists, admission was rejecting every create)", namespace, appName, cur, to)

	w.h.recordSystemAudit(ctx, auditEntry{
		ProjectID:    projectID,
		Action:       auditActionAutoscaleApp,
		ResourceKind: "App",
		ResourceName: appName,
		Outcome:      auditOutcomeSuccess,
		Metadata: map[string]any{
			"repair":              "deployment_template_limitrange",
			"from_envelope":       cur.String(),
			"to_envelope":         to.String(),
			"live_pods":           0,
			"namespace":           namespace,
			"claimed_by":          "app-autoscale-watcher",
			"delivered_by":        "deployment_template_patch",
			"delivery_bypasses":   "argocd_ignoredifferences_on_container_resources",
			"delivery_bypass_why": "no pod to resize in place and no diff for argocd to apply",
		},
	})
}
