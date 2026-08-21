package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// actionFailureFix is one closed PLATFORM action-failure signature: a bug on
// our side broke a user-initiated action (not the user's own mistake), the
// bug is fixed now, and the user who hit it while it was broken deserves a
// prompt on their next visit rather than silence. This is the sibling of
// selfHealFix (platform_selfheal.go) for actions that never produced a
// resource to rebuild -- there is no app to fix, only a person to bring back.
//
// Same discipline as selfHealFix: append here, never edit an existing entry's
// Kind or FixedAt after it has shipped. Kind is the stable string the console
// switches on to render a specific message and CTA; changing it after ship
// breaks whatever the frontend already matched it against, and changing
// FixedAt reclassifies failures that were already told apart by the old
// value.
//
// This is a plain Go slice, not a database table, for the exact reason
// platformSelfHealFixes is one: the set of signatures the platform has ever
// closed changes at the rate of platform releases, is reviewed in the same PR
// as the fix itself, and needs no admin UI to edit.
type actionFailureFix struct {
	Kind      string
	Action    string
	MetaKey   string
	MetaValue string
	FixedAt   time.Time
	Note      string
}

const (
	recoveryKindSolutionInstallEnvFailed  = "solution_install_env_failed"
	recoveryKindPaymentRecurringForbidden = "payment_recurring_forbidden"
)

// platformActionFailureFixes is the whole registry.
//
// solution_install_env_failed (backlog 0431): a trailing newline in the
// GITOPS_ENCRYPTION_KEY secret broke hex.DecodeString in crypto.decodeKey, so
// InstallSolution failed writing the app's env vars for every install attempt
// while the bad key was live. Fixed by commit 17db736d (2026-08-19 11:57
// UTC). Live incident: kkartov@yandex.ru hit this three times on 2026-08-19
// 04:10-04:12 UTC, came back at 19:26 to an empty app list, and has had zero
// apps for four days since -- the install never produced anything to retry
// from, so there is nothing on the console today that tells them to try
// again.
//
// payment_recurring_forbidden (backlog 0431): the recurring-payment checkbox
// defaulted ON while the YooKassa shop account cannot do recurring charges,
// so CreatePayment came back 403 for anyone who left the default checked.
// Fixed by commit b49fe2a8 (2026-08-16 10:58 UTC), which turned the checkbox
// off by default and added the pre-flight check in BillingCheckout. Live
// incident: artempro2021@bk.ru hit this on 2026-08-15 21:45:43 UTC and never
// retried.
var platformActionFailureFixes = []actionFailureFix{
	{
		Kind:      recoveryKindSolutionInstallEnvFailed,
		Action:    "InstallSolution",
		MetaKey:   "reason",
		MetaValue: "env_failed",
		FixedAt:   time.Date(2026, 8, 19, 11, 57, 0, 0, time.UTC),
		Note:      "trailing newline in GITOPS_ENCRYPTION_KEY broke hex.DecodeString in crypto.decodeKey, failing every env var write during install",
	},
	{
		Kind:      recoveryKindPaymentRecurringForbidden,
		Action:    "CreatePaymentFailed",
		MetaKey:   "error_class",
		MetaValue: "yk_forbidden",
		FixedAt:   time.Date(2026, 8, 16, 10, 58, 0, 0, time.UTC),
		Note:      "recurring-payment checkbox defaulted on while the shop account cannot do recurring charges, so CreatePayment came back 403 for anyone who left it checked",
	},
}

// recoveryPrompt is what GET /recovery-prompt hands the console for one
// user: at most one still-open reason to say "we broke this, it works now,
// come back." ProjectID/EnvironmentID are omitted (not zero-valued) when the
// underlying audit row carried none, which is the case for
// payment_recurring_forbidden -- CreatePaymentFailed's audit row is written
// against an org, not a project (see recordCheckoutFailureTx).
type recoveryPrompt struct {
	Kind          string     `json:"kind"`
	FailedAt      time.Time  `json:"failed_at"`
	FixedAt       time.Time  `json:"fixed_at"`
	ProjectID     *uuid.UUID `json:"project_id,omitempty"`
	EnvironmentID *uuid.UUID `json:"environment_id,omitempty"`
	ResourceName  string     `json:"resource_name,omitempty"`
}

// GetRecoveryPrompt returns at most one platform-recovery prompt for the
// calling user: proof they were hit by a now-fixed platform bug, that they
// have not already recovered on their own, and (per-kind) that the failure
// still shows in whatever the console itself considers "unresolved" for that
// kind. Deliberately narrow -- see recoveryPromptEligible -- so this can
// never grow into a spam banner.
//
// @ID          getRecoveryPrompt
// @Summary     Get the caller's platform-recovery prompt, if any
// @Description Returns a prompt when the caller was hit by a now-fixed platform bug (not their own mistake) and has not already recovered on their own. Returns {"prompt": null} otherwise.
// @Tags        recovery
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{} "object with a nullable prompt field"
// @Failure     401 {object} map[string]string
// @Router      /recovery-prompt [get]
func (h *Handler) GetRecoveryPrompt(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	prompt, err := h.recoveryPromptFor(c.Request.Context(), claims.UserID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check recovery prompts")
		return
	}
	if prompt == nil {
		c.JSON(http.StatusOK, gin.H{"prompt": nil})
		return
	}

	metrics.RecordRecoveryPromptServed(prompt.Kind)
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		Action:       "PlatformRecoveryPromptServed",
		ResourceKind: "User",
		ResourceName: claims.UserID.String(),
		Outcome:      auditOutcomeSuccess,
		Metadata: map[string]any{
			"kind":      prompt.Kind,
			"failed_at": prompt.FailedAt.Format(time.RFC3339),
		},
	})
	c.JSON(http.StatusOK, gin.H{"prompt": prompt})
}

// recoveryPromptFor walks the registry newest-fix-match-wins and returns the
// single most recent eligible prompt, or nil if none of the registry's
// signatures match this user. Most-recent is decided by FailedAt, not
// registry order, so a user who hit two different closed bugs sees only the
// one that actually happened last.
func (h *Handler) recoveryPromptFor(ctx context.Context, userID uuid.UUID) (*recoveryPrompt, error) {
	var best *recoveryPrompt
	for _, fix := range platformActionFailureFixes {
		cand, err := h.recoveryPromptEligible(ctx, userID, fix)
		if err != nil {
			return nil, err
		}
		if cand == nil {
			continue
		}
		if best == nil || cand.FailedAt.After(best.FailedAt) {
			best = cand
		}
	}
	return best, nil
}

// recoveryPromptEligible checks the three gates for one registry entry, all
// of which must hold:
//
//  1. the user has a failure row on fix.Action/fix.MetaKey=fix.MetaValue
//     strictly before fix.FixedAt -- a failure at or after FixedAt is a
//     different, still-open bug, never claimed fixed here;
//  2. the user has not already succeeded at the same class of action since
//     FixedAt (recoveredSinceFix) -- they got themselves unstuck, nothing to
//     say;
//  3. for solution_install_env_failed only, the user currently owns zero
//     apps (userHasAnyApp) -- the one narrowing that is not generic across
//     the registry, because "did the install actually leave them with
//     nothing" has no equivalent for a payment failure (a failed payment
//     never creates a resource to check).
func (h *Handler) recoveryPromptEligible(ctx context.Context, userID uuid.UUID, fix actionFailureFix) (*recoveryPrompt, error) {
	var (
		projectID, environmentID *uuid.UUID
		resourceName             string
		failedAt                 time.Time
	)
	err := h.pool.QueryRow(ctx,
		`SELECT project_id, environment_id, resource_name, created_at
		 FROM audit_events
		 WHERE actor_id = $1
		   AND action = $2
		   AND outcome = 'failure'
		   AND metadata->>$3 = $4
		   AND created_at < $5
		 ORDER BY created_at DESC
		 LIMIT 1`,
		userID, fix.Action, fix.MetaKey, fix.MetaValue, fix.FixedAt,
	).Scan(&projectID, &environmentID, &resourceName, &failedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	recovered, err := h.recoveredSinceFix(ctx, userID, fix)
	if err != nil {
		return nil, err
	}
	if recovered {
		return nil, nil
	}

	if fix.Kind == recoveryKindSolutionInstallEnvFailed {
		hasApp, err := h.userHasAnyApp(ctx, userID)
		if err != nil {
			return nil, err
		}
		if hasApp {
			return nil, nil
		}
	}

	return &recoveryPrompt{
		Kind:          fix.Kind,
		FailedAt:      failedAt,
		FixedAt:       fix.FixedAt,
		ProjectID:     projectID,
		EnvironmentID: environmentID,
		ResourceName:  resourceName,
	}, nil
}

// recoveredSinceFix answers "did this user already get past this class of
// failure on their own since the fix landed." The check is necessarily
// kind-specific -- InstallSolution's own audit trail proves a retry outright,
// while CreatePaymentFailed has no matching success row at all (Checkout only
// writes audit on failure, see billing_payments.go), so a successful
// checkout has to be read off the payments table itself.
func (h *Handler) recoveredSinceFix(ctx context.Context, userID uuid.UUID, fix actionFailureFix) (bool, error) {
	switch fix.Kind {
	case recoveryKindSolutionInstallEnvFailed:
		var exists bool
		err := h.pool.QueryRow(ctx,
			`SELECT EXISTS (
			   SELECT 1 FROM audit_events
			   WHERE actor_id = $1 AND action = 'InstallSolution' AND outcome = 'success' AND created_at >= $2
			 )`,
			userID, fix.FixedAt,
		).Scan(&exists)
		return exists, err
	case recoveryKindPaymentRecurringForbidden:
		var exists bool
		err := h.pool.QueryRow(ctx,
			`SELECT EXISTS (
			   SELECT 1 FROM payments
			   WHERE created_by_sub = $1 AND yk_payment_id IS NOT NULL AND created_at >= $2
			 )`,
			userID.String(), fix.FixedAt,
		).Scan(&exists)
		return exists, err
	default:
		return false, nil
	}
}

// userHasAnyApp answers "zero apps" the way the console itself would: the
// same resource_snapshots kind='App' + notOrphanedSnapshot filter ListApps
// and every billing usage query in this package already use, joined through
// projects.owner_id rather than project_members -- project_members is
// effectively empty in prod (most projects have no row there at all), so
// scoping by it would read every solo owner as having no projects, which is
// the opposite of what this check needs.
func (h *Handler) userHasAnyApp(ctx context.Context, userID uuid.UUID) (bool, error) {
	var exists bool
	err := h.pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM resource_snapshots rs
		   JOIN projects p ON p.id = rs.project_id
		   WHERE p.owner_id = $1 AND rs.kind = 'App' AND `+notOrphanedSnapshot+`
		 )`,
		userID,
	).Scan(&exists)
	return exists, err
}
