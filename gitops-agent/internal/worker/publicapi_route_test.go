package worker

import (
	"context"
	"testing"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
)

func ingressFor(name, host, edgeIP string) *networkingv1.Ingress {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "internal-prod"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: host}},
		},
	}
	if edgeIP != "" {
		ing.Status.LoadBalancer.Ingress = []networkingv1.IngressLoadBalancerIngress{{IP: edgeIP}}
	}
	return ing
}

func publicAPI(fqdn string, gatewayRoute bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "platform.dada-tuda.ru/v1alpha1",
		"kind":       "PublicApi",
		"metadata":   map[string]any{"name": "endpoint"},
		"spec": map[string]any{
			"gatewayRoute": gatewayRoute,
			"dns":          map[string]any{"enabled": true, "fqdn": fqdn},
		},
	}}
}

// reconcilerWithRoutes builds a reconciler whose DNS answers are pre-seeded into
// the resolve cache, so a route decision is exercised without a live resolver,
// and returns the routing table built from the seeded Ingresses.
func reconcilerWithRoutes(t *testing.T, resolved map[string][]string, objs ...*networkingv1.Ingress) (*StatusReconciler, ingressRoutes) {
	t.Helper()
	client := fake.NewSimpleClientset()
	for _, o := range objs {
		if _, err := client.NetworkingV1().Ingresses(o.Namespace).Create(context.Background(), o, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed ingress: %v", err)
		}
	}
	r := &StatusReconciler{client: client, addrVerdicts: map[string]addrVerdict{}}
	for fqdn, addrs := range resolved {
		r.addrVerdicts[fqdn] = addrVerdict{addrs: addrs, at: time.Now()}
	}
	return r, r.loadIngressRoutes(context.Background())
}

func TestLoadIngressRoutesIndexesHostsWildcardsAndEdge(t *testing.T) {
	_, routes := reconcilerWithRoutes(t, nil,
		ingressFor("site", "development.dada-tuda.ru", "155.212.223.198"),
		ingressFor("previews", "*.pv.dada-tuda.ru", "155.212.223.198"),
	)

	if !routes.known {
		t.Fatal("routing table with published edge addresses must be known")
	}
	if !routes.routed("development.dada-tuda.ru") {
		t.Error("exact host must be reported as routed")
	}
	if !routes.routed("pr-42.pv.dada-tuda.ru") {
		t.Error("single-label wildcard must match")
	}
	if routes.routed("deep.pr-42.pv.dada-tuda.ru") {
		t.Error("wildcard must not match a multi-label prefix")
	}
	if routes.routed("pv.dada-tuda.ru") {
		t.Error("wildcard must not match the bare apex")
	}
	if !routes.atOurEdge([]string{"1.2.3.4", "155.212.223.198"}) {
		t.Error("published edge address must be recognised")
	}
	if routes.atOurEdge([]string{"45.84.227.218"}) {
		t.Error("foreign address must not be treated as our edge")
	}
}

func TestPublicApiRouteMissingFlagsHostWithoutIngress(t *testing.T) {
	r, routes := reconcilerWithRoutes(t,
		map[string][]string{"jira.dada-tuda.ru": {"155.212.223.198"}},
		ingressFor("other", "development.dada-tuda.ru", "155.212.223.198"),
	)

	if !r.publicApiRouteMissing(context.Background(), publicAPI("jira.dada-tuda.ru", false), routes) {
		t.Fatal("record pointing at our edge with no Ingress must be reported route-missing")
	}
}

func TestPublicApiRouteMissingSilentOnHealthyRoutedHost(t *testing.T) {
	r, routes := reconcilerWithRoutes(t,
		map[string][]string{"development.dada-tuda.ru": {"155.212.223.198"}},
		ingressFor("site", "development.dada-tuda.ru", "155.212.223.198"),
	)

	if r.publicApiRouteMissing(context.Background(), publicAPI("development.dada-tuda.ru", false), routes) {
		t.Fatal("a host an Ingress claims must never be demoted")
	}
}

func TestPublicApiRouteMissingSkipsForeignAndGatewayAndUnknown(t *testing.T) {
	resolved := map[string][]string{
		"ns1.dada-tuda.ru":                     {"159.194.204.174"},
		"cloud-backend-dada-prod.dada-tuda.ru": {"155.212.223.198"},
		"unresolvable.dada-tuda.ru":            nil,
	}
	r, routes := reconcilerWithRoutes(t, resolved,
		ingressFor("site", "development.dada-tuda.ru", "155.212.223.198"),
	)
	ctx := context.Background()

	if r.publicApiRouteMissing(ctx, publicAPI("ns1.dada-tuda.ru", false), routes) {
		t.Error("record answering from foreign infrastructure must be left alone")
	}
	if r.publicApiRouteMissing(ctx, publicAPI("cloud-backend-dada-prod.dada-tuda.ru", true), routes) {
		t.Error("gateway-routed endpoint has no Ingress of its own and must be skipped")
	}
	if r.publicApiRouteMissing(ctx, publicAPI("unresolvable.dada-tuda.ru", false), routes) {
		t.Error("a record that does not resolve is not evidence of a missing route")
	}
}

// TestPublicApiRouteMissingSilentWhenRoutingTableUnreadable pins the blip rule:
// a table with no published edge address carries no evidence at all, so a failed
// or empty Ingress list must never flip a live endpoint red.
func TestPublicApiRouteMissingSilentWhenRoutingTableUnreadable(t *testing.T) {
	r, _ := reconcilerWithRoutes(t, map[string][]string{"jira.dada-tuda.ru": {"155.212.223.198"}})

	unknown := ingressRoutes{hosts: map[string]bool{}, wildcards: map[string]bool{}, edgeAddrs: map[string]bool{}}
	if r.publicApiRouteMissing(context.Background(), publicAPI("jira.dada-tuda.ru", false), unknown) {
		t.Fatal("unknown routing table must never demote an endpoint")
	}
}
