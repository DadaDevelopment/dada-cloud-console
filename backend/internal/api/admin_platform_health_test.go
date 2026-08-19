package api

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func int32Ptr(v int32) *int32 { return &v }

func healthyPlatformPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "argocd-prod",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: name + "-7d9f"},
			},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func healthyPlatformDeployment(name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "argocd-prod",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
		},
		Spec:   appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
}

func TestPlatformHealthHealthyNamespaceObservesAndReportsNothing(t *testing.T) {
	h := &Handler{}
	cs := k8sfake.NewSimpleClientset(
		healthyPlatformPod("gitops-agent"),
		healthyPlatformDeployment("gitops-agent"),
	)

	got := h.overviewPlatformHealth(context.Background(), cs, []string{"argocd-prod"})

	if !got.Observed {
		t.Fatalf("Observed = false (reason %q), want true on a reachable namespace", got.UnavailableReason)
	}
	if len(got.Unhealthy) != 0 {
		t.Fatalf("Unhealthy = %+v, want empty on a healthy namespace", got.Unhealthy)
	}
	if got.PodsTotal != 1 || got.WorkloadsTotal != 1 {
		t.Fatalf("PodsTotal/WorkloadsTotal = %d/%d, want 1/1", got.PodsTotal, got.WorkloadsTotal)
	}
}

func TestPlatformHealthReportsCrashloopingGitopsAgent(t *testing.T) {
	h := &Handler{}
	pod := healthyPlatformPod("gitops-agent")
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:         "gitops-agent",
		RestartCount: 37,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
			Reason:  "CrashLoopBackOff",
			Message: "back-off 5m0s restarting failed container",
		}},
	}}
	cs := k8sfake.NewSimpleClientset(pod)

	got := h.overviewPlatformHealth(context.Background(), cs, []string{"argocd-prod"})

	if !got.Observed {
		t.Fatalf("Observed = false (reason %q), want true", got.UnavailableReason)
	}
	if len(got.Unhealthy) != 1 {
		t.Fatalf("Unhealthy = %+v, want exactly the crashlooping pod", got.Unhealthy)
	}
	u := got.Unhealthy[0]
	if u.Kind != "Pod" || u.Name != "gitops-agent" || u.Reason != "CrashLoopBackOff" {
		t.Fatalf("Unhealthy[0] = %+v, want Pod/gitops-agent/CrashLoopBackOff", u)
	}
	if u.Restarts != 37 {
		t.Fatalf("Restarts = %d, want 37", u.Restarts)
	}
}

func TestPlatformHealthReportsWorkloadWithZeroPods(t *testing.T) {
	h := &Handler{}
	d := healthyPlatformDeployment("build-agent")
	d.Status.ReadyReplicas = 0
	cs := k8sfake.NewSimpleClientset(d)

	got := h.overviewPlatformHealth(context.Background(), cs, []string{"argocd-prod"})

	if !got.Observed {
		t.Fatalf("Observed = false (reason %q), want true", got.UnavailableReason)
	}
	if len(got.Unhealthy) != 1 {
		t.Fatalf("Unhealthy = %+v, want the admission-blocked deployment", got.Unhealthy)
	}
	u := got.Unhealthy[0]
	if u.Kind != "Deployment" || u.Name != "build-agent" || u.Reason != "NoPodsCreated" {
		t.Fatalf("Unhealthy[0] = %+v, want Deployment/build-agent/NoPodsCreated", u)
	}
	if u.DesiredReplicas != 1 || u.ReadyReplicas != 0 {
		t.Fatalf("replicas = %d/%d, want ready 0 desired 1", u.ReadyReplicas, u.DesiredReplicas)
	}
}

func TestPlatformHealthRollingUpdateIsNotReportedAsBroken(t *testing.T) {
	h := &Handler{}
	d := healthyPlatformDeployment("dada-cloud-console-backend")
	d.Spec.Replicas = int32Ptr(2)
	d.Status.ReadyReplicas = 1
	d.Status.Conditions = []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue},
		{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue, Reason: "ReplicaSetUpdated"},
	}
	pod := healthyPlatformPod("dada-cloud-console-backend")
	cs := k8sfake.NewSimpleClientset(d, pod)

	got := h.overviewPlatformHealth(context.Background(), cs, []string{"argocd-prod"})

	if len(got.Unhealthy) != 0 {
		t.Fatalf("Unhealthy = %+v, want empty: a rolling update that still holds minimum availability is not a broken platform", got.Unhealthy)
	}
}

func TestPlatformHealthStuckRolloutIsReported(t *testing.T) {
	h := &Handler{}
	d := healthyPlatformDeployment("dada-cloud-console-backend")
	d.Spec.Replicas = int32Ptr(2)
	d.Status.ReadyReplicas = 0
	d.Status.Conditions = []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse, Reason: "MinimumReplicasUnavailable"},
	}
	pod := healthyPlatformPod("dada-cloud-console-backend")
	cs := k8sfake.NewSimpleClientset(d, pod)

	got := h.overviewPlatformHealth(context.Background(), cs, []string{"argocd-prod"})

	if len(got.Unhealthy) != 1 {
		t.Fatalf("Unhealthy = %+v, want the unavailable deployment", got.Unhealthy)
	}
	if got.Unhealthy[0].Reason != "ReplicasNotReady" {
		t.Fatalf("Reason = %q, want ReplicasNotReady", got.Unhealthy[0].Reason)
	}
}

func TestPlatformHealthListErrorIsBlindnessNotHealth(t *testing.T) {
	h := &Handler{}
	cs := k8sfake.NewSimpleClientset()
	cs.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})

	got := h.overviewPlatformHealth(context.Background(), cs, []string{"argocd-prod"})

	if got.Observed {
		t.Fatalf("Observed = true on a failed list: blindness must never read as health (%+v)", got)
	}
	if strings.TrimSpace(got.UnavailableReason) == "" {
		t.Fatalf("UnavailableReason empty, want the list failure named")
	}
	if len(got.Unhealthy) != 0 {
		t.Fatalf("Unhealthy = %+v, want empty when nothing could be observed", got.Unhealthy)
	}
}

func TestPlatformHealthNilClientsetIsBlind(t *testing.T) {
	h := &Handler{}

	got := h.overviewPlatformHealth(context.Background(), nil, []string{"argocd-prod"})

	if got.Observed || got.UnavailableReason == "" {
		t.Fatalf("got = %+v, want Observed=false with a reason off-cluster", got)
	}
}
