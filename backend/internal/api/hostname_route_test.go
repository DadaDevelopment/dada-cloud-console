package api

import (
	"context"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/dada-tuda/console/backend/internal/models"
)

// TestIngressRouteMatchesHost is the regression guard for the route-check
// itself: it must match a rule with the exact host, ignore unrelated rules
// (including empty-host default-backend rules), and cope with an Ingress
// that has no rules at all rather than panicking on it.
func TestIngressRouteMatchesHost(t *testing.T) {
	cases := []struct {
		name     string
		rules    []networkingv1.IngressRule
		hostname string
		want     bool
	}{
		{
			name:     "exact match",
			rules:    []networkingv1.IngressRule{{Host: "app.dada-tuda.ru"}},
			hostname: "app.dada-tuda.ru",
			want:     true,
		},
		{
			name:     "no match",
			rules:    []networkingv1.IngressRule{{Host: "other.dada-tuda.ru"}},
			hostname: "app.dada-tuda.ru",
			want:     false,
		},
		{
			name: "multiple rules, match is not first",
			rules: []networkingv1.IngressRule{
				{Host: "unrelated.dada-tuda.ru"},
				{Host: ""},
				{Host: "app.dada-tuda.ru"},
			},
			hostname: "app.dada-tuda.ru",
			want:     true,
		},
		{
			name:     "wildcard rule matches one label deep",
			rules:    []networkingv1.IngressRule{{Host: "*.pv.dada-tuda.ru"}},
			hostname: "webapp-pr-1.pv.dada-tuda.ru",
			want:     true,
		},
		{
			name:     "wildcard rule does not match two labels deep",
			rules:    []networkingv1.IngressRule{{Host: "*.dada-tuda.ru"}},
			hostname: "webapp-pr-1.pv.dada-tuda.ru",
			want:     false,
		},
		{
			name:     "wildcard rule does not match the bare suffix",
			rules:    []networkingv1.IngressRule{{Host: "*.dada-tuda.ru"}},
			hostname: "dada-tuda.ru",
			want:     false,
		},
		{
			name:     "empty rule list",
			rules:    nil,
			hostname: "app.dada-tuda.ru",
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ingressRouteMatchesHost(tc.rules, tc.hostname); got != tc.want {
				t.Errorf("ingressRouteMatchesHost(%v, %q) = %v, want %v", tc.rules, tc.hostname, got, tc.want)
			}
		})
	}
}

// TestHostnameRouteLive exercises the kube-API lookup against a fake
// clientset: a matching Ingress reports live, no matching Ingress reports
// not-live-but-known, and a nil clientset (off-cluster, the state every local
// dev run and every other test in this package runs in) reports unknown
// rather than "not live" -- the whole point of the known/live split is that
// "no client" must never be read as "no route".
func TestHostnameRouteLive(t *testing.T) {
	ns := "proj-ns"
	host := "magic-mirror-7679ef.dada-tuda.ru"

	t.Run("matching ingress reports live", func(t *testing.T) {
		cs := fake.NewSimpleClientset(&networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: ns},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{{Host: host}},
			},
		})
		live, known := hostnameRouteLive(context.Background(), cs, host)
		if !known || !live {
			t.Fatalf("live=%v known=%v, want live=true known=true", live, known)
		}
	})

	t.Run("no matching ingress reports not live but known", func(t *testing.T) {
		cs := fake.NewSimpleClientset(&networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: ns},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{{Host: "some-other-app.dada-tuda.ru"}},
			},
		})
		live, known := hostnameRouteLive(context.Background(), cs, host)
		if !known || live {
			t.Fatalf("live=%v known=%v, want live=false known=true", live, known)
		}
	})

	t.Run("wildcard ingress in another namespace routes the name", func(t *testing.T) {
		cs := fake.NewSimpleClientset(&networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: "preview", Namespace: "argocd-prod"},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{{Host: "*.pv.dada-tuda.ru"}},
			},
		})
		live, known := hostnameRouteLive(context.Background(), cs, "webapp-pr-1.pv.dada-tuda.ru")
		if !known || !live {
			t.Fatalf("live=%v known=%v, want live=true known=true", live, known)
		}
	})

	t.Run("nil clientset reports unknown, not absent", func(t *testing.T) {
		live, known := hostnameRouteLive(context.Background(), nil, host)
		if known || live {
			t.Fatalf("live=%v known=%v, want live=false known=false", live, known)
		}
	})
}

// TestHostnameCertRouteDecision is the reconciler's own branching table: a
// cert probe that already passed must still not flip the row active unless
// the route check also confirms it, must land on route_missing (not silently
// stay pending with the old cert_pending reason) when the route check
// affirmatively found nothing, and must change NOTHING when the route check
// itself could not answer -- a kube-API blip must never read as "route gone".
// VM runtime skips the route check entirely (rule 4 of the spec): a cert pass
// there is enough, matching pre-existing behaviour.
func TestHostnameCertRouteDecision(t *testing.T) {
	cases := []struct {
		name       string
		runtime    models.EnvironmentRuntime
		routeKnown bool
		routeLive  bool
		want       hostnameCertRouteOutcome
	}{
		{
			name:       "k8s cert live and route live goes active",
			runtime:    models.EnvironmentRuntimeK8s,
			routeKnown: true,
			routeLive:  true,
			want:       hostnameOutcomeActive,
		},
		{
			name:       "k8s cert live but route missing stays pending with reason",
			runtime:    models.EnvironmentRuntimeK8s,
			routeKnown: true,
			routeLive:  false,
			want:       hostnameOutcomeRouteMissing,
		},
		{
			name:       "k8s route check unknown (kube-API error) changes nothing",
			runtime:    models.EnvironmentRuntimeK8s,
			routeKnown: false,
			routeLive:  false,
			want:       hostnameOutcomeUnknown,
		},
		{
			name:       "vm runtime skips the route check and goes active on cert alone",
			runtime:    models.EnvironmentRuntimeVM,
			routeKnown: false,
			routeLive:  false,
			want:       hostnameOutcomeActive,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hostnameCertRouteDecision(tc.runtime, tc.routeKnown, tc.routeLive)
			if got != tc.want {
				t.Errorf("hostnameCertRouteDecision(%v, known=%v, live=%v) = %v, want %v",
					tc.runtime, tc.routeKnown, tc.routeLive, got, tc.want)
			}
		})
	}
}
