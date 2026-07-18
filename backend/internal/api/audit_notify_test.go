package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/google/uuid"
)

// TestNotifyDeployHook_NoopWithoutNotifier confirms notifyDeployHook is a
// silent no-op (no goroutine spawned, no pool touched, no panic) when the
// handler has no configured notifier -- the common case in tests and any
// deployment that never sets SMTP_FROM/DEPLOY_HOOK_NOTIFY_EMAIL.
func TestNotifyDeployHook_NoopWithoutNotifier(t *testing.T) {
	h := &Handler{}
	h.notifyDeployHook(uuid.New(), "DeployImageVersion", "myapp", "CI (deploy-hook)")
}

// TestNotifyDeployHook_NoopWithoutRecipient confirms notifyDeployHook no-ops
// when auditNotifier is set but deployHookNotifyEmail resolved to "" (both
// DEPLOY_HOOK_NOTIFY_EMAIL and SMTP_FROM unset) -- it must not dereference
// h.pool in that case.
func TestNotifyDeployHook_NoopWithoutRecipient(t *testing.T) {
	h := &Handler{auditNotifier: nil, deployHookNotifyEmail: ""}
	h.notifyDeployHook(uuid.New(), "CreateDeployHook", "myapp", "user@example.com")
}

func TestActorLabelFromClaims(t *testing.T) {
	cases := []struct {
		name   string
		claims *auth.Claims
		want   string
	}{
		{"nil claims", nil, ""},
		{"prefers email", &auth.Claims{Email: "user@example.com", Username: "user"}, "user@example.com"},
		{"falls back to username", &auth.Claims{Username: "user"}, "user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := actorLabelFromClaims(tc.claims); got != tc.want {
				t.Errorf("actorLabelFromClaims() = %q, want %q", got, tc.want)
			}
		})
	}
}
