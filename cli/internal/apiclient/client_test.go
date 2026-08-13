package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	c := New(srv.URL, srv.Client(), func(ctx context.Context) (string, error) {
		return "test-token", nil
	}, "CLAUDECODE")
	return c, srv
}

func TestRequestsCarryDadaHeaders(t *testing.T) {
	var gotClient, gotMarker, gotAuth string
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotClient = r.Header.Get(ClientHeaderName)
		gotMarker = r.Header.Get(AgentMarkerHeaderName)
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(listProjectsResponse{Projects: []Project{}})
	})
	defer srv.Close()

	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotClient == "" || gotClient[:4] != "cli/" {
		t.Errorf("X-Dada-Client = %q, want prefix cli/", gotClient)
	}
	if gotMarker != "CLAUDECODE" {
		t.Errorf("%s = %q, want CLAUDECODE", AgentMarkerHeaderName, gotMarker)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestListProjectsAndEnvironments(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects":
			json.NewEncoder(w).Encode(listProjectsResponse{Projects: []Project{
				{ID: "p1", Name: "myproj", Role: "owner"},
			}})
		case "/projects/p1":
			json.NewEncoder(w).Encode(getProjectResponse{
				Project: Project{ID: "p1", Name: "myproj"},
				Environments: []Environment{
					{ID: "e1", ProjectID: "p1", Name: "dev", Runtime: "k8s"},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer srv.Close()

	projects, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ID != "p1" {
		t.Fatalf("unexpected projects: %+v", projects)
	}

	envs, err := c.GetProjectEnvironments(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || envs[0].ID != "e1" {
		t.Fatalf("unexpected environments: %+v", envs)
	}
}

func TestAPIErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "not logged in"},
		{http.StatusForbidden, "write access"},
		{http.StatusNotFound, "not found"},
		{http.StatusRequestEntityTooLarge, "100MB"},
		{http.StatusServiceUnavailable, "not enabled"},
	}
	for _, tc := range cases {
		c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			json.NewEncoder(w).Encode(errorBody{Error: "raw server prose nobody should branch on"})
		})
		_, err := c.ListProjects(context.Background())
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: expected error", tc.status)
		}
		msg := Explain(err)
		if !contains(msg, tc.want) {
			t.Errorf("status %d: Explain() = %q, want substring %q", tc.status, msg, tc.want)
		}
	}
}

func TestExplainOnlyBranchesOnStatusNotProse(t *testing.T) {
	err := &APIError{Status: http.StatusForbidden, Message: "some arbitrary server text that could change any time"}
	msg := Explain(err)
	if contains(msg, "arbitrary server text") {
		t.Fatal("Explain must not depend on the server's prose for known status codes")
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestIsTerminalBuildStatusMatchesPlatformVocabulary pins the CLI's view of
// build states against the platform's own list in
// build-agent/internal/db/builds.go:14. The first version of this CLI treated
// "pushing" and "detecting" as terminal and looked for "succeeded", so a
// perfectly healthy build was reported to the user as a failure.
func TestIsTerminalBuildStatusMatchesPlatformVocabulary(t *testing.T) {
	inFlight := []string{"queued", "detecting", "building", "pushing"}
	for _, s := range inFlight {
		if IsTerminalBuildStatus(s) {
			t.Errorf("%q must not be terminal", s)
		}
	}
	for _, s := range []string{"success", "failed", "canceled"} {
		if !IsTerminalBuildStatus(s) {
			t.Errorf("%q must be terminal", s)
		}
	}
	if IsTerminalBuildStatus("some-future-status") {
		t.Error("an unknown status must be treated as in flight, not as a failed build")
	}
	if StatusSuccess != "success" {
		t.Errorf("StatusSuccess = %q, want \"success\"", StatusSuccess)
	}
}

// TestAgentMarkerHeaderMatchesServer pins the header name against the one the
// console classifies in clientClaimMiddleware. They were briefly different
// (X-Dada-Agent-Marker here, X-Dada-Agent-Session there), which loses the
// marker without any error anywhere.
func TestAgentMarkerHeaderMatchesServer(t *testing.T) {
	if AgentMarkerHeaderName != "X-Dada-Agent-Session" {
		t.Errorf("AgentMarkerHeaderName = %q, want X-Dada-Agent-Session", AgentMarkerHeaderName)
	}
	if ClientHeaderName != "X-Dada-Client" {
		t.Errorf("ClientHeaderName = %q, want X-Dada-Client", ClientHeaderName)
	}
}
