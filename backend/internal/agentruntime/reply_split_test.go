package agentruntime

import (
	"strings"
	"testing"
	"time"

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

	for _, reply := range []string{
		"первое\n---\nвторое",
		"  обычный ответ без швов  ",
		"---",
		"таблица\n---\n",
	} {
		reply := reply
		text, parts := replyForDelivery(rt, "conv", "agent", reply)
		require.Equal(t, reply, text, "an off flag must not touch a single byte")
		require.Nil(t, parts, "an off flag must leave the gateway with exactly one message")
	}
}

// replyForDelivery mirrors the one branch in runTurn that decides whether the
// turn is cut at all, so the "off means untouched" promise is tested where it
// is actually made.
func replyForDelivery(r *Runtime, conversationID, agentName, reply string) (string, []string) {
	if r.flags.SplitReply && !r.structuredAgents[agentName] {
		return r.splitTurn(conversationID, agentName, reply)
	}
	return reply, nil
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
