package api

import (
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/google/uuid"
)

func TestParseFeedbackRouteProjectAndApp(t *testing.T) {
	want := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	projectID, appName := parseFeedbackRoute("/projects/11111111-2222-3333-4444-555555555555/apps/web/settings")
	if projectID == nil || *projectID != want {
		t.Fatalf("project id = %v, want %s", projectID, want)
	}
	if appName != "web" {
		t.Fatalf("app name = %q, want %q", appName, "web")
	}
}

func TestParseFeedbackRouteAppWithQuery(t *testing.T) {
	_, appName := parseFeedbackRoute("/projects/11111111-2222-3333-4444-555555555555/apps/my-api?tab=logs")
	if appName != "my-api" {
		t.Fatalf("app name = %q, want %q", appName, "my-api")
	}
}

func TestParseFeedbackRouteWithoutTarget(t *testing.T) {
	projectID, appName := parseFeedbackRoute("/billing")
	if projectID != nil {
		t.Fatalf("project id = %v, want nil", projectID)
	}
	if appName != "" {
		t.Fatalf("app name = %q, want empty", appName)
	}
}

func TestParseFeedbackRouteIgnoresMalformedUUID(t *testing.T) {
	projectID, _ := parseFeedbackRoute("/projects/not-a-uuid-at-all-but-36-chars-x/apps/web")
	if projectID != nil {
		t.Fatalf("project id = %v, want nil for a non-uuid segment", projectID)
	}
}

func TestFeedbackAutofixContextLabelsTheSource(t *testing.T) {
	got := feedbackAutofixContext("  the upload page eats my files  ")
	if !strings.HasPrefix(got, "User-reported issue") {
		t.Fatalf("context does not identify the source: %q", got)
	}
	if !strings.Contains(got, "the upload page eats my files") {
		t.Fatalf("context lost the message: %q", got)
	}
	if strings.Contains(got, "  the upload") {
		t.Fatalf("context kept the untrimmed message: %q", got)
	}
}

func TestFeedbackAgeHours(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	if got := feedbackAgeHours(now.Add(-50*time.Hour), now); got != 50 {
		t.Fatalf("age = %d, want 50", got)
	}
	if got := feedbackAgeHours(now.Add(-30*time.Minute), now); got != 0 {
		t.Fatalf("age = %d, want 0", got)
	}
}

func TestDerefOr(t *testing.T) {
	s := "value"
	if got := derefOr(&s, "fallback"); got != "value" {
		t.Fatalf("derefOr(&s) = %q, want %q", got, "value")
	}
	if got := derefOr(nil, "fallback"); got != "fallback" {
		t.Fatalf("derefOr(nil) = %q, want %q", got, "fallback")
	}
}

func TestShouldNotifyAutofixOnTerminalTransition(t *testing.T) {
	tr := cloudTaskTransition{Matched: true, TaskType: "autofix", OldStatus: "running", NewStatus: "completed"}
	if !shouldNotifyAutofix(tr) {
		t.Fatal("a run that just completed must notify")
	}
}

func TestShouldNotifyAutofixSkipsRepeatCallback(t *testing.T) {
	tr := cloudTaskTransition{Matched: true, TaskType: "autofix", OldStatus: "completed", NewStatus: "completed"}
	if shouldNotifyAutofix(tr) {
		t.Fatal("a repeat callback about a finished run must stay silent")
	}
}

func TestShouldNotifyAutofixSkipsRunningAndOtherTaskTypes(t *testing.T) {
	if shouldNotifyAutofix(cloudTaskTransition{Matched: true, TaskType: "autofix", OldStatus: "running", NewStatus: "running"}) {
		t.Fatal("a still-running task must not notify")
	}
	if shouldNotifyAutofix(cloudTaskTransition{Matched: true, TaskType: "agentsync", OldStatus: "running", NewStatus: "completed"}) {
		t.Fatal("only autofix tasks notify")
	}
	if shouldNotifyAutofix(cloudTaskTransition{Matched: false, TaskType: "autofix", OldStatus: "running", NewStatus: "completed"}) {
		t.Fatal("an update that matched no row must not notify")
	}
}

// TestClaimsFromKeycloakCarriesIdentity pins the fields a public route needs
// from an authenticated caller. The optional resolver used to build its own
// struct with UserID and Groups only, which turned every signed-in support
// ticket into "аноним" in the operator email.
func TestClaimsFromKeycloakCarriesIdentity(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	kc := &auth.KeycloakClaims{
		PreferredUsername: "artem",
		Email:             "artem@example.com",
		Name:              "Artem M",
		Groups:            []string{"/orgs/dada/Owner"},
		Roles:             []string{"user"},
		Scope:             "openid email",
		SessionID:         "sess-1",
	}

	got := claimsFromKeycloak(id, kc)

	if got.UserID != id {
		t.Fatalf("user id = %s, want %s", got.UserID, id)
	}
	if got.Email != kc.Email {
		t.Fatalf("email = %q, want %q", got.Email, kc.Email)
	}
	if got.Username != kc.PreferredUsername {
		t.Fatalf("username = %q, want %q", got.Username, kc.PreferredUsername)
	}
	if got.DisplayName != kc.Name {
		t.Fatalf("display name = %q, want %q", got.DisplayName, kc.Name)
	}
	if len(got.Groups) != 1 || got.Groups[0] != kc.Groups[0] {
		t.Fatalf("groups = %v, want %v", got.Groups, kc.Groups)
	}
	if got.Scope != kc.Scope || got.SessionID != kc.SessionID {
		t.Fatalf("scope/session = %q/%q, want %q/%q", got.Scope, got.SessionID, kc.Scope, kc.SessionID)
	}
}
