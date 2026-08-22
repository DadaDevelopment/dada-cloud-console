package api

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

// TestLinkGitRepo_PublicCloneFlight_RecordsPairOnSuccess is the slice's Verify
// line for backlog 0410: an anonymous github connect (no bound installation,
// no token) used to leave zero rows in the StartGitAppInstall/
// FinishGitAppInstall pair, so the git-oauth-flight queries measured the
// mortality of the App-install and user-authorize mechanisms only and quietly
// stood in for the whole connect door. This pins that the pair now lands with
// flow=public_clone, both halves correlated by the same install_nonce, and
// that the Finish row is a fact of the SAME statement that committed the
// git_repos row: a request cancelled the instant after this call returns can
// never see the git_repos row without also seeing its Finish row.
func TestLinkGitRepo_PublicCloneFlight_RecordsPairOnSuccess(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme-flight", "k8s")

	prev := githubCloneProbe
	githubCloneProbe = func(context.Context, string) (bool, bool) { return true, true }
	t.Cleanup(func() { githubCloneProbe = prev })

	appName := "flight-ok-" + uuid.NewString()[:8]
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	repo, fault := h.linkGitRepo(context.Background(), userID, projectID, envID, &connectGitRepoRequest{
		RepoFullName: "acme/" + uuid.NewString()[:8],
		AppName:      appName,
		Provider:     "github",
	})
	if fault != nil {
		t.Fatalf("link failed: %+v", fault)
	}
	if repo == nil {
		t.Fatal("repo is nil with no fault")
	}

	rows, err := pool.Query(context.Background(),
		`SELECT action, outcome, metadata->>'flow', metadata->>'install_nonce'
		   FROM audit_events
		  WHERE project_id = $1 AND action IN ('StartGitAppInstall', 'FinishGitAppInstall')
		  ORDER BY action`,
		projectID,
	)
	if err != nil {
		t.Fatalf("query flight rows: %v", err)
	}
	defer rows.Close()

	type flightRow struct{ action, outcome, flow, nonce string }
	var got []flightRow
	for rows.Next() {
		var fr flightRow
		if err := rows.Scan(&fr.action, &fr.outcome, &fr.flow, &fr.nonce); err != nil {
			t.Fatalf("scan flight row: %v", err)
		}
		got = append(got, fr)
	}
	if len(got) != 2 {
		t.Fatalf("got %d flight rows, want 2 (Start+Finish): %+v", len(got), got)
	}
	start, finish := got[1], got[0]
	if start.action != "StartGitAppInstall" || finish.action != "FinishGitAppInstall" {
		t.Fatalf("unexpected action pair: %+v", got)
	}
	if start.flow != installFlowPublicClone || finish.flow != installFlowPublicClone {
		t.Fatalf("flow = %q/%q, want %q on both", start.flow, finish.flow, installFlowPublicClone)
	}
	if start.nonce == "" || start.nonce != finish.nonce {
		t.Fatalf("nonces do not correlate: start=%q finish=%q", start.nonce, finish.nonce)
	}
	if finish.outcome != auditOutcomeSuccess {
		t.Fatalf("finish outcome = %q, want success", finish.outcome)
	}
}

// TestLinkGitRepo_PublicCloneFlight_DecisiveRejectionRecordsFailure asserts the
// mirror case: a decisive "not clonable" verdict blocks the connect (no
// git_repos row) but still closes the flight with outcome=failure, so the
// mortality query sees this as a died attempt rather than an invisible one.
func TestLinkGitRepo_PublicCloneFlight_DecisiveRejectionRecordsFailure(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme-flight-reject", "k8s")

	prev := githubCloneProbe
	githubCloneProbe = func(context.Context, string) (bool, bool) { return false, true }
	t.Cleanup(func() { githubCloneProbe = prev })

	appName := "flight-rejected-" + uuid.NewString()[:8]
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	repo, fault := h.linkGitRepo(context.Background(), userID, projectID, envID, &connectGitRepoRequest{
		RepoFullName: "acme/" + uuid.NewString()[:8],
		AppName:      appName,
		Provider:     "github",
	})
	if fault == nil {
		t.Fatal("link succeeded, want github_access_required")
	}
	if repo != nil {
		t.Fatal("repo is non-nil despite a decisive rejection")
	}

	var finishOutcome, finishReason string
	err := pool.QueryRow(context.Background(),
		`SELECT outcome, metadata->>'reason' FROM audit_events
		  WHERE project_id = $1 AND action = 'FinishGitAppInstall'`,
		projectID,
	).Scan(&finishOutcome, &finishReason)
	if err != nil {
		t.Fatalf("expected a FinishGitAppInstall row for the rejected attempt: %v", err)
	}
	if finishOutcome != auditOutcomeFailure {
		t.Fatalf("outcome = %q, want failure", finishOutcome)
	}
	if finishReason != "github_access_required" {
		t.Fatalf("reason = %q, want github_access_required", finishReason)
	}
}

// TestLinkGitRepo_InstalledConnect_NeverWritesPublicCloneFlight guards the
// boundary: a connect that resolves to a bound App installation is already
// covered by GetGitInstallURL/GitInstallCallback's own Start/Finish pair
// (flow=app_install), so linkGitRepo must not also emit a public_clone row for
// it -- that would double count the same connect under two flows.
func TestLinkGitRepo_InstalledConnect_NeverWritesPublicCloneFlight(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme-flight-installed", "k8s")

	var orgID string
	if err := pool.QueryRow(context.Background(),
		`SELECT org_id FROM projects WHERE id = $1`, projectID,
	).Scan(&orgID); err != nil {
		t.Fatalf("read project org_id: %v", err)
	}
	var installationID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO git_app_installations (project_id, org_id, provider, installation_id, account_login, account_type)
		 VALUES ($1, $2, 'github', $3, 'acme', 'Organization') RETURNING id`,
		projectID, orgID, int64(90000001),
	).Scan(&installationID); err != nil {
		t.Fatalf("seed git_app_installations: %v", err)
	}

	appName := "flight-installed-" + uuid.NewString()[:8]
	h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	repo, fault := h.linkGitRepo(context.Background(), userID, projectID, envID, &connectGitRepoRequest{
		RepoFullName:   "acme/" + uuid.NewString()[:8],
		AppName:        appName,
		Provider:       "github",
		InstallationID: installationID.String(),
	})
	if fault != nil {
		t.Fatalf("link failed: %+v", fault)
	}
	if repo == nil {
		t.Fatal("repo is nil with no fault")
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events
		  WHERE project_id = $1 AND action IN ('StartGitAppInstall', 'FinishGitAppInstall')`,
		projectID,
	).Scan(&count); err != nil {
		t.Fatalf("query flight rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d public_clone flight rows for an installed connect, want 0", count)
	}
}
