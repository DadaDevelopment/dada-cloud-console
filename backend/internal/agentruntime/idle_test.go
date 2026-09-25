package agentruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeA2AIdle struct{ reply string }

func (f fakeA2AIdle) Send(ctx context.Context, run AgentRunRequest) (string, error) {
	return f.reply, nil
}

func TestInvocationEnvelope(t *testing.T) {
	r := idleHookRow{IdleMinutes: 31, HookMessage: "Спроси про KYC."}
	got := invocationEnvelope(r)
	if !strings.Contains(got, "[invocation: cause=conversation_idle, idle=31m, step=1]") {
		t.Fatalf("envelope must carry cause, idle duration and the 1-based ladder step, got %q", got)
	}
	if third := invocationEnvelope(idleHookRow{IdleMinutes: 1440, Step: 2}); !strings.Contains(third, "step=3]") {
		t.Fatalf("step in the envelope is the number of the follow-up being written, got %q", third)
	}
	if !strings.Contains(got, "Спроси про KYC.") {
		t.Fatalf("envelope must carry the hook instruction, got %q", got)
	}

	def := invocationEnvelope(idleHookRow{IdleMinutes: 30})
	if !strings.Contains(def, "follow-up") {
		t.Fatalf("empty hook message must fall back to the default instruction, got %q", def)
	}
}

// idleDaytime is a Moscow lunchtime: inside the quiet-hours gap and inside a
// send window, so every ladder step is allowed and the tests do not depend on
// the wall clock they run at.
func idleDaytime() time.Time {
	return time.Date(2026, 9, 21, 13, 15, 0, 0, idleSendLocation)
}

func TestIdleStepAllowedAt(t *testing.T) {
	at := func(hour int) time.Time { return time.Date(2026, 9, 21, hour, 30, 0, 0, idleSendLocation) }
	cases := []struct {
		step int
		hour int
		want bool
	}{
		{0, 10, true}, {0, 23, false}, {0, 3, false}, {0, 7, true},
		{1, 16, true}, {1, 0, false},
		{2, 16, false}, {2, 19, true}, {2, 7, true}, {2, 8, true}, {2, 13, true}, {2, 23, false},
		{5, 12, false}, {5, 13, true},
	}
	for _, c := range cases {
		if got := idleStepAllowedAt(c.step, at(c.hour)); got != c.want {
			t.Errorf("step %d at %02d:30 MSK: got %v want %v", c.step, c.hour, got, c.want)
		}
	}
	utc := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	if !idleStepAllowedAt(2, utc) {
		t.Fatalf("10:00 UTC is 13:00 Moscow, a send window")
	}
}

func TestIdleScheduler_LadderFiresEachStepOnceAndResetsOnInbound(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	pool := store.(*pgStoreAlias).pool

	agentName := "idle-ladder-" + uuid.NewString()[:8]
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-ladder", Actor{ExternalID: "u1"})
	require_NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
		_, _ = pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})
	_, err = pool.Exec(ctx, `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, 'follow-up', 'conversation.idle', '{"ladder_minutes":[20,40,120]}', 'schedule', '{"agent_message":"дожим"}')
	`, agentName)
	require_NoError(t, err)

	var envelopes []string
	a2a := fakeA2AIdleCapture{reply: "Получилось пройти регистрацию?", seen: &envelopes}
	var delivered int
	outbound := &fakeOutbound{onSend: func(agent, chat, text string) { delivered++ }}
	rt := NewRuntime(store, &noopHooks{}, a2a, nil)
	rt.contextKey = []byte(testRuntimeToken)
	sched := NewIdleScheduler(pool, rt, a2a, outbound, time.Second)
	sched.now = idleDaytime

	age := func(minutes int) {
		_, err := pool.Exec(ctx, `UPDATE conversations SET updated_at = NOW() - make_interval(mins => $2) WHERE id = $1`, conv.ID, minutes)
		require_NoError(t, err)
	}
	tick := func(label string) {
		if err := sched.Tick(ctx); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
	}

	age(10)
	tick("too early")
	if delivered != 0 {
		t.Fatalf("10 minutes of silence must not reach the 20-minute first step, got %d", delivered)
	}

	age(25)
	tick("step 1")
	tick("step 1 repeat")
	if delivered != 1 {
		t.Fatalf("first step fires once per silence, got %d", delivered)
	}
	if len(envelopes) != 1 || !strings.Contains(envelopes[0], "idle=20m, step=1]") {
		t.Fatalf("first envelope must name step 1 with its own wait, got %v", envelopes)
	}

	age(30)
	tick("step 2 too early")
	if delivered != 1 {
		t.Fatalf("second step waits 40 minutes after the first follow-up, got %d deliveries", delivered)
	}
	age(45)
	tick("step 2")
	if delivered != 2 || !strings.Contains(envelopes[1], "idle=40m, step=2]") {
		t.Fatalf("second step must fire after its own wait, got %d deliveries %v", delivered, envelopes)
	}

	age(3 * 24 * 60)
	tick("step 3 beyond first-step reach")
	if delivered != 3 || !strings.Contains(envelopes[2], "step=3]") {
		t.Fatalf("max_idle_minutes bounds the first step only, got %d deliveries %v", delivered, envelopes)
	}

	age(3 * 24 * 60)
	tick("ladder exhausted")
	if delivered != 3 {
		t.Fatalf("a three-step ladder must stop after three follow-ups, got %d", delivered)
	}

	if err := store.ClearIdleFlag(ctx, conv.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	age(25)
	tick("restart")
	if delivered != 4 || !strings.Contains(envelopes[3], "step=1]") {
		t.Fatalf("an inbound message restarts the ladder from step 1, got %d deliveries %v", delivered, envelopes)
	}
}

func TestIdleScheduler_HoldsStepsOutsideSendWindow(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	pool := store.(*pgStoreAlias).pool

	agentName := "idle-window-" + uuid.NewString()[:8]
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-window", Actor{ExternalID: "u1"})
	require_NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
		_, _ = pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})
	_, err = pool.Exec(ctx, `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, 'follow-up', 'conversation.idle', '{"ladder_minutes":[0]}', 'schedule', '{}')
	`, agentName)
	require_NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE conversations SET updated_at = NOW() - interval '5 minutes' WHERE id = $1`, conv.ID)
	require_NoError(t, err)

	var delivered int
	outbound := &fakeOutbound{onSend: func(agent, chat, text string) { delivered++ }}
	rt := NewRuntime(store, &noopHooks{}, fakeA2AIdle{reply: "Продолжим?"}, nil)
	rt.contextKey = []byte(testRuntimeToken)
	sched := NewIdleScheduler(pool, rt, fakeA2AIdle{reply: "Продолжим?"}, outbound, time.Second)

	sched.now = func() time.Time { return time.Date(2026, 9, 21, 2, 0, 0, 0, idleSendLocation) }
	if err := sched.Tick(ctx); err != nil {
		t.Fatalf("night tick: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("no follow-up goes out at night, got %d", delivered)
	}
	sched.now = idleDaytime
	if err := sched.Tick(ctx); err != nil {
		t.Fatalf("day tick: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("the held step fires on the first daytime tick, got %d", delivered)
	}
}

func TestIdleScheduler_SkipsGroupsWhenDirectOnlyAndPausedConversations(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	pool := store.(*pgStoreAlias).pool

	agentName := "idle-direct-" + uuid.NewString()[:8]
	direct, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "1482108441", Actor{ExternalID: "1482108441"})
	require_NoError(t, err)
	group, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "-90010001351", Actor{ExternalID: "90010001351"})
	require_NoError(t, err)
	paused, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "5228790663", Actor{ExternalID: "5228790663"})
	require_NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
		_, _ = pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})
	for _, id := range []uuid.UUID{direct.ID, group.ID, paused.ID} {
		_, err = pool.Exec(ctx, `UPDATE conversations SET updated_at = NOW() - interval '30 minutes' WHERE id = $1`, id)
		require_NoError(t, err)
	}
	rt := NewRuntime(store, &noopHooks{}, fakeA2AIdle{reply: "Продолжим?"}, nil)
	rt.contextKey = []byte(testRuntimeToken)
	if _, err := rt.states.PauseAgent(ctx, paused.ID, "operator"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, 'follow-up', 'conversation.idle', '{"ladder_minutes":[20],"direct_only":true}', 'schedule', '{}')
	`, agentName)
	require_NoError(t, err)

	var chats []string
	outbound := &fakeOutbound{onSend: func(agent, chat, text string) { chats = append(chats, chat) }}
	sched := NewIdleScheduler(pool, rt, fakeA2AIdle{reply: "Продолжим?"}, outbound, time.Second)
	sched.now = idleDaytime
	if err := sched.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(chats) != 1 || chats[0] != "1482108441" {
		t.Fatalf("direct_only must reach the private chat only, never the group or the paused conversation, got %v", chats)
	}
	var groupStep any
	require_NoError(t, pool.QueryRow(ctx, `SELECT metadata->>'idle_step' FROM conversations WHERE id = $1`, group.ID).Scan(&groupStep))
	if groupStep != nil {
		t.Fatalf("a skipped group chat must not be claimed, got idle_step %v", groupStep)
	}
}

type fakeA2AIdleCapture struct {
	reply string
	seen  *[]string
}

func (f fakeA2AIdleCapture) Send(ctx context.Context, run AgentRunRequest) (string, error) {
	for i := len(run.Messages) - 1; i >= 0; i-- {
		if run.Messages[i].Role == "system" {
			*f.seen = append(*f.seen, run.Messages[i].Content)
			break
		}
	}
	return f.reply, nil
}

func TestIdleScheduler_InvokesOncePerIdlePeriod(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	agentName := "idle-agent-" + uuid.NewString()[:8]
	actor := Actor{ExternalID: "u1", Username: "tester"}
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-idle-e2e", actor)
	require_NoError(t, err)

	t.Cleanup(func() {
		_, _ = store.(*pgStoreAlias).pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
		_, _ = store.(*pgStoreAlias).pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})

	_, err = store.(*pgStoreAlias).pool.Exec(ctx, `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, 'follow-up', 'conversation.idle', '{"idle_minutes":0}', 'schedule', '{"agent_message":"вернись к вопросу"}')
	`, agentName)
	require_NoError(t, err)

	var delivered []string
	outbound := &fakeOutbound{onSend: func(agent, chat, text string) {
		delivered = append(delivered, text)
	}}

	rt := NewRuntime(store, &noopHooks{}, fakeA2AIdle{reply: "возвращаюсь к вашему вопросу"}, nil)
	rt.contextKey = []byte(testRuntimeToken)
	sched := NewIdleScheduler(store.(*pgStoreAlias).pool, rt, fakeA2AIdle{reply: "возвращаюсь к вашему вопросу"}, outbound, time.Second)
	sched.now = idleDaytime

	if err := sched.Tick(ctx); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if len(delivered) != 1 {
		t.Fatalf("first tick must fire the follow-up, got %d deliveries", len(delivered))
	}
	if !strings.Contains(delivered[0], "возвращаюсь") {
		t.Fatalf("delivery must carry the agent reply, got %q", delivered[0])
	}

	if err := sched.Tick(ctx); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if len(delivered) != 1 {
		t.Fatalf("second tick must not double-fire (idle_fired_at claim), got %d", len(delivered))
	}

	if err := store.ClearIdleFlag(ctx, conv.ID); err != nil {
		t.Fatalf("clear idle flag: %v", err)
	}
	if err := sched.Tick(ctx); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if len(delivered) != 2 {
		t.Fatalf("after ClearIdleFlag the hook must fire again, got %d", len(delivered))
	}
}

func TestIdleScheduler_SkipsConversationsQuietLongerThanReach(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	pool := store.(*pgStoreAlias).pool

	agentName := "idle-reach-" + uuid.NewString()[:8]
	stale, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-stale", Actor{ExternalID: "u1"})
	require_NoError(t, err)
	fresh, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-fresh", Actor{ExternalID: "u2"})
	require_NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
		_, _ = pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})
	_, err = pool.Exec(ctx, `UPDATE conversations SET updated_at = NOW() - interval '3 days' WHERE id = $1`, stale.ID)
	require_NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE conversations SET updated_at = NOW() - interval '3 hours' WHERE id = $1`, fresh.ID)
	require_NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, 'follow-up', 'conversation.idle', '{"idle_minutes":120}', 'schedule', '{}')
	`, agentName)
	require_NoError(t, err)

	var chats []string
	outbound := &fakeOutbound{onSend: func(agent, chat, text string) { chats = append(chats, chat) }}
	rt := NewRuntime(store, &noopHooks{}, fakeA2AIdle{reply: "Получилось зарегистрироваться?"}, nil)
	rt.contextKey = []byte(testRuntimeToken)
	sched := NewIdleScheduler(pool, rt, fakeA2AIdle{reply: "Получилось зарегистрироваться?"}, outbound, time.Second)
	sched.now = idleDaytime
	if err := sched.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(chats) != 1 || chats[0] != "chat-fresh" {
		t.Fatalf("only the conversation inside the 24h reach may get a follow-up, got %v", chats)
	}

	_, err = pool.Exec(ctx, `UPDATE lifecycle_hooks SET trigger_config = '{"idle_minutes":120,"max_idle_minutes":10080}' WHERE agent_name = $1`, agentName)
	require_NoError(t, err)
	if err := sched.Tick(ctx); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if len(chats) != 2 || chats[1] != "chat-stale" {
		t.Fatalf("a wider max_idle_minutes must reach the older conversation, got %v", chats)
	}
}

func TestClearIdleFlag(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	agentName := "idle-agent-" + uuid.NewString()[:8]
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-cif", Actor{ExternalID: "u"})
	require_NoError(t, err)
	t.Cleanup(func() {
		_, _ = store.(*pgStoreAlias).pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
	})

	_, err = store.(*pgStoreAlias).pool.Exec(ctx,
		`UPDATE conversations SET metadata = metadata || '{"idle_fired_at":"2026-09-03T00:00:00Z","idle_step":3}' WHERE id = $1`, conv.ID)
	require_NoError(t, err)

	if err := store.ClearIdleFlag(ctx, conv.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err := store.GetConversation(ctx, conv.ID)
	require_NoError(t, err)
	if _, has := got.Metadata["idle_fired_at"]; has {
		t.Fatalf("idle_fired_at must be gone, got %v", got.Metadata)
	}
	if _, has := got.Metadata["idle_step"]; has {
		t.Fatalf("idle_step must be gone so the ladder restarts, got %v", got.Metadata)
	}
}

func TestHooksAPI_CRUD(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_TOKEN", testRuntimeToken)
	store := setupTestStore(t)
	ctx := context.Background()
	agentName := "hooks-api-" + uuid.NewString()[:8]

	pool := store.(*pgStoreAlias).pool
	srv := NewServer(pool, "/tmp/gitops")
	handler := srv.Handler()

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})

	req, _ := http.NewRequest(http.MethodPost, "/hooks", strings.NewReader(`{
		"agent_name": "`+agentName+`",
		"name": "follow-up-30m",
		"trigger_event": "conversation.idle",
		"trigger_config": {"idle_minutes": 30},
		"action_type": "schedule",
		"action_config": {"agent_message": "вернись к вопросу"}
	}`))
	rec := httptest.NewRecorder()
	req.Header.Set("Authorization", "Bearer "+testRuntimeToken)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create hook: status %d body %s", rec.Code, rec.Body.String())
	}

	req, _ = http.NewRequest(http.MethodGet, "/hooks?agent_name="+agentName, nil)
	rec = httptest.NewRecorder()
	req.Header.Set("Authorization", "Bearer "+testRuntimeToken)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "follow-up-30m") {
		t.Fatalf("list hooks must include the created hook, got %d %s", rec.Code, rec.Body.String())
	}

	req, _ = http.NewRequest(http.MethodDelete, "/hooks/nonexistent-id", nil)
	rec = httptest.NewRecorder()
	req.Header.Set("Authorization", "Bearer "+testRuntimeToken)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete of unknown hook must 404, got %d", rec.Code)
	}
}

type fakeOutbound struct {
	onSend func(agent, chat, text string)
}

func (f *fakeOutbound) SendOutbound(ctx context.Context, agentName, chatExternalID, text, replyTo string) error {
	f.onSend(agentName, chatExternalID, text)
	return nil
}

type noopHooks struct{}

func (n *noopHooks) Execute(ctx context.Context, event string, conv Conversation, extra any) error {
	return nil
}
func (n *noopHooks) ListIdleHooks(ctx context.Context) ([]Hook, error) {
	return nil, nil
}

type pgStoreAlias = pgStore

func require_NoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIdleEnabled pins the rollback switch from plan 4.4: the tick alone keeps
// deciding while AGENT_RUNTIME_IDLE_ENABLED is unset (today's behaviour), and
// a tick of 0 really means "off" rather than "60s".
func TestIdleEnabled(t *testing.T) {
	cases := []struct {
		name string
		env  string
		tick int
		want bool
	}{
		{"unset tick 60 stays on", "", 60, true},
		{"unset tick 0 is off", "", 0, false},
		{"unset negative tick is off", "", -5, false},
		{"explicit 0 wins over a positive tick", "0", 60, false},
		{"explicit false wins over a positive tick", "false", 60, false},
		{"explicit off wins over a positive tick", "OFF", 60, false},
		{"explicit 1 wins over a zero tick", "1", 0, true},
		{"explicit true wins over a zero tick", " True ", 0, true},
		{"unknown value falls back to the tick", "maybe", 0, false},
		{"unknown value falls back to the tick, on", "maybe", 30, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IdleEnabled(tc.env, tc.tick); got != tc.want {
				t.Fatalf("IdleEnabled(%q, %d) = %v, want %v", tc.env, tc.tick, got, tc.want)
			}
		})
	}
}

// TestNewIdleSchedulerClampsInterval documents why the clamp stays: a
// time.Ticker panics on a non-positive interval, so a scheduler constructed
// despite the gate must still be safe.
func TestNewIdleSchedulerClampsInterval(t *testing.T) {
	s := NewIdleScheduler(nil, nil, nil, nil, 0)
	if s.interval != idleScanIntervalDefault {
		t.Fatalf("interval = %v, want %v", s.interval, idleScanIntervalDefault)
	}
}

type fakeA2ARegisterRetry struct {
	replies []string
	errors  *[]string
	calls   *int
}

func (f fakeA2ARegisterRetry) Send(ctx context.Context, run AgentRunRequest) (string, error) {
	*f.errors = append(*f.errors, run.ConversationContext.ReplyError)
	i := *f.calls
	*f.calls++
	if i >= len(f.replies) {
		i = len(f.replies) - 1
	}
	return f.replies[i], nil
}

func TestIdleScheduler_RewritesFollowUpInClientRegister(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	pool := store.(*pgStoreAlias).pool

	agentName := "idle-register-" + uuid.NewString()[:8]
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-register", Actor{ExternalID: "u1"})
	require_NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
		_, _ = pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})
	_, err = pool.Exec(ctx, `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, 'follow-up', 'conversation.idle', '{"idle_minutes":0}', 'schedule', '{"agent_message":"дожим"}')
	`, agentName)
	require_NoError(t, err)
	_, err = store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "привет, ты тут?"})
	require_NoError(t, err)

	var errors []string
	var calls int
	a2a := fakeA2ARegisterRetry{errors: &errors, calls: &calls,
		replies: []string{"Ты на связи? Давайте продолжим, я всё объясню", "Ты на связи? Давай продолжим, всё объясню"}}
	var delivered []string
	outbound := &fakeOutbound{onSend: func(agent, chat, text string) { delivered = append(delivered, text) }}
	rt := NewRuntime(store, &noopHooks{}, a2a, nil)
	rt.contextKey = []byte(testRuntimeToken)
	sched := NewIdleScheduler(pool, rt, a2a, outbound, time.Second)
	sched.now = idleDaytime

	if err := sched.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if calls != 2 || errors[0] != "" || !strings.Contains(errors[1], "на «ты»") {
		t.Fatalf("a «давайте» follow-up to a «ты» client must be sent back once with the register hint, got calls=%d errors=%q", calls, errors)
	}
	if len(delivered) != 1 || strings.Contains(delivered[0], "Давайте") {
		t.Fatalf("the rewritten follow-up is what reaches the client, got %q", delivered)
	}
}

func TestIdleScheduler_InvokeNowClaimsStepWithoutDelivery(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	pool := store.(*pgStoreAlias).pool

	agentName := "idle-now-" + uuid.NewString()[:8]
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "-900123", Actor{ExternalID: "900123"})
	require_NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
		_, _ = pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})
	_, err = pool.Exec(ctx, `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, 'follow-up', 'conversation.idle', '{"ladder_minutes":[20,40],"direct_only":true}', 'schedule', '{"agent_message":"дожим"}')
	`, agentName)
	require_NoError(t, err)

	var envelopes []string
	a2a := fakeA2AIdleCapture{reply: "Получилось пройти регистрацию?", seen: &envelopes}
	var delivered int
	rt := NewRuntime(store, &noopHooks{}, a2a, nil)
	rt.contextKey = []byte(testRuntimeToken)
	sched := NewIdleScheduler(pool, rt, a2a, &fakeOutbound{onSend: func(_, _, _ string) { delivered++ }}, time.Second)

	for step, want := range []string{"idle=20m, step=1]", "idle=40m, step=2]"} {
		reply, err := sched.InvokeNow(ctx, conv)
		require_NoError(t, err)
		if reply == "" || !strings.Contains(envelopes[step], want) {
			t.Fatalf("step %d: reply %q envelopes %v", step+1, reply, envelopes)
		}
	}
	reply, err := sched.InvokeNow(ctx, conv)
	require_NoError(t, err)
	if reply != "" || len(envelopes) != 2 || delivered != 0 {
		t.Fatalf("exhausted ladder must stay silent and nothing reaches the channel: reply %q envelopes %d delivered %d", reply, len(envelopes), delivered)
	}
}

func idleLeakFixture(t *testing.T, replies []string) (sent []string, errs []string, calls int) {
	t.Helper()
	store := setupTestStore(t)
	ctx := context.Background()
	pool := store.(*pgStoreAlias).pool
	agentName := "idle-leak-" + uuid.NewString()[:8]
	conv, _, err := store.GetOrCreateConversation(ctx, agentName, "telegram", "chat-leak", Actor{ExternalID: "u1"})
	require_NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM conversations WHERE agent_name = $1`, agentName)
		_, _ = pool.Exec(ctx, `DELETE FROM lifecycle_hooks WHERE agent_name = $1`, agentName)
	})
	_, err = pool.Exec(ctx, `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, 'follow-up', 'conversation.idle', '{"idle_minutes":0}', 'schedule', '{"agent_message":"дожим"}')
	`, agentName)
	require_NoError(t, err)
	_, err = store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: "привет, ты тут?"})
	require_NoError(t, err)
	a2a := fakeA2ARegisterRetry{errors: &errs, calls: &calls, replies: replies}
	outbound := &fakeOutbound{onSend: func(agent, chat, text string) { sent = append(sent, text) }}
	rt := NewRuntime(store, &noopHooks{}, a2a, nil)
	rt.contextKey = []byte(testRuntimeToken)
	sched := NewIdleScheduler(pool, rt, a2a, outbound, time.Second)
	sched.now = idleDaytime
	if err := sched.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	return sent, errs, calls
}

func TestIdleScheduler_RewritesLeakedFollowUpOnce(t *testing.T) {
	sent, errs, calls := idleLeakFixture(t, []string{"Ступень S5 → S6: Ты на связи?", "Ты на связи? Давай продолжим"})
	if calls != 2 || errs[0] != "" || errs[1] == "" {
		t.Fatalf("a leaked follow-up must be sent back once with the repair hint, got calls=%d errors=%q", calls, errs)
	}
	if len(sent) != 1 || strings.Contains(sent[0], "S5") {
		t.Fatalf("only the rewritten follow-up reaches the client, got %q", sent)
	}
}

func TestIdleScheduler_DropsFollowUpThatLeaksTwiceOrIsBlank(t *testing.T) {
	sent, _, calls := idleLeakFixture(t, []string{"S5 → S6", "S6 → S7"})
	if calls != 2 || len(sent) != 0 {
		t.Fatalf("a follow-up that leaks after the rewrite must not be sent, got calls=%d sent=%q", calls, sent)
	}
	sent, _, calls = idleLeakFixture(t, []string{" \n "})
	if calls != 1 || len(sent) != 0 {
		t.Fatalf("a blank follow-up must not be sent, got calls=%d sent=%q", calls, sent)
	}
}
