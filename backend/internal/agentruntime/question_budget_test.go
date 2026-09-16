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
	calls       int
	lastAsked   bool
	lastClosing string
	lastKeep    int
}

func (s *countingStore) RecordTurnCounters(_ context.Context, _ uuid.UUID, asked bool, closing string, keep int) error {
	s.calls++
	s.lastAsked = asked
	s.lastClosing = closing
	s.lastKeep = keep
	return nil
}

func TestRecordTurnCounters_WritesNothingWhileTheFlagIsOff(t *testing.T) {
	store := &countingStore{}
	rt := &Runtime{store: store, flags: runtimeFlagsFromEnv()}
	rt.recordTurnCounters(context.Background(), Conversation{ID: uuid.New()}, "Сколько планируете?")
	require.Zero(t, store.calls, "an off budget must not touch the metadata column at all")
}

// The runtime reports what the turn DID; the arithmetic is the store's, done
// inside the UPDATE (review M2), so nothing here depends on a counter read a
// moment earlier.
func TestRecordTurnCounters_ReportsTheTurnNotTheResultWhenOn(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_QUESTION_BUDGET", "on")
	store := &countingStore{}
	rt := &Runtime{store: store, flags: runtimeFlagsFromEnv()}
	conv := Conversation{ID: uuid.New(), Metadata: map[string]any{
		questionsInRowKey: float64(1),
		usedPhrasesKey:    []any{"Сколько планируете?"},
	}}

	rt.recordTurnCounters(context.Background(), conv, "Понял. А на какой срок смотрите?")

	require.Equal(t, 1, store.calls)
	require.True(t, store.lastAsked)
	require.Equal(t, "А на какой срок смотрите?", store.lastClosing)
	require.Equal(t, usedPhrasesKept, store.lastKeep)

	rt.recordTurnCounters(context.Background(), conv, "Принято, зафиксировал.")
	require.False(t, store.lastAsked, "a turn without a question resets the run in SQL")
	require.Equal(t, "Принято, зафиксировал.", store.lastClosing)
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

// Review M2: the arithmetic lives in the UPDATE. This pins what the SQL does
// with a real database: increments, resets, appends, trims, and refuses a
// conversation that is not there.
func TestPGRecordTurnCounters_IncrementsInsideTheStatement(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	conv, _, err := store.GetOrCreateConversation(ctx, "counters-test-"+uuid.NewString(), "telegram", "9001", Actor{ExternalID: "9001"})
	require.NoError(t, err)

	read := func() (int, []string) {
		fresh, err := store.GetConversation(ctx, conv.ID)
		require.NoError(t, err)
		return turnCounters(fresh)
	}

	require.NoError(t, store.RecordTurnCounters(ctx, conv.ID, true, "Сколько планируете?", 3))
	q, phrases := read()
	require.Equal(t, 1, q)
	require.Equal(t, []string{"Сколько планируете?"}, phrases)

	require.NoError(t, store.RecordTurnCounters(ctx, conv.ID, true, "На какой срок?", 3))
	q, phrases = read()
	require.Equal(t, 2, q, "the increment must come from the row, not from a value read earlier")
	require.Equal(t, []string{"Сколько планируете?", "На какой срок?"}, phrases)

	require.NoError(t, store.RecordTurnCounters(ctx, conv.ID, false, "Принято, зафиксировал.", 3))
	q, phrases = read()
	require.Zero(t, q, "a turn without a question resets the run")
	require.Len(t, phrases, 3)

	require.NoError(t, store.RecordTurnCounters(ctx, conv.ID, true, "А счёт открыли?", 3))
	_, phrases = read()
	require.Equal(t, []string{"На какой срок?", "Принято, зафиксировал.", "А счёт открыли?"}, phrases, "only the newest keep phrases survive, in order")

	require.NoError(t, store.RecordTurnCounters(ctx, conv.ID, true, "", 3))
	q, phrases = read()
	require.Equal(t, 2, q)
	require.Len(t, phrases, 3, "an empty closing adds nothing")

	require.Error(t, store.RecordTurnCounters(ctx, uuid.New(), true, "нет такой беседы", 3))
}

// Review M5: both narrow-mode statements report a missing conversation
// instead of quietly writing nothing.
func TestPGNarrowMode_EnterAndClearReportAMissingConversation(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	conv, _, err := store.GetOrCreateConversation(ctx, "narrow-store-test-"+uuid.NewString(), "telegram", "9002", Actor{ExternalID: "9002"})
	require.NoError(t, err)

	require.NoError(t, store.EnterNarrowMode(ctx, conv.ID))
	fresh, err := store.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	_, inNarrow := narrowSince(fresh)
	require.True(t, inNarrow)

	require.NoError(t, store.ClearNarrowMode(ctx, conv.ID))
	fresh, err = store.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	_, inNarrow = narrowSince(fresh)
	require.False(t, inNarrow)

	require.Error(t, store.EnterNarrowMode(ctx, uuid.New()))
	require.Error(t, store.ClearNarrowMode(ctx, uuid.New()))
}

// Review, open item: metadata->'used_phrases' can hold the JSON literal null.
// COALESCE does not catch it (the key IS present), and jsonb_array_elements
// on null fails the whole statement, so the counter would stop advancing for
// that conversation forever.
func TestPGRecordTurnCounters_SurvivesAJsonbNullInUsedPhrases(t *testing.T) {
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	conv, _, err := store.GetOrCreateConversation(ctx, "counters-null-"+uuid.NewString(), "telegram", "9003", Actor{ExternalID: "9003"})
	require.NoError(t, err)

	_, err = store.pool.Exec(ctx, `
		UPDATE conversations
		SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{used_phrases}', 'null'::jsonb, true)
		WHERE id = $1`, conv.ID)
	require.NoError(t, err)

	require.NoError(t, store.RecordTurnCounters(ctx, conv.ID, true, "Сколько планируете?", 3))

	fresh, err := store.GetConversation(ctx, conv.ID)
	require.NoError(t, err)
	q, phrases := turnCounters(fresh)
	require.Equal(t, 1, q)
	require.Equal(t, []string{"Сколько планируете?"}, phrases)
}
