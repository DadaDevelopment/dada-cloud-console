package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The GitHub install flight is the one step of the connect path that leaves our
// origin: the user goes to github.com and either comes back or does not. Until
// both halves were audited the two endings were indistinguishable from a silent
// user, and the reconstructed path of a real account (click "Connect GitHub" ->
// redirect -> nothing, ever) could not say whether GitHub was ever reached.
//
// The contract these tests hold: an intent row is written before the user
// leaves, the verdict row that closes it is attributed to the SAME actor even
// though the callback carries no session, and a callback that cannot find its
// intent writes nothing rather than an unattributed row.

func installFlightIntent(t *testing.T, pool *pgxpool.Pool, actorID, projectID uuid.UUID, nonce string) {
	t.Helper()
	writeAuditRow(context.Background(), pool, actorID, auditEntry{
		ProjectID:    projectID,
		Action:       auditActionStartGitAppInstall,
		ResourceKind: "git_installation",
		ResourceName: "github",
		Outcome:      auditOutcomePending,
		Metadata:     map[string]any{"install_nonce": nonce, "provider": "github", "flow": "app_install"},
	})
}

func installFlightVerdict(t *testing.T, pool *pgxpool.Pool, nonce string) (actorID uuid.UUID, outcome, reason string, found bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT actor_id, outcome, COALESCE(metadata->>'reason', '')
		   FROM audit_events
		  WHERE action = $1 AND metadata->>'install_nonce' = $2
		  ORDER BY created_at DESC LIMIT 1`,
		auditActionFinishGitAppInstall, nonce,
	).Scan(&actorID, &outcome, &reason)
	if err != nil {
		return uuid.Nil, "", "", false
	}
	return actorID, outcome, reason, true
}

func TestInstallVerdictIsAttributedToTheUserWhoLeft(t *testing.T) {
	pool := testOptimisticPool(t)
	userID := seedUser(t, pool)
	projectID, _ := seedOptimisticFixture(t, pool)
	h := &Handler{pool: pool}

	_, nonce := signInstallState("s3cr3t", projectID)
	installFlightIntent(t, pool, userID, projectID, nonce)

	h.recordInstallVerdict(context.Background(), projectID, nonce, auditOutcomeSuccess,
		map[string]any{"installation_id": "424242"})

	actor, outcome, _, found := installFlightVerdict(t, pool, nonce)
	if !found {
		t.Fatal("no verdict row: a returning user is still indistinguishable from one who never came back")
	}
	if actor != userID {
		t.Errorf("actor = %s, want %s — the callback carries no session, so the actor must come from the intent row", actor, userID)
	}
	if outcome != auditOutcomeSuccess {
		t.Errorf("outcome = %q, want %q", outcome, auditOutcomeSuccess)
	}
}

func TestInstallVerdictCarriesTheReasonItDied(t *testing.T) {
	pool := testOptimisticPool(t)
	userID := seedUser(t, pool)
	projectID, _ := seedOptimisticFixture(t, pool)
	h := &Handler{pool: pool}

	_, nonce := signInstallState("s3cr3t", projectID)
	installFlightIntent(t, pool, userID, projectID, nonce)

	h.recordInstallVerdict(context.Background(), projectID, nonce, auditOutcomeFailure,
		map[string]any{"reason": "resolve_installation_failed"})

	_, outcome, reason, found := installFlightVerdict(t, pool, nonce)
	if !found {
		t.Fatal("a failed return wrote no row at all")
	}
	if outcome != auditOutcomeFailure {
		t.Errorf("outcome = %q, want %q", outcome, auditOutcomeFailure)
	}
	if reason != "resolve_installation_failed" {
		t.Errorf("reason = %q, want the cause of death — an unexplained failure cannot be acted on", reason)
	}
}

func TestInstallVerdictWithoutIntentWritesNothing(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, _ := seedOptimisticFixture(t, pool)
	h := &Handler{pool: pool}

	_, nonce := signInstallState("s3cr3t", projectID)

	h.recordInstallVerdict(context.Background(), projectID, nonce, auditOutcomeSuccess, nil)

	if _, _, _, found := installFlightVerdict(t, pool, nonce); found {
		t.Error("wrote a verdict with no intent behind it: a row that cannot name who acted pollutes every per-user count")
	}
}

func TestOpenFlightsAreCountableAsTheLeak(t *testing.T) {
	pool := testOptimisticPool(t)
	userID := seedUser(t, pool)
	projectID, _ := seedOptimisticFixture(t, pool)
	h := &Handler{pool: pool}

	_, returned := signInstallState("s3cr3t", projectID)
	_, lost := signInstallState("s3cr3t", projectID)
	installFlightIntent(t, pool, userID, projectID, returned)
	installFlightIntent(t, pool, userID, projectID, lost)
	h.recordInstallVerdict(context.Background(), projectID, returned, auditOutcomeSuccess, nil)

	var open int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events s
		  WHERE s.action = $1
		    AND s.actor_id = $3
		    AND NOT EXISTS (
		          SELECT 1 FROM audit_events f
		           WHERE f.action = $2
		             AND f.metadata->>'install_nonce' = s.metadata->>'install_nonce')`,
		auditActionStartGitAppInstall, auditActionFinishGitAppInstall, userID,
	).Scan(&open)
	if err != nil {
		t.Fatalf("count open flights: %v", err)
	}
	if open != 1 {
		t.Errorf("open flights = %d, want 1 — start-minus-finish IS the mortality of the step", open)
	}
}
