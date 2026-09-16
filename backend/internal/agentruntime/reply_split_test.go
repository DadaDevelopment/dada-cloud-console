package agentruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func splitRuntime(t *testing.T, on bool) *Runtime {
	t.Helper()
	if on {
		t.Setenv("AGENT_RUNTIME_SPLIT_REPLY", "1")
	}
	return &Runtime{flags: runtimeFlagsFromEnv()}
}

func TestSplitReplyParts(t *testing.T) {
	require.Equal(t, []string{"один"}, splitReplyParts("один"))
	require.Equal(t, []string{"один", "два"}, splitReplyParts("один\n---\nдва"))
	require.Equal(t, []string{"один", "два"}, splitReplyParts("один\n  ----  \n\n\nдва\n"))
	require.Empty(t, splitReplyParts("---\n---"))
	require.Equal(t, []string{"тире - внутри строки не шов"}, splitReplyParts("тире - внутри строки не шов"))
	require.Equal(t, []string{"один", "два"}, splitReplyParts("один\r\n---\r\nдва"))
}

func TestCapReplyParts_ExtraPartsFoldIntoTheLast(t *testing.T) {
	got := capReplyParts([]string{"a", "b", "c", "d", "e"})
	require.Equal(t, []string{"a", "b", "c d e"}, got, "nothing the model wrote may be dropped")
	require.Equal(t, []string{"a", "b"}, capReplyParts([]string{"a", "b"}))
}

// With the flag off the turn must reach the customer byte for byte as the
// model wrote it: splitTurn is not called at all, so nothing is trimmed,
// reglued or stripped.
func TestSplitTurn_FlagOffLeavesTheReplyUntouched(t *testing.T) {
	rt := splitRuntime(t, false)
	require.False(t, rt.flags.SplitReply)

	conv := Conversation{ID: uuid.New(), AgentName: "agent"}
	for _, reply := range []string{
		"первое\n---\nвторое",
		"  обычный ответ без швов  ",
		"---",
		"таблица\n---\n",
	} {
		text, parts := rt.splitForDelivery(conv, reply)
		require.Equal(t, reply, text, "an off flag must not touch a single byte")
		require.Nil(t, parts, "an off flag must leave the gateway with exactly one message")
	}
}

// The structured branch is out of scope even with the flag on.
func TestSplitForDelivery_StructuredAgentIsNeverCut(t *testing.T) {
	rt := splitRuntime(t, true)
	rt.structuredAgents = map[string]bool{"structured": true}
	conv := Conversation{ID: uuid.New(), AgentName: "structured"}

	text, parts := rt.splitForDelivery(conv, "первое\n---\nвторое")
	require.Equal(t, "первое\n---\nвторое", text)
	require.Nil(t, parts)
}

// The PG test is the authoritative one: it runs the whole turn through
// ProcessMessage and compares the stored transcript with what the agent
// returned, byte for byte.
func TestPGProcessMessage_SplitOffStoresTheAgentReplyVerbatim(t *testing.T) {
	const agentReply = "  Принял.\n---\nСчёт открыли?\n"
	f := newRecoveryFixture(t, func(int, AgentRunRequest) (string, error) { return agentReply, nil })
	require.False(t, f.srv.runtime.flags.SplitReply, "the flag is off unless a test sets it")

	resp, err := f.srv.runtime.ProcessMessage(context.Background(), f.request("1", "готово"))
	require.NoError(t, err)
	require.Equal(t, agentReply, resp.Text, "the turn must reach the gateway exactly as the agent wrote it")
	require.Nil(t, resp.Messages)
	require.Equal(t, []string{agentReply}, f.assistantMessages(t), "and be stored exactly as the agent wrote it")
}

// With the flag on the same turn is cut, and the transcript keeps the glued
// form while the gateway gets the parts.
func TestPGProcessMessage_SplitOnCutsTheTurnAndKeepsOneTranscriptRow(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_SPLIT_REPLY", "1")
	f := newRecoveryFixture(t, func(int, AgentRunRequest) (string, error) { return "Принял.\n---\nСчёт открыли?", nil })
	require.True(t, f.srv.runtime.flags.SplitReply)

	resp, err := f.srv.runtime.ProcessMessage(context.Background(), f.request("1", "готово"))
	require.NoError(t, err)
	require.Equal(t, []string{"Принял.", "Счёт открыли?"}, resp.Messages)
	require.Equal(t, "Принял. Счёт открыли?", resp.Text)
	require.Equal(t, []string{"Принял. Счёт открыли?"}, f.assistantMessages(t))
}

func TestSplitTurn_FlagOnCutsUpToThree(t *testing.T) {
	rt := splitRuntime(t, true)
	text, parts := rt.splitTurn("conv", "agent", "раз\n---\nдва\n---\nтри\n---\nчетыре")
	require.Equal(t, []string{"раз", "два", "три четыре"}, parts)
	require.Equal(t, "раз два три четыре", text, "Text stays the whole turn for an old gateway and the transcript")
}

func TestSplitTurn_FlagOnSingleMessageStaysSingle(t *testing.T) {
	rt := splitRuntime(t, true)
	text, parts := rt.splitTurn("conv", "agent", "только одно сообщение")
	require.Equal(t, "только одно сообщение", text)
	require.Nil(t, parts)
}

func TestSplitTurn_AllSeparatorsKeepTheOriginal(t *testing.T) {
	rt := splitRuntime(t, true)
	text, parts := rt.splitTurn("conv", "agent", "---")
	require.Equal(t, "---", text)
	require.Nil(t, parts)
}

func TestSplitTurn_LongPartAndMixedLinkOnlyWarn(t *testing.T) {
	rt := splitRuntime(t, true)
	long := strings.Repeat("я", replySplitPartRunes+10)
	text, parts := rt.splitTurn("conv", "agent", long+"\n---\nвот ссылка https://example.com смотри")
	require.Len(t, parts, 2, "form problems are logged, never held back")
	require.Contains(t, text, long)
}

func TestReplySplitEnvelopeMarker(t *testing.T) {
	render := func(split bool) string {
		return renderAgentRunAt(AgentRunRequest{
			AgentName: "a", ContextID: "runtime-1",
			Messages:            []Message{{Role: "user", Content: "привет"}},
			ConversationContext: AgentConversationContext{ConversationID: "1", Channel: "telegram", ExternalID: "1", ReplySplit: split},
		}, time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	}
	require.NotContains(t, render(false), "reply_split")
	require.Contains(t, render(true), `"reply_split":true`)
}
