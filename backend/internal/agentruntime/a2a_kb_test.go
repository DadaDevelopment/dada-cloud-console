package agentruntime

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
)

// Only history parts stamped kagent_type=function_response and named
// kb_search count; the query comes from the matching function_call. A
// data.name+response part without that metadata, another tool's response and
// a call part are all skipped.
func TestKBResults_ReadsFunctionResponsesFromHistory(t *testing.T) {
	raw, err := os.ReadFile("testdata/a2a_task_kb_search.json")
	require.NoError(t, err)
	require.Equal(t, []agentjudge.KBResult{{Query: "комиссия за пополнение", Text: "Пополнение без комиссии."}}, kbResults(json.RawMessage(raw)))
	require.Equal(t, "Комиссия 0 %.", extractText(json.RawMessage(raw)), "customer text is unchanged by the tool parts")
}

func TestKBResults_EmptyAndUnreadable(t *testing.T) {
	require.Nil(t, kbResults(json.RawMessage(`{"history":[]}`)))
	require.Nil(t, kbResults(json.RawMessage(`not json`)))
	got := kbResults(json.RawMessage(`{"history":[{"parts":[{"kind":"data","data":{"id":"a","name":"kb_search","response":{"hits":[1]}},"metadata":{"kagent_type":"function_response"}}]}]}`))
	require.Equal(t, []agentjudge.KBResult{{Text: `{"hits":[1]}`}}, got, "a response without text blocks is kept as JSON, an unknown call has no query")
}

// Only escalate_to_operator function_call parts count; the code is read
// from args.reason_code and upper-cased as the tool handler reads it.
func TestEscalationCalls_ReadsModelHandOffCodes(t *testing.T) {
	raw := `{"history":[{"parts":[
		{"kind":"data","data":{"id":"a","name":"escalate_to_operator","args":{"reason_code":" e_check_and_return ","summary":"s"}},"metadata":{"kagent_type":"function_call"}},
		{"kind":"data","data":{"id":"a","name":"escalate_to_operator","response":{"ok":true}},"metadata":{"kagent_type":"function_response"}},
		{"kind":"data","data":{"id":"b","name":"escalate_to_operator","args":{"reason_code":"E_OTHER"}}},
		{"kind":"data","data":{"id":"c","name":"kb_search","args":{"reason_code":"E_OTHER"}},"metadata":{"kagent_type":"function_call"}}
	]}]}`
	require.Equal(t, []string{"E_CHECK_AND_RETURN"}, escalationCalls(json.RawMessage(raw)))
	require.Nil(t, escalationCalls(json.RawMessage(`not json`)))
}

// A call counts only when its function_response went through: the tool's
// {status_code, body} with a rejection (body.error_code), an MCP isError
// result, an ADK {"error": ...}, a result whose text block holds the
// rejection, and a call with no response at all are not listed.
func TestEscalationCalls_OnlySuccessfulResponses(t *testing.T) {
	raw := `{"history":[{"parts":[
		{"kind":"data","data":{"id":"ok","name":"escalate_to_operator","args":{"reason_code":"E_DISTRUST"}},"metadata":{"kagent_type":"function_call"}},
		{"kind":"data","data":{"id":"ok","name":"escalate_to_operator","response":{"content":[{"type":"text","text":"{\"status_code\":200,\"body\":{\"agent_enabled\":true,\"mode\":\"signal\",\"operator_notified\":true}}"}],"isError":false}},"metadata":{"kagent_type":"function_response"}},
		{"kind":"data","data":{"id":"rej","name":"escalate_to_operator","args":{"reason_code":"E_CHECK_AND_RETURN"}},"metadata":{"kagent_type":"function_call"}},
		{"kind":"data","data":{"id":"rej","name":"escalate_to_operator","response":{"status_code":400,"body":{"updated":false,"error":"unknown escalation reason","error_code":"unknown_reason_code"}}},"metadata":{"kagent_type":"function_response"}},
		{"kind":"data","data":{"id":"txt","name":"escalate_to_operator","args":{"reason_code":"E_LOST_MONEY"}},"metadata":{"kagent_type":"function_call"}},
		{"kind":"data","data":{"id":"txt","name":"escalate_to_operator","response":{"content":[{"type":"text","text":"{\"status_code\":400,\"body\":{\"error_code\":\"empty_summary\"}}"}]}},"metadata":{"kagent_type":"function_response"}},
		{"kind":"data","data":{"id":"mcp","name":"escalate_to_operator","args":{"reason_code":"E_WITHDRAW"}},"metadata":{"kagent_type":"function_call"}},
		{"kind":"data","data":{"id":"mcp","name":"escalate_to_operator","response":{"content":[{"type":"text","text":"timeout"}],"isError":true}},"metadata":{"kagent_type":"function_response"}},
		{"kind":"data","data":{"id":"adk","name":"escalate_to_operator","args":{"reason_code":"E_OTHER"}},"metadata":{"kagent_type":"function_call"}},
		{"kind":"data","data":{"id":"adk","name":"escalate_to_operator","response":{"error":"tool crashed"}},"metadata":{"kagent_type":"function_response"}},
		{"kind":"data","data":{"id":"none","name":"escalate_to_operator","args":{"reason_code":"E_TECH_BLOCKED"}},"metadata":{"kagent_type":"function_call"}}
	]}]}`
	require.Equal(t, []string{"E_DISTRUST"}, escalationCalls(json.RawMessage(raw)))
}

func TestWithEarlierAttempts_UnionsKBOnceAndKeepsCalls(t *testing.T) {
	a, b := agentjudge.KBResult{Query: "a", Text: "1"}, agentjudge.KBResult{Query: "b", Text: "2"}
	prev := A2AReply{Text: "old", KB: []agentjudge.KBResult{a}, Escalations: []string{"E_DISTRUST"}}
	got := withEarlierAttempts(prev, A2AReply{Text: "new", TraceID: "t", KB: []agentjudge.KBResult{a, b}})
	require.Equal(t, A2AReply{Text: "new", TraceID: "t", KB: []agentjudge.KBResult{a, b}, Escalations: []string{"E_DISTRUST"}}, got)
	require.Equal(t, []agentjudge.KBResult{a}, prev.KB, "prev is not aliased")
	require.Equal(t, A2AReply{Text: "x"}, withEarlierAttempts(A2AReply{}, A2AReply{Text: "x"}), "nothing seen stays nil")
}
