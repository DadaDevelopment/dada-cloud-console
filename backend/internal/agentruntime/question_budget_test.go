package agentruntime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTurnCounters_ReadsMetadataAndSurvivesGarbage(t *testing.T) {
	q, p := turnCounters(Conversation{})
	require.Zero(t, q)
	require.Empty(t, p)

	q, p = turnCounters(Conversation{Metadata: map[string]any{
		questionsInRowKey: float64(2),
		usedPhrasesKey:    []any{"Сколько планируете?", "", 17, "Что скажете?"},
	}})
	require.Equal(t, 2, q)
	require.Equal(t, []string{"Сколько планируете?", "Что скажете?"}, p)

	q, _ = turnCounters(Conversation{Metadata: map[string]any{questionsInRowKey: "два"}})
	require.Zero(t, q, "an unreadable counter must not change how the agent talks")
}

func TestEndsWithQuestionAndClosingSentence(t *testing.T) {
	require.True(t, endsWithQuestion("Понял. Сколько планируете завести?"))
	require.True(t, endsWithQuestion(`Понял. "Сколько планируете?"`))
	require.False(t, endsWithQuestion("Сколько планируете? Напишу вечером."))
	require.False(t, endsWithQuestion(""))

	require.Equal(t, "Сколько планируете завести?", closingSentence("Понял. Сколько планируете завести?"))
	require.Equal(t, "Ок", closingSentence("Ок"))
	require.Equal(t, "", closingSentence("   "))
}

func TestNextCounters_ResetsOnATurnWithoutAQuestion(t *testing.T) {
	q, p := nextCounters(0, nil, "Понял. Сколько планируете?")
	require.Equal(t, 1, q)
	require.Equal(t, []string{"Сколько планируете?"}, p)

	q, p = nextCounters(q, p, "А на какой срок смотрите?")
	require.Equal(t, 2, q)

	q, p = nextCounters(q, p, "Принято, зафиксировал.")
	require.Zero(t, q, "a turn without a question resets the run")
	require.Len(t, p, 3)
}

func TestNextCounters_KeepsOnlyTheLastPhrases(t *testing.T) {
	var p []string
	q := 0
	for i := 0; i < usedPhrasesKept+3; i++ {
		q, p = nextCounters(q, p, "Фраза "+string(rune('а'+i))+"?")
	}
	require.Len(t, p, usedPhrasesKept)
	require.Equal(t, usedPhrasesKept+3, q)
}

func TestQuestionBudgetSpent_NeedsBothTheRunAndTheAmount(t *testing.T) {
	withAmount := RuntimeState{ReportedFacts: map[string]ReportedFact{amountFactKey: {Value: "1000"}}}
	empty := RuntimeState{}

	require.False(t, questionBudgetSpent(1, withAmount))
	require.False(t, questionBudgetSpent(5, empty), "with no amount recorded the lead still needs the question")
	require.True(t, questionBudgetSpent(2, withAmount))
	require.True(t, questionBudgetSpent(3, withAmount))
}

// countingStore records what the runtime would persist without needing a
// database.
type countingStore struct {
	ConversationStore
	calls    int
	lastQ    int
	lastSaid []string
}

func (s *countingStore) RecordTurnCounters(_ context.Context, _ uuid.UUID, q int, phrases []string) error {
	s.calls++
	s.lastQ = q
	s.lastSaid = phrases
	return nil
}

func TestRecordTurnCounters_WritesNothingWhileTheFlagIsOff(t *testing.T) {
	store := &countingStore{}
	rt := &Runtime{store: store, flags: runtimeFlagsFromEnv()}
	rt.recordTurnCounters(context.Background(), Conversation{ID: uuid.New()}, "Сколько планируете?")
	require.Zero(t, store.calls, "an off budget must not touch the metadata column at all")
}

func TestRecordTurnCounters_AdvancesThePairWhenOn(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_QUESTION_BUDGET", "on")
	store := &countingStore{}
	rt := &Runtime{store: store, flags: runtimeFlagsFromEnv()}
	conv := Conversation{ID: uuid.New(), Metadata: map[string]any{
		questionsInRowKey: float64(1),
		usedPhrasesKey:    []any{"Сколько планируете?"},
	}}

	rt.recordTurnCounters(context.Background(), conv, "А на какой срок смотрите?")

	require.Equal(t, 1, store.calls)
	require.Equal(t, 2, store.lastQ)
	require.Equal(t, []string{"Сколько планируете?", "А на какой срок смотрите?"}, store.lastSaid)
}

func TestQuestionBudgetEnvelopeMarker(t *testing.T) {
	render := func(ctx AgentConversationContext) string {
		return renderAgentRunAt(AgentRunRequest{AgentName: "a", ContextID: "runtime-1",
			Messages: []Message{{Role: "user", Content: "привет"}}, ConversationContext: ctx},
			time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	}
	base := AgentConversationContext{ConversationID: "1", Channel: "telegram", ExternalID: "1"}
	require.NotContains(t, render(base), "no_question_this_turn")
	require.NotContains(t, render(base), "used_phrases")

	base.NoQuestionThisTurn = true
	base.UsedPhrases = []string{"Сколько планируете?"}
	on := render(base)
	require.Contains(t, on, `"no_question_this_turn":true`)
	require.Contains(t, on, `"used_phrases":["Сколько планируете?"]`)
}
