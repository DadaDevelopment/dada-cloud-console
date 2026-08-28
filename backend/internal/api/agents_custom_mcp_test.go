package api

import (
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/kagent"
	"github.com/dada-tuda/console/backend/internal/models"
)

func testProjectMCPServer(name, project string) *unstructured.Unstructured {
	obj := testMCPServer(name, "https://"+name+".example.com/mcp")
	obj.SetLabels(map[string]string{kagent.ProjectLabel: project})
	return obj
}

// TestListAgentTools_KeepsOneTenantsServerOutOfAnothersForm: the whole agent
// runtime lives in one namespace, so without the project label the tool list is
// every tenant's MCP servers offered to every tenant -- and a checkbox is
// enough to point an agent at somebody else's server with somebody else's
// credentials behind it.
func TestListAgentTools_KeepsOneTenantsServerOutOfAnothersForm(t *testing.T) {
	h := agentTestHandler([]runtime.Object{
		testMCPServer("platform-task-tools", "http://platform/mcp"),
		testProjectMCPServer("sandbox-notion", "agent-sandbox"),
		testProjectMCPServer("neighbour-crm", "someone-else"),
	})

	c, w := agentTestContext(t, "GET", "/agents/tools?project=agent-sandbox", "", testAgentClaims())
	h.ListAgentTools(c)
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Tools []AgentToolResponse `json:"tools"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	names := map[string]AgentToolResponse{}
	for _, tool := range got.Tools {
		names[tool.Name] = tool
	}
	if _, ok := names["neighbour-crm"]; ok {
		t.Errorf("another project's MCP server must not be offered: %+v", got.Tools)
	}
	if _, ok := names["platform-task-tools"]; !ok {
		t.Errorf("a server with no project is platform infrastructure and stays on offer: %+v", got.Tools)
	}
	own, ok := names["sandbox-notion"]
	if !ok {
		t.Fatalf("the project's own server must be listed: %+v", got.Tools)
	}
	if own.URL == "" {
		t.Errorf("a project owns its own server and may see its address, got %+v", own)
	}

	c, w = agentTestContext(t, "GET", "/agents/tools?project=agent-sandbox", "",
		&auth.Claims{Groups: []string{"/platform-admins"}})
	h.ListAgentTools(c)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Tools) != 3 {
		t.Errorf("a platform admin debugging the runtime sees all three, got %+v", got.Tools)
	}
}

// TestListAgentTools_NeverReturnsTheHeadersOfAServer: the headers are how a
// third-party MCP is authorized, so the list would otherwise hand every
// authenticated user the token of every server in the runtime.
func TestListAgentTools_NeverReturnsTheHeadersOfAServer(t *testing.T) {
	server := testProjectMCPServer("sandbox-notion", "agent-sandbox")
	_ = unstructured.SetNestedSlice(server.Object, []any{
		map[string]any{"name": "Authorization", "value": "Bearer super-secret"},
	}, "spec", "headersFrom")

	h := agentTestHandler([]runtime.Object{server})
	c, w := agentTestContext(t, "GET", "/agents/tools?project=agent-sandbox", "",
		&auth.Claims{Groups: []string{"/platform-admins"}})
	h.ListAgentTools(c)
	if got := w.Body.String(); strings.Contains(got, "super-secret") {
		t.Fatalf("the token of a tool server must never leave the cluster: %s", got)
	}
}

// TestValidateAgentDraft_RefusesACustomMCPThatWouldLeakOrSilentlyFail collects
// the four ways an own MCP server is wrong in a way the cluster answers late or
// not at all: plain http carries the bearer token in the clear, an unknown
// protocol connects to nothing and the agent still starts, a ${VAR} that names
// no env sends an empty header and earns a 401 nobody can explain, and headers
// on a server this claim does not own are set somewhere else entirely.
func TestValidateAgentDraft_RefusesACustomMCPThatWouldLeakOrSilentlyFail(t *testing.T) {
	cases := map[string]struct {
		req   saveAgentRequest
		field string
	}{
		"plain http to the internet": {
			req: saveAgentRequest{
				Name: "sandbox-mcp", Prompt: "You are helpful.",
				Tools: []models.AgentToolRef{{Name: "own", URL: "http://mcp.example.com/mcp"}},
			},
			field: "tools[0].url",
		},
		"a protocol the runtime does not speak": {
			req: saveAgentRequest{
				Name: "sandbox-mcp", Prompt: "You are helpful.",
				Tools: []models.AgentToolRef{{Name: "own", URL: "https://mcp.example.com/mcp", Protocol: "WEBSOCKET"}},
			},
			field: "tools[0].protocol",
		},
		"a header referring to an env that is not there": {
			req: saveAgentRequest{
				Name: "sandbox-mcp", Prompt: "You are helpful.",
				Tools: []models.AgentToolRef{{
					Name: "own", URL: "https://mcp.example.com/mcp",
					Headers: []models.AgentToolHeader{{Name: "Authorization", Value: "Bearer ${MISSING_TOKEN}"}},
				}},
			},
			field: "tools[0].headers[0].value",
		},
		"headers on a server this claim does not own": {
			req: saveAgentRequest{
				Name: "sandbox-mcp", Prompt: "You are helpful.",
				Tools: []models.AgentToolRef{{
					Name:    "platform-task-tools",
					Headers: []models.AgentToolHeader{{Name: "Authorization", Value: "Bearer x"}},
				}},
			},
			field: "tools[0].url",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			problems := validateAgentDraft(tc.req)
			for _, p := range problems {
				if p.Field == tc.field {
					return
				}
			}
			t.Fatalf("expected a problem on %s, got %+v", tc.field, problems)
		})
	}
}

// TestValidateAgentDraft_AcceptsAnOwnServerAuthorizedFromEnv is the case the
// feature exists for: a third-party MCP over https whose token lives once, in
// the agent's own environment.
func TestValidateAgentDraft_AcceptsAnOwnServerAuthorizedFromEnv(t *testing.T) {
	problems := validateAgentDraft(saveAgentRequest{
		Name:   "sandbox-mcp",
		Prompt: "You are helpful.",
		Tools: []models.AgentToolRef{{
			Name: "sandbox-notion", URL: "https://mcp.notion.com/mcp", Protocol: "SSE",
			Headers: []models.AgentToolHeader{{Name: "Authorization", Value: "Bearer ${NOTION_TOKEN}"}},
		}},
		Env: []models.AgentEnvVar{{Name: "NOTION_TOKEN", Value: "ntn_live"}},
	})
	if len(problems) != 0 {
		t.Fatalf("a working custom MCP must pass: %+v", problems)
	}
}
