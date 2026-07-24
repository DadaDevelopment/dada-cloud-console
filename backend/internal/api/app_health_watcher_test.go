package api

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func podWithStatus(appName string, statuses ...corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-abc12",
			Namespace: "acme-prod",
			Labels:    map[string]string{"dada.io/app": appName},
		},
		Status: corev1.PodStatus{ContainerStatuses: statuses},
	}
}

func TestDetectPodAlertCrashLoopBackOff(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:  "web",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	})
	alert, bad := detectPodAlert(pod)
	if !bad {
		t.Fatalf("expected bad state detected")
	}
	if alert.Reason != reasonCrashLoopBackOff || alert.AppName != "web" || alert.PodName != "web-abc12" {
		t.Fatalf("unexpected alert: %+v", alert)
	}
}

func TestDetectPodAlertImagePullBackOff(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:  "web",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
	})
	alert, bad := detectPodAlert(pod)
	if !bad || alert.Reason != reasonImagePullBackOff {
		t.Fatalf("expected ImagePullBackOff alert, got bad=%v alert=%+v", bad, alert)
	}
}

func TestDetectPodAlertOOMKilled(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name: "web",
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
		},
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	})
	alert, bad := detectPodAlert(pod)
	if !bad || alert.Reason != reasonOOMKilled {
		t.Fatalf("expected OOMKilled to win over CrashLoopBackOff, got bad=%v alert=%+v", bad, alert)
	}
}

func TestDetectPodAlertHealthyPodIgnored(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:  "web",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	if _, bad := detectPodAlert(pod); bad {
		t.Fatalf("expected healthy running pod to not alert")
	}
}

func TestDetectPodAlertSkipsPodsWithoutAppLabel(t *testing.T) {
	pod := podWithStatus("", corev1.ContainerStatus{
		Name:  "web",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	})
	if _, bad := detectPodAlert(pod); bad {
		t.Fatalf("expected pod without dada.io/app label to be skipped")
	}
}

func TestDetectPodAlertPendingPullNotYetBackoffIgnored(t *testing.T) {
	pod := podWithStatus("web", corev1.ContainerStatus{
		Name:  "web",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
	})
	if _, bad := detectPodAlert(pod); bad {
		t.Fatalf("expected ContainerCreating (normal transient state) to not alert")
	}
}
