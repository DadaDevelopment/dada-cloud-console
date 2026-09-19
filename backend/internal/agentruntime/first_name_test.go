package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestRenderAgentRunCarriesFirstName: the prompt greets by
// runtime_context.first_name, so the envelope must carry the channel's first
// name and drop the key when the channel sent none.
func TestRenderAgentRunCarriesFirstName(t *testing.T) {
	run := testRun()
	run.ConversationContext.FirstName = actorFirstName(map[string]any{"first_name": " Иван "})
	var env struct {
		Context map[string]any `json:"runtime_context"`
	}
	rendered := renderAgentRunAt(run, time.Unix(1_700_000_000, 0))
	if err := json.Unmarshal([]byte(rendered[strings.Index(rendered, "{"):]), &env); err != nil {
		t.Fatalf("envelope json: %v", err)
	}
	if env.Context["first_name"] != "Иван" {
		t.Fatalf("first_name = %v, want Иван", env.Context["first_name"])
	}

	run.ConversationContext.FirstName = actorFirstName(map[string]any{"username": "x"})
	rendered = renderAgentRunAt(run, time.Unix(1_700_000_000, 0))
	if strings.Contains(rendered, `"first_name"`) {
		t.Fatalf("first_name must be omitted when unknown: %s", rendered)
	}
}
