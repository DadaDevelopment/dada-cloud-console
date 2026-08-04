package metrics

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/backend/internal/dbtest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// seedPendingHostname creates a throwaway project/environment plus one
// domain_hostnames row in the given status, and returns the hostname, project
// name and app name the gauge is expected to carry as labels.
func seedPendingHostname(t *testing.T, pool *pgxpool.Pool, status string) (hostname, project, app string) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	project = "domain-collector-" + suffix
	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		project,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dbtest.DropProject(pool, projectID) })

	app = "dc-" + suffix
	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime)
		 VALUES ($1, $2, $3, 'dev', 'k8s') RETURNING id`,
		projectID, app, app+"-ns",
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, display_name)
		 VALUES ($1, $2, 'x', $1) RETURNING id`,
		"domain-collector-"+suffix, "domain-collector-"+suffix+"@example.invalid",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { dbtest.DropUser(pool, userID) })

	var authID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO domain_authorizations (project_id, apex_domain, verification_token, status, created_by)
		 VALUES ($1, $2, $3, 'verified', $4) RETURNING id`,
		projectID, suffix+".example.invalid", "tok-"+suffix, userID,
	).Scan(&authID); err != nil {
		t.Fatalf("seed domain authorization: %v", err)
	}

	hostname = "www." + suffix + ".example.invalid"
	if _, err := pool.Exec(ctx,
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, created_at)
		 VALUES ($1, $2, $3, $4, 'CNAME', $5, now() - interval '2 hours')`,
		authID, envID, app, hostname, status,
	); err != nil {
		t.Fatalf("seed domain hostname: %v", err)
	}
	return hostname, project, app
}

// pendingAgeFor reads the pending-age gauge for one hostname's label set,
// reporting whether the series exists at all. Absence is a meaningful answer
// here: an attached hostname must stop producing a series, not report 0.
func pendingAgeFor(t *testing.T, hostname, project, app string) (float64, bool) {
	t.Helper()
	ch := make(chan prometheus.Metric, 64)
	go func() {
		domainHostnamePendingAge.Collect(ch)
		close(ch)
	}()
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatalf("read gauge: %v", err)
		}
		labels := map[string]string{}
		for _, l := range pb.GetLabel() {
			labels[l.GetName()] = l.GetValue()
		}
		if labels["hostname"] == hostname && labels["project"] == project && labels["app"] == app {
			return pb.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

// TestCollectPendingHostnamesLabelsTheStuckDomain is the alert's contract: the
// stuck hostname must be identifiable from the metric alone. Before this, the
// gauge was a single unlabelled min(created_at) over every pending row, so
// DadaCustomDomainStuck could only say "a custom hostname" and the on-call had
// to query the database to learn which one.
func TestCollectPendingHostnamesLabelsTheStuckDomain(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	hostname, project, app := seedPendingHostname(t, pool, "pending")
	collectPendingHostnames(ctx, pool)

	age, ok := pendingAgeFor(t, hostname, project, app)
	if !ok {
		t.Fatalf("no dada_domain_hostname_pending_age_seconds series for hostname=%q project=%q app=%q; "+
			"the alert cannot name a domain it has no label for", hostname, project, app)
	}
	if age < 3600 {
		t.Errorf("pending age = %v seconds for a hostname created two hours ago, want >= 3600", age)
	}
}

// TestCollectPendingHostnamesDropsAttachedDomains guards the Reset(): once a
// hostname goes active its series must disappear. A stale series would hold
// DadaCustomDomainStuck lit for a domain that already works.
func TestCollectPendingHostnamesDropsAttachedDomains(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	hostname, project, app := seedPendingHostname(t, pool, "pending")
	collectPendingHostnames(ctx, pool)
	if _, ok := pendingAgeFor(t, hostname, project, app); !ok {
		t.Fatalf("seeded pending hostname %q produced no series", hostname)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE domain_hostnames SET status = 'active' WHERE hostname = $1`, hostname); err != nil {
		t.Fatalf("mark hostname active: %v", err)
	}
	collectPendingHostnames(ctx, pool)

	if age, ok := pendingAgeFor(t, hostname, project, app); ok {
		t.Errorf("hostname %q is active but still reports pending age %v; the alert would keep firing "+
			"for a domain that already attached", hostname, age)
	}
}
