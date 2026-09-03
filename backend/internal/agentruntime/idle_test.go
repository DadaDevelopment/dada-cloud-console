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

func (f fakeA2AIdle) Send(ctx context.Context, agentName string, messages []Message) (string, error) {
	return f.reply, nil
}

func TestInvocationEnvelope(t *testing.T) {
	r := idleHookRow{IdleMinutes: 31, HookMessage: "Спроси про KYC."}
	got := invocationEnvelope(r)
	if !strings.Contains(got, "[invocation: cause=conversation_idle, idle=31m]") {
		t.Fatalf("envelope must carry cause and idle duration, got %q", got)
	}
	if !strings.Contains(got, "Спроси про KYC.") {
		t.Fatalf("envelope must carry the hook instruction, got %q", got)
	}

	def := invocationEnvelope(idleHookRow{IdleMinutes: 30})
	if !strings.Contains(def, "follow-up") {
		t.Fatalf("empty hook message must fall back to the default instruction, got %q", def)
	}
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
	sched := NewIdleScheduler(store.(*pgStoreAlias).pool, rt, fakeA2AIdle{reply: "возвращаюсь к вашему вопросу"}, outbound, time.Second)

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
		`UPDATE conversations SET metadata = jsonb_set(metadata, '{idle_fired_at}', '"2026-09-03T00:00:00Z"') WHERE id = $1`, conv.ID)
	require_NoError(t, err)

	if err := store.ClearIdleFlag(ctx, conv.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err := store.GetConversation(ctx, conv.ID)
	require_NoError(t, err)
	if _, has := got.Metadata["idle_fired_at"]; has {
		t.Fatalf("idle_fired_at must be gone, got %v", got.Metadata)
	}
}

func TestHooksAPI_CRUD(t *testing.T) {
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
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create hook: status %d body %s", rec.Code, rec.Body.String())
	}

	req, _ = http.NewRequest(http.MethodGet, "/hooks?agent_name="+agentName, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "follow-up-30m") {
		t.Fatalf("list hooks must include the created hook, got %d %s", rec.Code, rec.Body.String())
	}

	req, _ = http.NewRequest(http.MethodDelete, "/hooks/nonexistent-id", nil)
	rec = httptest.NewRecorder()
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
