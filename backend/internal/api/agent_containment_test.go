package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
)

// TestResolveRole_AgentConfinement pins the containment contract for automation
// identities: an agent granted one project sees that project and nothing else.
// This is the gate that stops every Claude Code session from minting a fresh test
// project in org dada.
func TestResolveRole_AgentConfinement(t *testing.T) {
	agent := &auth.Claims{
		Username: "service-account-dada-routine-svc",
		Groups: []string{
			"/agents",
			"/orgs/dada/projects/agent-sandbox/Owner",
		},
	}

	if role, ok := resolveRole(agent, "dada", "agent-sandbox"); !ok || role != models.MemberRoleOwner {
		t.Errorf("sandbox: resolveRole = (%q, ok=%v), want (Owner, ok=true)", role, ok)
	}
	if role, ok := resolveRole(agent, "dada", "internal"); ok {
		t.Errorf("sibling project in the same org: resolveRole = (%q, ok=%v), want denied", role, ok)
	}
	if role, ok := resolveRole(agent, "service-account-dada-routine-svc", "anything"); ok {
		t.Errorf("own personal org: resolveRole = (%q, ok=%v), want denied", role, ok)
	}
}

// TestResolveRole_PlatformAnalyst pins read-only staff access: every project
// readable at ReadOnly, and never more than the caller's real grant where one
// exists.
func TestResolveRole_PlatformAnalyst(t *testing.T) {
	analyst := &auth.Claims{Username: "svc-analytics", Groups: []string{"/platform-analysts"}}

	role, ok := resolveRole(analyst, "someone-else", "p1")
	if !ok || role != models.MemberRoleReadOnly {
		t.Errorf("foreign project: resolveRole = (%q, ok=%v), want (ReadOnly, ok=true)", role, ok)
	}

	boosted := &auth.Claims{
		Username: "svc-analytics",
		Groups:   []string{"/platform-analysts", "/orgs/dada/projects/agent-sandbox/Owner"},
	}
	if role, ok := resolveRole(boosted, "dada", "agent-sandbox"); !ok || role != models.MemberRoleOwner {
		t.Errorf("explicit grant must win over the analyst floor: resolveRole = (%q, ok=%v), want (Owner, ok=true)", role, ok)
	}
}

// TestAdminReaderGate pins which staff group opens which door: analysts read the
// admin dashboards, only platform-admins write.
func TestAdminReaderGate(t *testing.T) {
	god := &auth.Claims{Groups: []string{"/platform-admins"}}
	analyst := &auth.Claims{Groups: []string{"/platform-analysts"}}
	agent := &auth.Claims{Groups: []string{"/agents"}}
	user := &auth.Claims{Username: "alexkekiy", Groups: []string{"/orgs/dada/Owner"}}

	cases := []struct {
		name        string
		claims      *auth.Claims
		wantReader  bool
		wantWriter  bool
		wantIsAgent bool
	}{
		{"platform-admin reads and writes", god, true, true, false},
		{"analyst reads, never writes", analyst, true, false, false},
		{"agent gets neither", agent, false, false, true},
		{"org owner is not staff", user, false, false, false},
		{"nil claims get nothing", nil, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAdminReader(tc.claims); got != tc.wantReader {
				t.Errorf("isAdminReader = %v, want %v", got, tc.wantReader)
			}
			if got := isGod(tc.claims); got != tc.wantWriter {
				t.Errorf("isGod = %v, want %v", got, tc.wantWriter)
			}
			if got := isAgent(tc.claims); got != tc.wantIsAgent {
				t.Errorf("isAgent = %v, want %v", got, tc.wantIsAgent)
			}
		})
	}
}
