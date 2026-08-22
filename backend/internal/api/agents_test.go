package api

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
)

func fieldsOf(problems []AgentFieldError) []string {
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, p.Field)
	}
	return out
}

// A draft that is fine must produce no problems: the validator sits in front of
// every save, so a false refusal here is a lever nobody can pull.
func TestValidateAgentDraft_AcceptsAWorkingAgent(t *testing.T) {
	problems := validateAgentDraft(saveAgentRequest{
		Name:   "reels-poc",
		Prompt: "Ты помощник.\n\nОтвечай коротко.",
		Tools: []models.AgentToolRef{
			{Name: "reels-task-tools", AllowedHeaders: []string{"x-dada-user"}},
			{Name: "shared-tools"},
		},
		Env: []models.AgentEnvVar{{Name: "AGENTSYNC_BASE_URL", Value: "https://agentsync.dada-tuda.ru"}},
	})
	if len(problems) != 0 {
		t.Fatalf("a valid draft was refused: %#v", problems)
	}
}

// An empty prompt is the failure worth catching before git: the agent starts,
// answers, and answers as the bare model.
func TestValidateAgentDraft_RefusesAnEmptyPrompt(t *testing.T) {
	problems := validateAgentDraft(saveAgentRequest{Name: "reels-poc", Prompt: "   \n"})
	if len(problems) != 1 || problems[0].Field != "prompt" {
		t.Fatalf("want one prompt problem, got %#v", problems)
	}
}

// A bad name has to be refused on the field: Kubernetes would refuse it too,
// but minutes later and as an Argo sync failure nobody reads.
func TestValidateAgentDraft_RefusesANameKubernetesWouldReject(t *testing.T) {
	for _, name := range []string{"", "Reels_POC", "-reels", "reels-", strings.Repeat("a", 64)} {
		problems := validateAgentDraft(saveAgentRequest{Name: name, Prompt: "Be brief."})
		if len(problems) == 0 {
			t.Errorf("name %q must be refused", name)
		}
	}
}

// Two references to the same MCP server compose two claims on one name, which
// is a fight, not a merge: the loser's tools vanish from the agent that
// declared them.
func TestValidateAgentDraft_RefusesADuplicateToolReference(t *testing.T) {
	problems := validateAgentDraft(saveAgentRequest{
		Name:   "reels-poc",
		Prompt: "Be brief.",
		Tools:  []models.AgentToolRef{{Name: "shared-tools"}, {Name: "shared-tools"}},
	})
	if len(problems) != 1 || problems[0].Field != "tools[1].name" {
		t.Fatalf("want a duplicate-tool problem, got %#v", problems)
	}
}

// A header the runtime will not forward is silent breakage: the agent runs, the
// downstream call arrives without the caller's identity, and the tool answers
// for the wrong user or for nobody.
func TestValidateAgentDraft_RefusesAMalformedAllowedHeader(t *testing.T) {
	problems := validateAgentDraft(saveAgentRequest{
		Name:   "reels-poc",
		Prompt: "Be brief.",
		Tools:  []models.AgentToolRef{{Name: "shared-tools", AllowedHeaders: []string{"X-Dada User"}}},
		Env:    []models.AgentEnvVar{{Name: ""}},
	})
	got := fieldsOf(problems)
	if len(got) != 2 || got[0] != "tools[0].allowed_headers[0]" || got[1] != "env[0].name" {
		t.Fatalf("want both problems reported at once, got %#v", problems)
	}
}
