package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
)

// TestRepoScopeName pins the strict split: the name is what the minted token is
// scoped to, so a guess would hand out a credential for the wrong repository.
func TestRepoScopeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"dada-tuda/console", "console", true},
		{"  dada-tuda/console  ", "console", true},
		{"console", "", false},
		{"dada-tuda/console/extra", "", false},
		{"/console", "", false},
		{"dada-tuda/", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := repoScopeName(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("repoScopeName(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestAgentMintInstallToken_AuthGate: minting is the only write among the
// agent-git endpoints, so the gate is checked before anything else. A caller
// that is not the agent's own client gets no credential, and an unconfigured
// verifier refuses rather than falling open.
func TestAgentMintInstallToken_AuthGate(t *testing.T) {
	h := &Handler{cfg: &config.Config{GithubAppID: "1", GithubAppPrivateKey: "pem"}}
	cases := []struct {
		name       string
		authHeader string
		verifier   tokenVerifier
		want       int
	}{
		{"missing bearer", "", fakeVerifier{claims: &auth.KeycloakClaims{Azp: "dada-agent"}}, http.StatusUnauthorized},
		{"verify error", "Bearer bad", fakeVerifier{err: errors.New("nope")}, http.StatusUnauthorized},
		{"wrong client", "Bearer ok", fakeVerifier{claims: &auth.KeycloakClaims{Azp: "box-agent"}}, http.StatusForbidden},
		{"unconfigured", "Bearer ok", nil, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		c, rec := newWebhookCtx(t, tc.authHeader, `{"project_id":"`+uuidZero+`","repo":"o/n"}`)
		h.agentMintInstallToken(c, tc.verifier)
		if rec.Code != tc.want {
			t.Errorf("%s: code = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

// TestAgentMintInstallToken_RefusesBeforeGitHub covers the checks that must
// happen while the request is still cheap: no App credentials configured, an
// unparseable project, or a repo that is not owner/name. None of these may reach
// GitHub, and none may answer with a token.
func TestAgentMintInstallToken_RefusesBeforeGitHub(t *testing.T) {
	agent := fakeVerifier{claims: &auth.KeycloakClaims{Azp: "dada-agent"}}
	cases := []struct {
		name string
		cfg  *config.Config
		body string
		want int
	}{
		{"app not configured", &config.Config{}, `{"project_id":"` + uuidZero + `","repo":"o/n"}`, http.StatusServiceUnavailable},
		{"bad project", configWithApp(), `{"project_id":"not-a-uuid","repo":"o/n"}`, http.StatusBadRequest},
		{"repo without owner", configWithApp(), `{"project_id":"` + uuidZero + `","repo":"console"}`, http.StatusBadRequest},
		{"repo missing", configWithApp(), `{"project_id":"` + uuidZero + `"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		h := &Handler{cfg: tc.cfg}
		c, rec := newWebhookCtx(t, "Bearer ok", tc.body)
		h.agentMintInstallToken(c, agent)
		if rec.Code != tc.want {
			t.Errorf("%s: code = %d, want %d, body = %s", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func configWithApp() *config.Config {
	return &config.Config{GithubAppID: "1", GithubAppPrivateKey: "pem"}
}

const uuidZero = "00000000-0000-0000-0000-000000000000"
