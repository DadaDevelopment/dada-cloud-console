package api

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/kubernetes/fake"
)

// withCertLiveProbe swaps the TLS handshake for a fixed verdict, so the heal
// decision can be exercised without an ingress to dial. Restored on cleanup,
// leaving every other test on the real probe.
func withCertLiveProbe(t *testing.T, live bool) {
	t.Helper()
	prev := hostnameCertLiveProbe
	hostnameCertLiveProbe = func(context.Context, string, string) bool { return live }
	t.Cleanup(func() { hostnameCertLiveProbe = prev })
}

// seedFailedHostnameWithReason inserts a hostname already frozen at
// failed/failed, the state ReconcilePendingHostnames leaves behind once the
// attach window expires.
func seedFailedHostnameWithReason(t *testing.T, pool *pgxpool.Pool, hostname, reason string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	hostnameID := seedPendingHostname(t, pool, hostname)
	if _, err := pool.Exec(ctx,
		`UPDATE domain_hostnames SET status='failed', cert_status='failed', status_reason=$2 WHERE id=$1`,
		hostnameID, reason,
	); err != nil {
		t.Fatalf("fail seeded hostname: %v", err)
	}
	return hostnameID
}

func readHostnameState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (status, certStatus string, reason *string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, cert_status, status_reason FROM domain_hostnames WHERE id = $1`, id,
	).Scan(&status, &certStatus, &reason); err != nil {
		t.Fatalf("read back hostname: %v", err)
	}
	return status, certStatus, reason
}

// TestHealRestoresServingFailedHostname is the regression guard for the
// one-way door: two production domains carried cert_status='failed' from the
// day their attach window expired while answering HTTPS 200, because nothing
// ever re-read a failed row.
func TestHealRestoresServingFailedHostname(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping hostname heal DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	host := "heal-" + uuid.NewString()[:8] + ".invalid"
	hostnameID := seedFailedHostnameWithReason(t, pool, host, hostnameReasonAttachTimeout)
	withCertLiveProbe(t, true)
	withRouteClientset(t, fake.NewSimpleClientset(ingressForHost(host)))

	if err := HealRecoveredFailedHostnames(ctx, pool, unreachableProbeConfig()); err != nil {
		t.Fatalf("heal: %v", err)
	}

	status, certStatus, reason := readHostnameState(t, pool, hostnameID)
	if status != "active" || certStatus != "active" {
		t.Fatalf("a hostname serving cert+route must be restored, got status=%q cert_status=%q", status, certStatus)
	}
	if reason != nil {
		t.Fatalf("status_reason = %q, want cleared", *reason)
	}
}

// TestHealLeavesFailedHostnameWithNoRoute proves the heal demands both proofs.
// The managed surrogate names share one wildcard certificate, so a cert-only
// check would resurrect every dead *.dada-tuda.ru row that routes nothing.
func TestHealLeavesFailedHostnameWithNoRoute(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping hostname heal DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	host := "heal-noroute-" + uuid.NewString()[:8] + ".invalid"
	hostnameID := seedFailedHostnameWithReason(t, pool, host, hostnameReasonAttachTimeout)
	withCertLiveProbe(t, true)
	withRouteClientset(t, fake.NewSimpleClientset())

	if err := HealRecoveredFailedHostnames(ctx, pool, unreachableProbeConfig()); err != nil {
		t.Fatalf("heal: %v", err)
	}

	if status, _, _ := readHostnameState(t, pool, hostnameID); status != "failed" {
		t.Fatalf("a certificate under our wildcard with no Ingress behind it must stay failed, got %q", status)
	}
}

// TestHealLeavesFailedHostnameWithNoCert covers the other half: a route alone
// is not proof the name is served over TLS by us.
func TestHealLeavesFailedHostnameWithNoCert(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping hostname heal DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	host := "heal-nocert-" + uuid.NewString()[:8] + ".invalid"
	hostnameID := seedFailedHostnameWithReason(t, pool, host, hostnameReasonAttachTimeout)
	withCertLiveProbe(t, false)
	withRouteClientset(t, fake.NewSimpleClientset(ingressForHost(host)))

	if err := HealRecoveredFailedHostnames(ctx, pool, unreachableProbeConfig()); err != nil {
		t.Fatalf("heal: %v", err)
	}

	if status, _, _ := readHostnameState(t, pool, hostnameID); status != "failed" {
		t.Fatalf("a hostname with no live certificate must stay failed, got %q", status)
	}
}

// TestHealNeverResurrectsDeletedAppHostname protects the one terminal-by-design
// reason: DeleteApp stamps app_deleted, and a managed surrogate name keeps
// passing the wildcard cert probe long after its app is gone.
func TestHealNeverResurrectsDeletedAppHostname(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping hostname heal DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	host := "heal-deleted-" + uuid.NewString()[:8] + ".invalid"
	hostnameID := seedFailedHostnameWithReason(t, pool, host, hostnameReasonAppDeleted)
	withCertLiveProbe(t, true)
	withRouteClientset(t, fake.NewSimpleClientset(ingressForHost(host)))

	if err := HealRecoveredFailedHostnames(ctx, pool, unreachableProbeConfig()); err != nil {
		t.Fatalf("heal: %v", err)
	}

	status, _, reason := readHostnameState(t, pool, hostnameID)
	if status != "failed" {
		t.Fatalf("a hostname retired with the app must stay failed, got %q", status)
	}
	if reason == nil || *reason != hostnameReasonAppDeleted {
		t.Fatalf("status_reason must survive the heal pass untouched")
	}
}
