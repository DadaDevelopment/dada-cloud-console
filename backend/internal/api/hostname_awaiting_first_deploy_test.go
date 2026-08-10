package api

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// withRouteClientset points the route check at a fake kube API for the
// duration of one test. Every other test in this package runs with the real
// factory, which returns nil off-cluster, so the route check reports
// "unknown" there and the pre-existing expectations are untouched.
func withRouteClientset(t *testing.T, cs kubernetes.Interface) {
	t.Helper()
	prev := domainRouteClientsetFactory
	domainRouteClientsetFactory = func() kubernetes.Interface { return cs }
	t.Cleanup(func() { domainRouteClientsetFactory = prev })
}

func ingressForHost(hostname string) *networkingv1.Ingress {
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "proj-ns"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: hostname}},
		},
	}
}

// TestReconcileParksHostnameOfAppThatNeverDeployed is the regression guard for
// the class of failure that produced 18 of the 20 failed managed domains in
// production: a default hostname is issued at CreateApp time, long before any
// build has landed an Ingress, so the certificate order it triggers is doomed
// by construction. Such a row must be parked as awaiting_first_deploy, not
// counted down toward failure.
func TestReconcileParksHostnameOfAppThatNeverDeployed(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping hostname reconcile DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	withRouteClientset(t, fake.NewSimpleClientset())

	host := "awaiting-" + uuid.NewString()[:8] + ".invalid"
	hostnameID := seedPendingHostname(t, pool, host)

	if err := ReconcilePendingHostnames(ctx, pool, unreachableProbeConfig()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var status string
	var reason *string
	if err := pool.QueryRow(ctx,
		`SELECT status, status_reason FROM domain_hostnames WHERE id = $1`, hostnameID,
	).Scan(&status, &reason); err != nil {
		t.Fatalf("read back hostname: %v", err)
	}
	if status != "pending" {
		t.Fatalf("hostname of an app with no route yet must stay pending, got %q", status)
	}
	if reason == nil || *reason != hostnameReasonAwaitingFirstDeploy {
		got := "<nil>"
		if reason != nil {
			got = *reason
		}
		t.Fatalf("status_reason = %s, want %q", got, hostnameReasonAwaitingFirstDeploy)
	}
}

// TestReconcileNeverFailsHostnameAwaitingFirstDeploy proves the park is a real
// exemption from the attach window and not merely a label: a row well past
// hostnamePendingFailAfter whose app still has no route must survive.
func TestReconcileNeverFailsHostnameAwaitingFirstDeploy(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping hostname reconcile DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	withRouteClientset(t, fake.NewSimpleClientset())

	host := "awaiting-old-" + uuid.NewString()[:8] + ".invalid"
	hostnameID := seedPendingHostname(t, pool, host)
	if _, err := pool.Exec(ctx,
		`UPDATE domain_hostnames SET created_at = now() - interval '49 hours' WHERE id = $1`, hostnameID,
	); err != nil {
		t.Fatalf("backdate hostname: %v", err)
	}

	if err := ReconcilePendingHostnames(ctx, pool, unreachableProbeConfig()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var status string
	var reason *string
	if err := pool.QueryRow(ctx,
		`SELECT status, status_reason FROM domain_hostnames WHERE id = $1`, hostnameID,
	).Scan(&status, &reason); err != nil {
		t.Fatalf("read back hostname: %v", err)
	}
	if status != "pending" {
		t.Fatalf("a hostname whose app never deployed must not be failed by the attach window, got %q", status)
	}
	if reason == nil || *reason != hostnameReasonAwaitingFirstDeploy {
		got := "<nil>"
		if reason != nil {
			got = *reason
		}
		t.Fatalf("status_reason = %s, want %q", got, hostnameReasonAwaitingFirstDeploy)
	}
}

// TestReconcileRestartsAttachWindowWhenRouteFinallyAppears covers the other
// half of the park: once the app's first deploy lands an Ingress, the row
// must rejoin the ordinary flow with a fresh attach window, not be failed on
// that very tick for time it spent waiting on a workload that did not exist.
func TestReconcileRestartsAttachWindowWhenRouteFinallyAppears(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping hostname reconcile DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	host := "deployed-" + uuid.NewString()[:8] + ".invalid"
	hostnameID := seedPendingHostname(t, pool, host)
	if _, err := pool.Exec(ctx,
		`UPDATE domain_hostnames
		    SET created_at = now() - interval '49 hours',
		        attach_started_at = now() - interval '49 hours',
		        status_reason = $2
		  WHERE id = $1`, hostnameID, hostnameReasonAwaitingFirstDeploy,
	); err != nil {
		t.Fatalf("backdate hostname: %v", err)
	}

	withRouteClientset(t, fake.NewSimpleClientset(ingressForHost(host)))
	if err := ReconcilePendingHostnames(ctx, pool, unreachableProbeConfig()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var status string
	var reason *string
	var attachStartedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, status_reason, attach_started_at FROM domain_hostnames WHERE id = $1`, hostnameID,
	).Scan(&status, &reason, &attachStartedAt); err != nil {
		t.Fatalf("read back hostname: %v", err)
	}
	if status != "pending" {
		t.Fatalf("a hostname whose route just appeared must resume as pending, got %q", status)
	}
	if reason != nil {
		t.Fatalf("status_reason = %q, want cleared so the next tick reports the real cert reason", *reason)
	}
	if attachStartedAt == nil || time.Since(*attachStartedAt) > time.Hour {
		t.Fatalf("attach_started_at = %v, want restamped to now so the app gets a full attach window", attachStartedAt)
	}
}

// TestReconcileStillFailsGenuinelyBrokenHostname is the control: with a live
// route and no certificate past the attach window, nothing about the park
// applies and the row must still fail exactly as before.
func TestReconcileStillFailsGenuinelyBrokenHostname(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping hostname reconcile DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	host := "broken-" + uuid.NewString()[:8] + ".invalid"
	hostnameID := seedPendingHostname(t, pool, host)
	if _, err := pool.Exec(ctx,
		`UPDATE domain_hostnames SET created_at = now() - interval '49 hours' WHERE id = $1`, hostnameID,
	); err != nil {
		t.Fatalf("backdate hostname: %v", err)
	}

	withRouteClientset(t, fake.NewSimpleClientset(ingressForHost(host)))
	if err := ReconcilePendingHostnames(ctx, pool, unreachableProbeConfig()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var status, certStatus string
	var reason *string
	if err := pool.QueryRow(ctx,
		`SELECT status, cert_status, status_reason FROM domain_hostnames WHERE id = $1`, hostnameID,
	).Scan(&status, &certStatus, &reason); err != nil {
		t.Fatalf("read back hostname: %v", err)
	}
	if status != "failed" || certStatus != "failed" {
		t.Fatalf("a routed hostname with no cert past the window is genuinely broken and must fail, got status=%q cert_status=%q", status, certStatus)
	}
	if reason == nil || *reason != hostnameReasonAttachTimeout {
		got := "<nil>"
		if reason != nil {
			got = *reason
		}
		t.Fatalf("status_reason = %s, want %q", got, hostnameReasonAttachTimeout)
	}
}
