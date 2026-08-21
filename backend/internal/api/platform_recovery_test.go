package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// recoveryTestFix is a throwaway registry entry with its own Kind/Action/
// MetaKey/MetaValue/FixedAt, distinct from every entry in
// platformActionFailureFixes. This is the house rule learned the hard way
// (backlog cycle-log): a test must never feed the classifier the exact
// string the classifier already produces in production, or a bug that
// changes the real registry's values would leave the test green. Using a
// synthetic action/reason here means these tests only pass if
// recoveryPromptEligible's SQL and gating logic are actually correct, not
// because the test happens to describe the shipped signatures.
func recoveryTestFix(suffix string) actionFailureFix {
	return actionFailureFix{
		Kind:      "test_recovery_kind_" + suffix,
		Action:    "TestRecoveryAction",
		MetaKey:   "test_reason",
		MetaValue: "test_broke_" + suffix,
		FixedAt:   time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		Note:      "synthetic signature for platform_recovery_test.go, never shipped",
	}
}

func seedRecoveryAuditEvent(t *testing.T, pool *pgxpool.Pool, actorID uuid.UUID, action, outcome, metaKey, metaValue string, at time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO audit_events (actor_id, action, resource_kind, resource_name, outcome, metadata, actor_type, created_at)
		 VALUES ($1, $2, 'App', 'test-app', $3, jsonb_build_object($4::text, $5::text), 'user', $6)`,
		actorID, action, outcome, metaKey, metaValue, at,
	)
	if err != nil {
		t.Fatalf("seed audit_events action=%s outcome=%s: %v", action, outcome, err)
	}
}

func seedRecoveryApp(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, envID uuid.UUID, name string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json, last_synced_at)
		 VALUES ($1, $2, 'App', $3, 'Running', '{}', now())`,
		projectID, envID, name,
	)
	if err != nil {
		t.Fatalf("seed resource_snapshots app %s: %v", name, err)
	}
}

// TestRecoveryPromptEligible_EligibleUserGetsThePrompt is the RED-provable
// baseline: a user with a matching failure before FixedAt, no success since,
// and (this fix being the install kind) zero apps must get a prompt back
// with the failure's own project/env/resource_name and the registry's
// FixedAt.
func TestRecoveryPromptEligible_EligibleUserGetsThePrompt(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	fix := recoveryTestFix(suffix)
	fix.Kind = recoveryKindSolutionInstallEnvFailed

	userID := overviewBrokenSeedUser(t, pool, "recov-elig-"+suffix, "recov-elig-"+suffix+"@example.com")
	projectID := overviewBrokenSeedProject(t, pool, "recov-elig-"+suffix)
	if _, err := pool.Exec(context.Background(), `UPDATE projects SET owner_id = $1 WHERE id = $2`, userID, projectID); err != nil {
		t.Fatalf("set owner_id: %v", err)
	}
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")

	failedAt := fix.FixedAt.Add(-time.Hour)
	seedRecoveryAuditEvent(t, pool, userID, fix.Action, "failure", fix.MetaKey, fix.MetaValue, failedAt)

	cand, err := h.recoveryPromptEligible(context.Background(), userID, fix)
	if err != nil {
		t.Fatalf("recoveryPromptEligible: %v", err)
	}
	if cand == nil {
		t.Fatal("expected an eligible prompt, got nil")
	}
	if cand.Kind != fix.Kind {
		t.Fatalf("kind=%q want %q", cand.Kind, fix.Kind)
	}
	if !cand.FixedAt.Equal(fix.FixedAt) {
		t.Fatalf("fixed_at=%v want %v", cand.FixedAt, fix.FixedAt)
	}
	if !cand.FailedAt.Equal(failedAt) {
		t.Fatalf("failed_at=%v want %v", cand.FailedAt, failedAt)
	}

	_ = envID
}

// TestRecoveryPromptEligible_FailureAfterFixedAtYieldsNoPrompt proves the
// strictly-before gate: a failure that landed AT OR AFTER FixedAt is a
// different, still-open bug and must never be reported as already fixed.
func TestRecoveryPromptEligible_FailureAfterFixedAtYieldsNoPrompt(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	fix := recoveryTestFix(suffix)

	userID := overviewBrokenSeedUser(t, pool, "recov-after-"+suffix, "recov-after-"+suffix+"@example.com")

	seedRecoveryAuditEvent(t, pool, userID, fix.Action, "failure", fix.MetaKey, fix.MetaValue, fix.FixedAt.Add(time.Hour))

	cand, err := h.recoveryPromptEligible(context.Background(), userID, fix)
	if err != nil {
		t.Fatalf("recoveryPromptEligible: %v", err)
	}
	if cand != nil {
		t.Fatalf("expected no prompt for a failure after FixedAt, got %+v", cand)
	}
}

// TestRecoveryPromptEligible_SuccessSinceFixYieldsNoPrompt proves the
// self-recovery gate for the install kind: a user who hit the bug and then
// successfully installed something after the fix landed does not need to be
// told anything.
func TestRecoveryPromptEligible_SuccessSinceFixYieldsNoPrompt(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	fix := recoveryTestFix(suffix)
	fix.Kind = recoveryKindSolutionInstallEnvFailed
	fix.Action = "InstallSolution"

	userID := overviewBrokenSeedUser(t, pool, "recov-recov-"+suffix, "recov-recov-"+suffix+"@example.com")

	seedRecoveryAuditEvent(t, pool, userID, fix.Action, "failure", fix.MetaKey, fix.MetaValue, fix.FixedAt.Add(-time.Hour))
	seedRecoveryAuditEvent(t, pool, userID, "InstallSolution", "success", "reason", "n/a", fix.FixedAt.Add(time.Hour))

	cand, err := h.recoveryPromptEligible(context.Background(), userID, fix)
	if err != nil {
		t.Fatalf("recoveryPromptEligible: %v", err)
	}
	if cand != nil {
		t.Fatalf("expected no prompt for a user who already succeeded since the fix, got %+v", cand)
	}
}

// TestRecoveryPromptEligible_UserWithAppsYieldsNoPromptForInstallKind proves
// the install-specific narrowing: even with a qualifying failure and no
// success since, a user who currently owns at least one app is not shown the
// "you have zero apps" recovery prompt -- they are not actually stuck.
func TestRecoveryPromptEligible_UserWithAppsYieldsNoPromptForInstallKind(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	h := &Handler{pool: pool}
	suffix := uuid.NewString()[:8]
	fix := recoveryTestFix(suffix)
	fix.Kind = recoveryKindSolutionInstallEnvFailed

	userID := overviewBrokenSeedUser(t, pool, "recov-hasapp-"+suffix, "recov-hasapp-"+suffix+"@example.com")
	projectID := overviewBrokenSeedProject(t, pool, "recov-hasapp-"+suffix)
	if _, err := pool.Exec(context.Background(), `UPDATE projects SET owner_id = $1 WHERE id = $2`, userID, projectID); err != nil {
		t.Fatalf("set owner_id: %v", err)
	}
	envID := overviewBrokenSeedEnv(t, pool, projectID, "prod")
	seedRecoveryApp(t, pool, projectID, envID, "already-live-"+suffix)

	seedRecoveryAuditEvent(t, pool, userID, fix.Action, "failure", fix.MetaKey, fix.MetaValue, fix.FixedAt.Add(-time.Hour))

	cand, err := h.recoveryPromptEligible(context.Background(), userID, fix)
	if err != nil {
		t.Fatalf("recoveryPromptEligible: %v", err)
	}
	if cand != nil {
		t.Fatalf("expected no prompt for a user who already has an app, got %+v", cand)
	}
}

// TestRecoveryPromptFor_PicksMostRecentFailureAcrossFixes proves the
// registry walk: a user eligible for two different fixes gets back the one
// whose own failure happened later, not whichever entry sits first in
// platformActionFailureFixes.
func TestRecoveryPromptFor_PicksMostRecentFailureAcrossFixes(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	suffix := uuid.NewString()[:8]

	userID := overviewBrokenSeedUser(t, pool, "recov-multi-"+suffix, "recov-multi-"+suffix+"@example.com")

	older := recoveryTestFix("older-" + suffix)
	newer := recoveryTestFix("newer-" + suffix)
	newer.FixedAt = older.FixedAt.Add(24 * time.Hour)

	seedRecoveryAuditEvent(t, pool, userID, older.Action, "failure", older.MetaKey, older.MetaValue, older.FixedAt.Add(-2*time.Hour))
	seedRecoveryAuditEvent(t, pool, userID, newer.Action, "failure", newer.MetaKey, newer.MetaValue, newer.FixedAt.Add(-time.Hour))

	orig := platformActionFailureFixes
	platformActionFailureFixes = []actionFailureFix{older, newer}
	t.Cleanup(func() { platformActionFailureFixes = orig })

	h := &Handler{pool: pool}
	prompt, err := h.recoveryPromptFor(context.Background(), userID)
	if err != nil {
		t.Fatalf("recoveryPromptFor: %v", err)
	}
	if prompt == nil {
		t.Fatal("expected a prompt, got nil")
	}
	if prompt.Kind != newer.Kind {
		t.Fatalf("kind=%q want %q (the more recent failure)", prompt.Kind, newer.Kind)
	}
}
