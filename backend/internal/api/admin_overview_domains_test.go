package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestOverviewDomainsExcludesAppDeletedTombstones reproduces the live
// production read on 2026-08-15: overviewDomains counted 25 domain_hostnames
// rows with status='failed' and reported failed:25 on the same
// /api/v1/admin/overview response whose overviewDomainIssues list (a few
// lines below, same file) excludes rows whose status_reason is
// hostnameReasonAppDeleted -- the tombstone demoteAppHostnames stamps on
// every hostname belonging to a deleted app. 24 of the 25 were exactly that:
// domains of apps the owner already removed, not live problems. The two
// numbers must agree on what "failed" means: a tombstoned row belongs in
// Retired, a live failure belongs in Failed, and Active+Pending+Failed+Retired
// must still equal count(*) so nothing silently disappears from the total.
func TestOverviewDomainsExcludesAppDeletedTombstones(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]

	userID := overviewBrokenSeedUser(t, pool, "domainsowner-"+suffix, "domainsowner-"+suffix+"@example.test")
	projectID := overviewBrokenSeedProject(t, pool, "domainscount-"+suffix)
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	var authID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO domain_authorizations (project_id, apex_domain, verification_token, status, created_by)
		 VALUES ($1, $2, $3, 'verified', $4) RETURNING id`,
		projectID, "domainscount-"+suffix+".test", "tok-"+suffix, userID,
	).Scan(&authID); err != nil {
		t.Fatalf("seed authorization: %v", err)
	}

	seedHostname := func(hostname, status, statusReason string) {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, status_reason)
			 VALUES ($1, $2, 'app', $3, 'CNAME', $4, $4, $5)`,
			authID, envID, hostname, status, nullIfEmpty(statusReason),
		); err != nil {
			t.Fatalf("seed hostname %s: %v", hostname, err)
		}
	}

	liveFailedHost := "live-failed-" + suffix + ".test"
	tombstoneHost := "app-deleted-" + suffix + ".test"
	activeHost := "active-" + suffix + ".test"

	seedHostname(liveFailedHost, "failed", "")
	seedHostname(tombstoneHost, "failed", hostnameReasonAppDeleted)
	seedHostname(activeHost, "active", "")

	issues, err := h.overviewDomainIssues(context.Background())
	if err != nil {
		t.Fatalf("overviewDomainIssues: %v", err)
	}
	hostnameFailedIssues := 0
	for _, i := range issues {
		if i.Stage == "hostname" && i.Status == "failed" {
			hostnameFailedIssues++
		}
		if i.Hostname == tombstoneHost {
			t.Fatalf("the app_deleted tombstone %s must not appear in overviewDomainIssues", tombstoneHost)
		}
	}

	out, err := h.overviewDomains(context.Background())
	if err != nil {
		t.Fatalf("overviewDomains: %v", err)
	}

	if out.Retired < 1 {
		t.Fatalf("Retired = %d, want at least 1: the app_deleted tombstone %s must be counted in Retired, not silently dropped", out.Retired, tombstoneHost)
	}
	if out.Failed != hostnameFailedIssues {
		t.Fatalf("Failed = %d, want %d (must equal the number of hostname-stage failed issues overviewDomainIssues reports on the same data)", out.Failed, hostnameFailedIssues)
	}

	var total int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM domain_hostnames`).Scan(&total); err != nil {
		t.Fatalf("count domain_hostnames: %v", err)
	}
	if sum := out.Active + out.Pending + out.Failed + out.Retired; sum != total {
		t.Fatalf("Active+Pending+Failed+Retired = %d, want %d (count(*) from domain_hostnames): a bucket must not silently drop rows", sum, total)
	}
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
