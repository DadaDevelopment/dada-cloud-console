package api

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// platformPodNotReadyGrace is how long a pod may sit not-Ready before it is
// reported. Mirrors a normal container-start window (image pull + readiness
// probe warmup) so a pod mid-rollout is not flagged the instant it appears.
const platformPodNotReadyGrace = 2 * time.Minute

// platformHealthListTimeout caps the k8s reads this section adds to the admin
// overview request. A sick API server must make the panel say it is blind,
// not make the whole overview hang past its latency budget.
const platformHealthListTimeout = 3 * time.Second

// platformUnhealthy is one platform pod or workload the overview's
// platform_health section can currently PROVE is broken. All fields carry
// json tags -- a tag-less struct has silently broken the console before
// (see project_structs_serialized_without_json_tags_break_console_silently).
type platformUnhealthy struct {
	Namespace       string `json:"namespace"`
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	Workload        string `json:"workload,omitempty"`
	Phase           string `json:"phase,omitempty"`
	Ready           bool   `json:"ready"`
	Restarts        int32  `json:"restarts"`
	Reason          string `json:"reason,omitempty"`
	Message         string `json:"message,omitempty"`
	AgeSeconds      int64  `json:"age_seconds"`
	ReadyReplicas   int32  `json:"ready_replicas"`
	DesiredReplicas int32  `json:"desired_replicas"`
}

// platformHealth is the admin overview's answer for "is the platform's own
// delivery machinery (gitops-agent, build-agent, and friends) alive". It is
// deliberately NOT a simple bool: a crashlooping platform pod and an
// inability to even ASK the question must never collapse into the same
// green/red signal, so Observed=false always carries a reason and is never
// paired with a silently-empty Unhealthy list.
type platformHealth struct {
	Observed          bool                `json:"observed"`
	UnavailableReason string              `json:"unavailable_reason,omitempty"`
	CheckedAt         time.Time           `json:"checked_at"`
	Namespaces        []string            `json:"namespaces"`
	PodsTotal         int                 `json:"pods_total"`
	WorkloadsTotal    int                 `json:"workloads_total"`
	Unhealthy         []platformUnhealthy `json:"unhealthy"`
}

// overviewPlatformHealth polls the given namespaces for the platform's own
// pods and Deployment/StatefulSet/DaemonSet workloads. Pure over an injected
// kubernetes.Interface so tests run against k8sfake without a real cluster.
//
// Blindness must never read as health: cs == nil or any list call failing
// returns Observed=false with a reason, never an empty Unhealthy list dressed
// up as Observed=true.
func (h *Handler) overviewPlatformHealth(ctx context.Context, cs kubernetes.Interface, namespaces []string) platformHealth {
	out := platformHealth{
		CheckedAt:  time.Now().UTC(),
		Namespaces: namespaces,
		Unhealthy:  []platformUnhealthy{},
	}
	if cs == nil {
		out.UnavailableReason = "no in-cluster client"
		return out
	}
	if len(namespaces) == 0 {
		out.Observed = true
		return out
	}

	ctx, cancel := context.WithTimeout(ctx, platformHealthListTimeout)
	defer cancel()

	var allPods []corev1.Pod
	var allDeployments []appsv1.Deployment
	var allStatefulSets []appsv1.StatefulSet
	var allDaemonSets []appsv1.DaemonSet

	for _, ns := range namespaces {
		pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			out.UnavailableReason = "failed to list pods in " + ns + ": " + err.Error()
			return out
		}
		allPods = append(allPods, pods.Items...)

		deployments, err := cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			out.UnavailableReason = "failed to list deployments in " + ns + ": " + err.Error()
			return out
		}
		allDeployments = append(allDeployments, deployments.Items...)

		statefulSets, err := cs.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			out.UnavailableReason = "failed to list statefulsets in " + ns + ": " + err.Error()
			return out
		}
		allStatefulSets = append(allStatefulSets, statefulSets.Items...)

		daemonSets, err := cs.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			out.UnavailableReason = "failed to list daemonsets in " + ns + ": " + err.Error()
			return out
		}
		allDaemonSets = append(allDaemonSets, daemonSets.Items...)
	}

	out.Observed = true
	out.PodsTotal = len(allPods)
	out.WorkloadsTotal = len(allDeployments) + len(allStatefulSets) + len(allDaemonSets)

	now := out.CheckedAt
	podCountByWorkload := map[string]int{}

	for _, pod := range allPods {
		if u := unhealthyPod(pod, now); u != nil {
			out.Unhealthy = append(out.Unhealthy, *u)
		}
		if owner := podWorkloadKey(pod); owner != "" {
			podCountByWorkload[owner]++
		}
	}

	for _, d := range allDeployments {
		key := d.Namespace + "/Deployment/" + d.Name
		if u := unhealthyWorkload(d.Namespace, "Deployment", d.Name, d.Spec.Replicas, d.Status.ReadyReplicas, podCountByWorkload[key], now, d.CreationTimestamp.Time, deploymentHoldsMinimumAvailability(d)); u != nil {
			out.Unhealthy = append(out.Unhealthy, *u)
		}
	}
	for _, s := range allStatefulSets {
		key := s.Namespace + "/StatefulSet/" + s.Name
		if u := unhealthyWorkload(s.Namespace, "StatefulSet", s.Name, s.Spec.Replicas, s.Status.ReadyReplicas, podCountByWorkload[key], now, s.CreationTimestamp.Time, false); u != nil {
			out.Unhealthy = append(out.Unhealthy, *u)
		}
	}
	for _, ds := range allDaemonSets {
		key := ds.Namespace + "/DaemonSet/" + ds.Name
		desired := ds.Status.DesiredNumberScheduled
		if u := unhealthyWorkload(ds.Namespace, "DaemonSet", ds.Name, &desired, ds.Status.NumberReady, podCountByWorkload[key], now, ds.CreationTimestamp.Time, false); u != nil {
			out.Unhealthy = append(out.Unhealthy, *u)
		}
	}

	return out
}

// podWorkloadKey identifies the owning workload of a pod as
// "<namespace>/<Kind>/<name>", matching a ReplicaSet-owned pod back up to its
// Deployment (the pod's direct owner is the ReplicaSet, not the Deployment)
// and a StatefulSet/DaemonSet pod straight to its owner. Empty when the pod
// has no recognized workload owner.
func podWorkloadKey(pod corev1.Pod) string {
	for _, ref := range pod.OwnerReferences {
		switch ref.Kind {
		case "StatefulSet", "DaemonSet":
			return pod.Namespace + "/" + ref.Kind + "/" + ref.Name
		case "ReplicaSet":
			if name := deploymentNameFromReplicaSet(ref.Name); name != "" {
				return pod.Namespace + "/Deployment/" + name
			}
		}
	}
	return ""
}

// deploymentNameFromReplicaSet strips the trailing "-<hash>" a ReplicaSet
// name carries over its owning Deployment's name (standard k8s naming).
// Returns "" when the name has no hyphen to strip.
func deploymentNameFromReplicaSet(rsName string) string {
	idx := -1
	for i := len(rsName) - 1; i >= 0; i-- {
		if rsName[i] == '-' {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return ""
	}
	return rsName[:idx]
}

// unhealthyPod reports a pod when it holds any of: phase Failed; phase
// Pending; not Ready for longer than platformPodNotReadyGrace while
// phase != Succeeded; a waiting container with a known bad reason; or a
// terminated container whose last exit was OOMKilled.
func unhealthyPod(pod corev1.Pod, now time.Time) *platformUnhealthy {
	age := now.Sub(pod.CreationTimestamp.Time)
	restarts := int32(0)
	for _, cs := range pod.Status.ContainerStatuses {
		restarts += cs.RestartCount
	}

	base := platformUnhealthy{
		Namespace:  pod.Namespace,
		Kind:       "Pod",
		Name:       pod.Name,
		Workload:   podWorkloadKey(pod),
		Phase:      string(pod.Status.Phase),
		Restarts:   restarts,
		AgeSeconds: int64(age.Seconds()),
	}

	switch pod.Status.Phase {
	case corev1.PodFailed:
		base.Reason = "Failed"
		base.Message = pod.Status.Reason
		return &base
	case corev1.PodPending:
		base.Reason = "Pending"
		base.Message = pod.Status.Reason
		return &base
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError":
				base.Reason = cs.State.Waiting.Reason
				base.Message = cs.State.Waiting.Message
				return &base
			}
		}
		if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
			base.Reason = "OOMKilled"
			base.Message = cs.LastTerminationState.Terminated.Message
			return &base
		}
	}

	if pod.Status.Phase != corev1.PodSucceeded && !podIsReady(&pod) && age > platformPodNotReadyGrace {
		base.Reason = "NotReady"
		return &base
	}

	base.Ready = podIsReady(&pod)
	return nil
}

// unhealthyWorkload reports a Deployment/StatefulSet/DaemonSet when its
// ready replica count falls short of desired, INCLUDING the class where an
// admission-blocked spec (e.g. a rejected pod template) yields zero pods and
// therefore no Pod object at all to catch via unhealthyPod -- see
// project_admin_broken_panel_read_health_from_own_blindness /
// FailedCreate-class events, where the platform never even attempted a pod.
func unhealthyWorkload(namespace, kind, name string, desiredReplicas *int32, readyReplicas int32, podCount int, now time.Time, createdAt time.Time, holdsMinimumAvailability bool) *platformUnhealthy {
	desired := int32(1)
	if desiredReplicas != nil {
		desired = *desiredReplicas
	}
	if desired <= 0 {
		return nil
	}
	if readyReplicas >= desired {
		return nil
	}
	if podCount > 0 && holdsMinimumAvailability {
		return nil
	}

	u := &platformUnhealthy{
		Namespace:       namespace,
		Kind:            kind,
		Name:            name,
		ReadyReplicas:   readyReplicas,
		DesiredReplicas: desired,
		AgeSeconds:      int64(now.Sub(createdAt).Seconds()),
		Reason:          "ReplicasNotReady",
	}
	if podCount == 0 {
		u.Reason = "NoPodsCreated"
		u.Message = "spec.replicas > 0 but no matching pod exists (likely admission-blocked)"
	}
	return u
}

// deploymentHoldsMinimumAvailability reports whether kubernetes itself still
// considers the Deployment available. A rolling update legitimately runs with
// ReadyReplicas below desired while Available stays true, and flagging that
// would make the panel cry wolf on every deploy; a rollout that actually lost
// its minimum flips Available to false and is reported.
func deploymentHoldsMinimumAvailability(d appsv1.Deployment) bool {
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentAvailable {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// platformHealthClientset lazily builds the in-cluster client the overview
// endpoint reads platform pods/workloads through, mirroring
// newAppHealthClientset's off-cluster => nil contract so local dev and tests
// never fail on missing service-account mounts.
func (h *Handler) platformHealthClientset() kubernetes.Interface {
	h.platformHealthOnce.Do(func() {
		h.platformHealthCS = newAppHealthClientset()
	})
	return h.platformHealthCS
}
