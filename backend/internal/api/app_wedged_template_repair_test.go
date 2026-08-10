package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// newWedgedDeployment builds the shape fonbet-value was actually wedged in: a
// Deployment whose template asks for more memory than the namespace LimitRange
// allows, so admission rejects every pod create and no pod exists.
func newWedgedDeployment(appName, memLimit, memReq string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName + "-deploy",
			Namespace: "ns",
			Labels:    map[string]string{"dada.io/app": appName},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: appName + "-container",
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2"),
								corev1.ResourceMemory: resource.MustParse(memLimit),
							},
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse(memReq),
							},
						},
					}},
				},
			},
		},
	}
}

func namespaceMax(cpu, mem string) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(mem),
	}
}

func templateMemoryLimit(t *testing.T, w *appAutoscaleWatcher, appName string) string {
	t.Helper()
	dep, err := w.clientset.AppsV1().Deployments("ns").Get(context.Background(), appName+"-deploy", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	q := dep.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]
	return q.String()
}

// TestWedgedTemplateIsClampedWhenNoPodExists is the case the committed-envelope
// path cannot reach. The database envelope has already been clamped by an
// earlier tick, so clampToLimitRange reports no movement there and it returns
// early; the Deployment template still carries the number admission rejects,
// and git cannot deliver the correction because ArgoCD excludes container
// resources from its diff.
func TestWedgedTemplateIsClampedWhenNoPodExists(t *testing.T) {
	w := &appAutoscaleWatcher{
		clientset: k8sfake.NewSimpleClientset(newWedgedDeployment("fonbet-value", "4Gi", "2Gi")),
		h:         &Handler{},
	}

	w.repairWedgedDeploymentTemplate(context.Background(), uuid.New(), "ns", "fonbet-value", namespaceMax("4", "2Gi"))

	if got := templateMemoryLimit(t, w, "fonbet-value"); got != "2Gi" {
		t.Fatalf("template memory limit = %s, want 2Gi -- the app stays wedged at FailedCreate", got)
	}
}

// TestWedgedTemplateRepairLeavesAppsWithPodsAlone pins the safety property. A
// template patch rolls a new ReplicaSet, so any existing pod -- running or
// merely pending -- makes this an outage rather than a repair.
func TestWedgedTemplateRepairLeavesAppsWithPodsAlone(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fonbet-value-abc",
			Namespace: "ns",
			Labels:    map[string]string{"dada.io/app": "fonbet-value"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
	w := &appAutoscaleWatcher{
		clientset: k8sfake.NewSimpleClientset(newWedgedDeployment("fonbet-value", "4Gi", "2Gi"), pod),
		h:         &Handler{},
	}

	w.repairWedgedDeploymentTemplate(context.Background(), uuid.New(), "ns", "fonbet-value", namespaceMax("4", "2Gi"))

	if got := templateMemoryLimit(t, w, "fonbet-value"); got != "4Gi" {
		t.Fatalf("template memory limit = %s, want 4Gi untouched -- patching a template with a live pod restarts the app", got)
	}
}

// TestWedgedTemplateRepairIsANoopWithinTheLimitRange keeps the pass idempotent:
// it runs against every app on every tick, so a template that already fits must
// produce no write at all.
func TestWedgedTemplateRepairIsANoopWithinTheLimitRange(t *testing.T) {
	client := k8sfake.NewSimpleClientset(newWedgedDeployment("fonbet-value", "1Gi", "512Mi"))
	w := &appAutoscaleWatcher{clientset: client, h: &Handler{}}

	w.repairWedgedDeploymentTemplate(context.Background(), uuid.New(), "ns", "fonbet-value", namespaceMax("4", "2Gi"))

	for _, action := range client.Actions() {
		if action.GetVerb() == "patch" {
			t.Fatalf("template within the LimitRange was patched anyway: %#v", action)
		}
	}
}
