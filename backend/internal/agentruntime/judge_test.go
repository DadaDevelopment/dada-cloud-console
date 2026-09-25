package agentruntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type tracedRunFunc func(context.Context, AgentRunRequest) (A2AReply, error)

func (f tracedRunFunc) Send(ctx context.Context, r AgentRunRequest) (string, error) {
	reply, err := f(ctx, r)
	return reply.Text, err
}

func (f tracedRunFunc) SendTraced(ctx context.Context, r AgentRunRequest) (A2AReply, error) {
	return f(ctx, r)
}

type recordingJudge struct {
	agent string
	turns []agentjudge.Turn
}

func (j *recordingJudge) Submit(agent string, t agentjudge.Turn) {
	j.agent = agent
	j.turns = append(j.turns, t)
}

func (j *recordingJudge) Check(context.Context, string, agentjudge.Turn) (agentjudge.Precheck, error) {
	return agentjudge.Precheck{}, nil
}

func (j *recordingJudge) Criterion(string, string) (agentjudge.Criterion, bool) {
	return agentjudge.Criterion{}, false
}

func TestPGJudgeReceivesTurnWithHistoryAndTrace(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agent := "judge-" + uuid.NewString()
	conv, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "9", Actor{ExternalID: "9", Username: "client"})
	require.NoError(t, err)
	earlier, err := store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "500 долларов"})
	require.NoError(t, err)
	require.NoError(t, store.MarkRuntimeHandled(ctx, []Message{earlier}))
	_, err = store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "assistant", Content: "Счёт у FxPro есть?"})
	require.NoError(t, err)
	_, err = store.ApplyState(ctx, conv.ID, 0, StatePatch{ReportedFacts: map[string]ReportedFact{amountFactKey: {Value: "500 долларов", SourceMessageID: earlier.ID}}})
	require.NoError(t, err)

	judge := &recordingJudge{}
	rt := NewRuntime(store, testHooks{}, tracedRunFunc(func(ctx context.Context, run AgentRunRequest) (A2AReply, error) {
		return A2AReply{Text: "Вот ссылка. Напишите, когда зарегистрируетесь", TraceID: "0af7651916cd43dd8448eb211c80319c", ObservationID: "b7ad6b7169203331"}, nil
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.judge = judge

	out, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "9", Messages: []InboundMessage{{Content: "нет", ChannelMessageID: "1"}, {Content: "давайте ссылку", ChannelMessageID: "2"}}})
	require.NoError(t, err)
	require.NotEmpty(t, out.Text)
	require.Len(t, judge.turns, 1)
	require.Equal(t, agent, judge.agent)

	turn := judge.turns[0]
	require.Equal(t, "0af7651916cd43dd8448eb211c80319c", turn.TraceID)
	require.Equal(t, "b7ad6b7169203331", turn.ObservationID)
	require.Equal(t, []string{"нет", "давайте ссылку"}, turn.Incoming)
	require.Equal(t, out.Text, turn.Reply)
	require.Equal(t, []agentjudge.Exchange{{Role: "client", Text: "500 долларов"}, {Role: "roman", Text: "Счёт у FxPro есть?"}}, turn.History)

	var packed map[string]any
	require.NoError(t, json.Unmarshal([]byte(turn.Context), &packed))
	require.Equal(t, "client", packed["username"])
	facts := packed["reported_facts"].(map[string]any)
	require.Contains(t, facts, amountFactKey)
}

func TestPGJudgeSkippedWithoutTrace(t *testing.T) {
	store := setupTestStore(t).(*pgStore)
	ctx := context.Background()
	agent := "judge-" + uuid.NewString()

	judge := &recordingJudge{}
	rt := NewRuntime(store, testHooks{}, runFunc(func(ctx context.Context, run AgentRunRequest) (string, error) {
		return "Счёт у FxPro есть?", nil
	}), nil)
	rt.contextKey = []byte(testRuntimeToken)
	rt.judge = judge

	_, err := rt.ProcessMessage(ctx, MessageRequest{AgentName: agent, Channel: "telegram", ExternalID: "10", Messages: []InboundMessage{{Content: "привет", ChannelMessageID: "1"}}})
	require.NoError(t, err)
	require.Empty(t, judge.turns)
}
