package agentruntime

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestParseNarrowTopics_DefaultListCoversThePlansFourTopics(t *testing.T) {
	topics := ParseNarrowTopics("")
	allowed := []string{
		"а какая комиссия за сделку?",
		"когда можно будет вывести деньги",
		"MT5 не ставится на телефон",
		"что нужно для верификации",
		"какие документы для KYC",
	}
	for _, text := range allowed {
		require.Truef(t, narrowTopicAllowed(text, topics), "%q must stay answerable in narrow mode", text)
	}
	blocked := []string{
		"а Роман точно вернёт деньги если что",
		"я скинул 1000, когда группа?",
		"вы вообще кто такие",
		"",
		"   ",
	}
	for _, text := range blocked {
		require.Falsef(t, narrowTopicAllowed(text, topics), "%q must be left to the curator", text)
	}
}

func TestParseNarrowTopics_EnvOverrideReplacesTheListAndSurvivesABadPattern(t *testing.T) {
	topics := ParseNarrowTopics(`(?i)только это, (?i)[unclosed`)
	require.Len(t, topics, 1)
	require.True(t, narrowTopicAllowed("ТОЛЬКО ЭТО и ничего больше", topics))
	require.False(t, narrowTopicAllowed("комиссия?", topics), "env override replaces the built-in list, it does not extend it")
}

func TestNarrowSince_ReadsTheMetadataMarkAndIgnoresGarbage(t *testing.T) {
	_, ok := narrowSince(Conversation{})
	require.False(t, ok)

	_, ok = narrowSince(Conversation{Metadata: map[string]any{narrowModeKey: "не дата"}})
	require.False(t, ok, "an unreadable mark must not mute a chat forever")

	since, ok := narrowSince(Conversation{Metadata: map[string]any{narrowModeKey: "2026-09-16T10:00:00Z"}})
	require.True(t, ok)
	require.Equal(t, time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC), since.UTC())
}

func TestNarrowExpired_ZeroReturnHoursNeverLifts(t *testing.T) {
	since := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	far := since.Add(1000 * time.Hour)
	require.False(t, narrowExpired(since, far, 0))
	require.False(t, narrowExpired(since, since.Add(2*time.Hour), 3*time.Hour))
	require.True(t, narrowExpired(since, since.Add(3*time.Hour), 3*time.Hour))
}

func TestRuntimeFlags_NarrowDefaultsAreTodaysBehaviour(t *testing.T) {
	f := runtimeFlagsFromEnv()
	require.False(t, f.NarrowEscalation)
	require.False(t, f.SeamlessHandoff)
	require.Zero(t, f.NarrowReturnAfter)
	require.NotEmpty(t, f.NarrowTopics)
}

// narrowStore is the minimum ConversationStore the gate touches, so the gate
// itself is testable without a database.
type narrowStore struct {
	ConversationStore
	cleared  int
	handled  int
	touched  int
	messages []Message
}

func (s *narrowStore) ClearNarrowMode(context.Context, uuid.UUID) error { s.cleared++; return nil }
func (s *narrowStore) Touch(context.Context, uuid.UUID) error           { s.touched++; return nil }
func (s *narrowStore) MarkRuntimeHandled(_ context.Context, m []Message) error {
	s.handled++
	s.messages = append(s.messages, m...)
	return nil
}

func narrowRuntime(t *testing.T, store ConversationStore, returnAfter time.Duration) (*Runtime, *[]string) {
	t.Helper()
	var cards []string
	rt := &Runtime{store: store, flags: runtimeFlags{NarrowEscalation: true, NarrowTopics: ParseNarrowTopics(""), NarrowReturnAfter: returnAfter}}
	rt.notifyOperator = func(_ context.Context, _ Conversation, text string) error {
		cards = append(cards, text)
		return nil
	}
	return rt, &cards
}

func narrowConv(since string) Conversation {
	conv := Conversation{ID: uuid.New(), AgentName: "a", Channel: "telegram", ExternalID: "1"}
	if since != "" {
		conv.Metadata = map[string]any{narrowModeKey: since}
	}
	return conv
}

func TestNarrowGate_NotInNarrowModePassesThrough(t *testing.T) {
	store := &narrowStore{}
	rt, cards := narrowRuntime(t, store, 0)
	resp, handled, err := rt.narrowGate(context.Background(), narrowConv(""), RuntimeState{}, []Message{{Content: "привет"}})
	require.NoError(t, err)
	require.False(t, handled)
	require.False(t, resp.Suppressed)
	require.Empty(t, *cards)
}

func TestNarrowGate_WhiteListedQuestionIsAnswered(t *testing.T) {
	store := &narrowStore{}
	rt, cards := narrowRuntime(t, store, 0)
	_, handled, err := rt.narrowGate(context.Background(), narrowConv("2026-09-16T10:00:00Z"), RuntimeState{}, []Message{{Content: "какая комиссия за вход?"}})
	require.NoError(t, err)
	require.False(t, handled, "a white-listed question still reaches the model")
	require.Empty(t, *cards)
	require.Zero(t, store.handled)
}

func TestNarrowGate_OffTopicIsSilentAndRaisesTheOperatorCard(t *testing.T) {
	store := &narrowStore{}
	rt, cards := narrowRuntime(t, store, 0)
	conv := narrowConv("2026-09-16T10:00:00Z")
	pending := []Message{{Content: "а когда Роман мне напишет?"}}

	resp, handled, err := rt.narrowGate(context.Background(), conv, RuntimeState{}, pending)

	require.NoError(t, err)
	require.True(t, handled)
	require.True(t, resp.Suppressed)
	require.Empty(t, resp.Text)
	require.Len(t, *cards, 1)
	require.Contains(t, (*cards)[0], "а когда Роман мне напишет?")
	require.Contains(t, (*cards)[0], narrowCardFooter)
	require.Equal(t, 1, store.handled, "a silent turn must still consume its pending inbox")
	require.Equal(t, 1, store.touched)
	require.Zero(t, store.cleared)
}

func TestNarrowGate_ExpiredModeIsClearedAndTheTurnProceeds(t *testing.T) {
	store := &narrowStore{}
	rt, cards := narrowRuntime(t, store, time.Hour)
	conv := narrowConv(time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339))

	_, handled, err := rt.narrowGate(context.Background(), conv, RuntimeState{}, []Message{{Content: "а когда Роман мне напишет?"}})

	require.NoError(t, err)
	require.False(t, handled)
	require.Equal(t, 1, store.cleared)
	require.Empty(t, *cards)
}

func TestNarrowGate_UnexpiredModeWithReturnHoursStaysSilent(t *testing.T) {
	store := &narrowStore{}
	rt, cards := narrowRuntime(t, store, 3*time.Hour)
	conv := narrowConv(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))

	_, handled, err := rt.narrowGate(context.Background(), conv, RuntimeState{}, []Message{{Content: "ну что там"}})

	require.NoError(t, err)
	require.True(t, handled)
	require.Zero(t, store.cleared)
	require.Len(t, *cards, 1)
}

// pendingStore is a narrowStore that also holds an inbox, so the drain can be
// tested without a database.
type pendingStore struct {
	narrowStore
	pending []Message
	err     error
}

func (s *pendingStore) PendingRuntimeMessages(context.Context, uuid.UUID) ([]Message, error) {
	return s.pending, s.err
}

func TestMarkPendingHandled_DrainsTheInboxOnce(t *testing.T) {
	store := &pendingStore{pending: []Message{{Content: "готово"}, {Content: "и ещё"}}}
	rt := &Runtime{store: store}

	require.NoError(t, rt.markPendingHandled(context.Background(), uuid.New()))
	require.Equal(t, 1, store.handled)
	require.Len(t, store.messages, 2)

	store.pending = nil
	require.NoError(t, rt.markPendingHandled(context.Background(), uuid.New()))
	require.Equal(t, 1, store.handled, "an empty inbox must not write a receipt")
}

func TestMarkPendingHandled_WithoutInboxStorageIsAnError(t *testing.T) {
	rt := &Runtime{store: &narrowStore{}}
	require.Error(t, rt.markPendingHandled(context.Background(), uuid.New()))
}

// Plan 4.1 plus review H2: the hand-off answers the turn itself, so the input
// must not stay pending for AGENT_RUNTIME_SILENCE_RECOVERY to replay, and a
// second escalate inside the dedup window must not tell the customer twice.
func TestPGNarrowHandoff_SecondEscalateDoesNotRepeatTheClientLine(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_NARROW_ESCALATION", "1")
	store := pauseRetryTestStore(t)
	ctx := context.Background()
	agent := "narrow-test-" + uuid.NewString()
	client, _, err := store.GetOrCreateConversation(ctx, agent, "telegram", "3001", Actor{ExternalID: "3001", Username: "client"})
	require.NoError(t, err)
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	srv := NewServer(store.pool, t.TempDir())
	srv.pauseCRM = pauseFunc(func(context.Context, Conversation, string) error { return nil })
	out := &recordingOutbound{}
	srv.outbound = out
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	token, err := issueContextToken([]byte(testRuntimeToken), client, time.Now().Add(time.Minute))
	require.NoError(t, err)

	body := map[string]any{
		"context_token":  token,
		"reason_code":    "E_DEPOSIT_HANDOFF",
		"summary":        "Пополнил 500, реквизиты дал сам.",
		"client_message": "Принял, дальше веду по шагам",
	}
	status, result := postRuntime(t, httpServer.URL, "/tools/escalate", body, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, "narrow", result["mode"])
	require.Equal(t, true, result["agent_enabled"], "narrow mode must not pause the agent")
	require.Equal(t, true, result["client_notified"])

	status, result = postRuntime(t, httpServer.URL, "/tools/escalate", body, testRuntimeToken)
	require.Equal(t, 200, status, result)
	require.Equal(t, true, result["already_signalled"])
	require.Equal(t, false, result["client_notified"])
	require.Len(t, out.texts, 1, "the customer hears about the hand-off exactly once")

	state, err := store.GetState(ctx, client.ID)
	require.NoError(t, err)
	require.True(t, state.AgentEnabled)
	require.Empty(t, state.PauseReason)

	conv, err := store.GetConversation(ctx, client.ID)
	require.NoError(t, err)
	_, inNarrow := narrowSince(conv)
	require.True(t, inNarrow)
}
