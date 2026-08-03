package api

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
)

// TestHostnameProbeTargetsNeverFollowsPublicHostname is the regression guard for
// the "green domain that serves nothing" bug: the cert probe used to dial the
// hostname itself, so a domain still delegated to its previous provider passed
// the probe (that provider serves a perfectly valid certificate for it) and the
// row flipped to active seconds after attach. The probe address must come from
// our own topology, never from the name being checked.
func TestHostnameProbeTargetsNeverFollowsPublicHostname(t *testing.T) {
	cfg := &config.Config{
		IngressTLSProbeAddr: "ingress-nginx-pub-controller.network.svc.cluster.local:443",
		CustomDomainATarget: "203.0.113.10",
		ClusterLBIP:         "203.0.113.10",
	}

	addr, dnsTarget, probeable := hostnameProbeTargets(cfg, "a2a-hub.pro", models.EnvironmentRuntimeK8s, nil)
	if !probeable {
		t.Fatalf("a k8s hostname must be probeable")
	}
	if addr != cfg.IngressTLSProbeAddr {
		t.Errorf("k8s probe address = %q, want the ingress address %q", addr, cfg.IngressTLSProbeAddr)
	}
	if dnsTarget != cfg.CustomDomainATarget {
		t.Errorf("k8s dns target = %q, want %q", dnsTarget, cfg.CustomDomainATarget)
	}

	vmIP := "198.51.100.7"
	addr, dnsTarget, probeable = hostnameProbeTargets(cfg, "app.example.com", models.EnvironmentRuntimeVM, &vmIP)
	if !probeable {
		t.Fatalf("a VM hostname with a recorded IP must be probeable")
	}
	if addr != "198.51.100.7:443" {
		t.Errorf("VM probe address = %q, want the VM host", addr)
	}
	if dnsTarget != vmIP {
		t.Errorf("VM dns target = %q, want %q", dnsTarget, vmIP)
	}

	if _, _, probeable = hostnameProbeTargets(cfg, "app.example.com", models.EnvironmentRuntimeVM, nil); probeable {
		t.Errorf("a VM environment with no IP has nothing to probe and must be skipped, not probed via the public hostname")
	}

	addr, dnsTarget, probeable = hostnameProbeTargets(nil, "app.example.com", models.EnvironmentRuntimeK8s, nil)
	if !probeable || addr != "app.example.com:443" || dnsTarget != "" {
		t.Errorf("with no config the probe falls back to the public hostname and claims no dns target; got addr=%q target=%q probeable=%v",
			addr, dnsTarget, probeable)
	}
}

// seedPendingHostname creates a project/environment/hostname trail and returns
// the hostname row id. Everything is torn down by the test's cleanup.
func seedPendingHostname(t *testing.T, pool *pgxpool.Pool, hostname string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"hostname-reason-"+suffix).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		dropSeededProject(pool, projectID)
	})

	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "ns-"+suffix).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM domain_hostnames WHERE environment_id = $1`, envID)
	})

	var hostnameID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, managed)
		 VALUES (NULL, $1, 'web', $2, 'A', 'pending', 'pending', false) RETURNING id`,
		envID, hostname).Scan(&hostnameID); err != nil {
		t.Fatalf("seed hostname: %v", err)
	}
	return hostnameID
}

// unreachableProbeConfig points the cert probe at a closed local port, so the
// handshake fails immediately without touching the network, and at a DNS target
// no test hostname can ever resolve to.
func unreachableProbeConfig() *config.Config {
	return &config.Config{
		IngressTLSProbeAddr: "127.0.0.1:1",
		CustomDomainATarget: "203.0.113.10",
		ClusterLBIP:         "203.0.113.10",
	}
}

func TestReconcilePendingHostnamesRecordsDNSReason(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping hostname reconcile DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	hostnameID := seedPendingHostname(t, pool, "reason-"+uuid.NewString()[:8]+".invalid")

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
	if status != "pending" || certStatus != "pending" {
		t.Fatalf("hostname with no certificate served by us must stay pending, got status=%q cert_status=%q", status, certStatus)
	}
	if reason == nil || *reason != hostnameReasonDNSNotPointed {
		got := "<nil>"
		if reason != nil {
			got = *reason
		}
		t.Fatalf("status_reason = %s, want %q so the console can say whose move it is", got, hostnameReasonDNSNotPointed)
	}
}

func TestReconcilePendingHostnamesFailsPastAttachWindowWithReason(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping hostname reconcile DB integration test")
	}
	pool := testAdvisoryPool(t)
	ctx := context.Background()

	hostnameID := seedPendingHostname(t, pool, "expired-"+uuid.NewString()[:8]+".invalid")
	if _, err := pool.Exec(ctx,
		`UPDATE domain_hostnames SET created_at = now() - interval '49 hours' WHERE id = $1`, hostnameID,
	); err != nil {
		t.Fatalf("backdate hostname: %v", err)
	}

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
		t.Fatalf("hostname past the attach window must fail, got status=%q cert_status=%q", status, certStatus)
	}
	if reason == nil || *reason != hostnameReasonAttachTimeout {
		got := "<nil>"
		if reason != nil {
			got = *reason
		}
		t.Fatalf("status_reason = %s, want %q", got, hostnameReasonAttachTimeout)
	}
}
