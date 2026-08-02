package box

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// newExposerFixture wires an exposer over a fake API server holding one box pod
// already labelled with its control-plane name, which is the state Bind leaves.
func newExposerFixture(t *testing.T) (*ClusterExposer, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "box-w1",
			Namespace: "dada-boxes",
			Labels: map[string]string{
				labelBox:      "true",
				labelBoxID:    "w1",
				labelBoxName:  "sunny-otter",
				labelBoxPhase: phaseLive,
			},
		},
	})
	return &ClusterExposer{
		clientset:    cs,
		Namespace:    "dada-boxes",
		HostnameBase: "box.dada-tuda.ru",
		TLSSecret:    "box-wildcard-tls",
		IngressClass: "nginx",
	}, cs
}

// TestExposeOpensTheIngressPathAndTheNetworkPolicy pins the pair that has to move
// together. The namespace default-denies ingress, so a Service and an Ingress
// without the dada.io/box-exposed label produce a hostname that resolves, routes,
// and then times out — the most expensive kind of working-looking failure.
func TestExposeOpensTheIngressPathAndTheNetworkPolicy(t *testing.T) {
	e, cs := newExposerFixture(t)

	exp, err := e.Expose("sunny-otter", 3000)
	if err != nil {
		t.Fatalf("expose: %v", err)
	}
	if exp.Hostname != "sunny-otter-3000.box.dada-tuda.ru" {
		t.Errorf("hostname %q is not the platform-assigned one", exp.Hostname)
	}
	if exp.URL != "https://sunny-otter-3000.box.dada-tuda.ru" {
		t.Errorf("url %q should be https: the wildcard certificate is what makes it so", exp.URL)
	}

	svc, err := cs.CoreV1().Services("dada-boxes").Get(context.Background(), "box-sunny-otter-3000", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	if svc.Spec.Selector[labelBoxName] != "sunny-otter" {
		t.Errorf("service selects %v, which will not find the box pod", svc.Spec.Selector)
	}
	if got := svc.Spec.Ports[0].TargetPort.IntValue(); got != 3000 {
		t.Errorf("service targets port %d, want the exposed port", got)
	}

	ing, err := cs.NetworkingV1().Ingresses("dada-boxes").Get(context.Background(), "box-sunny-otter-3000", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ingress: %v", err)
	}
	if ing.Spec.Rules[0].Host != exp.Hostname {
		t.Errorf("ingress host %q does not match the reported hostname %q", ing.Spec.Rules[0].Host, exp.Hostname)
	}
	if len(ing.Spec.TLS) != 1 || ing.Spec.TLS[0].SecretName != "box-wildcard-tls" {
		t.Errorf("ingress must use the replicated wildcard certificate, got %v", ing.Spec.TLS)
	}

	pod, err := cs.CoreV1().Pods("dada-boxes").Get(context.Background(), "box-w1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod: %v", err)
	}
	if pod.Labels[labelBoxExposed] != "true" {
		t.Error("expose did not label the pod, so the ingress NetworkPolicy still denies the edge")
	}
}

// TestExposePublishesWithNoindex pins the pair that keeps a temporary hostname
// out of search results: the ConfigMap holding the header and the annotation that
// points at it. Either one alone is a silent failure — an unreferenced ConfigMap
// sets no header, and an annotation naming a missing ConfigMap makes the hostname
// answer 503 instead of serving the box.
func TestExposePublishesWithNoindex(t *testing.T) {
	e, cs := newExposerFixture(t)

	if _, err := e.Expose("sunny-otter", 3000); err != nil {
		t.Fatalf("expose: %v", err)
	}

	cm, err := cs.CoreV1().ConfigMaps("dada-boxes").Get(context.Background(), noindexConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("noindex configmap: %v", err)
	}
	if cm.Data["X-Robots-Tag"] != noindexHeader {
		t.Errorf("configmap carries %q, want %q", cm.Data["X-Robots-Tag"], noindexHeader)
	}

	ing, err := cs.NetworkingV1().Ingresses("dada-boxes").Get(context.Background(), "box-sunny-otter-3000", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("ingress: %v", err)
	}
	if got := ing.Annotations["nginx.ingress.kubernetes.io/custom-headers"]; got != "dada-boxes/"+noindexConfigMap {
		t.Errorf("ingress points at %q, want the namespaced noindex configmap", got)
	}
}

// TestExposeIsIdempotent covers the retry. A caller that repeats expose after a
// timeout must not get a 500 for a box that is already published.
func TestExposeIsIdempotent(t *testing.T) {
	e, _ := newExposerFixture(t)
	first, err := e.Expose("sunny-otter", 3000)
	if err != nil {
		t.Fatalf("first expose: %v", err)
	}
	second, err := e.Expose("sunny-otter", 3000)
	if err != nil {
		t.Fatalf("second expose: %v", err)
	}
	if first.Hostname != second.Hostname {
		t.Errorf("a repeated expose changed the hostname: %q then %q", first.Hostname, second.Hostname)
	}
}

// TestUnexposeClosesTheDoorOnlyWhenTheLastPortGoes checks the part that is easy
// to get wrong: a box with two published ports must stay reachable after one is
// removed, and must stop being reachable once none are left.
func TestUnexposeClosesTheDoorOnlyWhenTheLastPortGoes(t *testing.T) {
	e, cs := newExposerFixture(t)
	if _, err := e.Expose("sunny-otter", 3000); err != nil {
		t.Fatalf("expose 3000: %v", err)
	}
	if _, err := e.Expose("sunny-otter", 8080); err != nil {
		t.Fatalf("expose 8080: %v", err)
	}

	if err := e.Unexpose("sunny-otter-3000.box.dada-tuda.ru"); err != nil {
		t.Fatalf("unexpose 3000: %v", err)
	}
	pod, _ := cs.CoreV1().Pods("dada-boxes").Get(context.Background(), "box-w1", metav1.GetOptions{})
	if pod.Labels[labelBoxExposed] != "true" {
		t.Error("removing one of two exposures cut the box off from the edge")
	}

	if err := e.Unexpose("sunny-otter-8080.box.dada-tuda.ru"); err != nil {
		t.Fatalf("unexpose 8080: %v", err)
	}
	pod, _ = cs.CoreV1().Pods("dada-boxes").Get(context.Background(), "box-w1", metav1.GetOptions{})
	if _, still := pod.Labels[labelBoxExposed]; still {
		t.Error("the last exposure went but the pod is still open to the edge")
	}
	if _, err := cs.NetworkingV1().Ingresses("dada-boxes").Get(context.Background(), "box-sunny-otter-8080", metav1.GetOptions{}); err == nil {
		t.Error("unexpose left the ingress behind")
	}
}

// TestUnexposeRefusesAHostnameItDoesNotOwn keeps a stored hostname from becoming
// a delete primitive against arbitrary objects in the namespace.
func TestUnexposeRefusesAHostnameItDoesNotOwn(t *testing.T) {
	e, _ := newExposerFixture(t)
	for _, hostname := range []string{
		"console.dada-tuda.ru",
		"sunny-otter-3000.box.evil.example",
		"box.dada-tuda.ru",
		"sunny-otter.box.dada-tuda.ru",
	} {
		if err := e.Unexpose(hostname); err == nil {
			t.Errorf("Unexpose accepted %q", hostname)
		}
	}
}
