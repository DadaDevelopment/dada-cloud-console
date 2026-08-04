package api

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/dada-tuda/console/backend/internal/config"
)

// newSessionTestHandler builds a handler wired to the real database with the
// memory feature on and a one-minute idle gap, so a test can move the clock by
// writing last_message_at instead of sleeping.
func newSessionTestHandler(t *testing.T) *Handler {
	t.Helper()
	return &Handler{
		pool: testAgentChatPool(t),
		cfg: &config.Config{
			AgentChatSessionIdleMinutes: 60,
			AgentChatHistoryMaxChars:    120000,
			AgentChatMemoryMaxChars:     1200,
		},
	}
}

func cleanupSessionUser(t *testing.T, h *Handler, userSub string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := h.pool.Exec(ctx, `DELETE FROM agent_chat_messages WHERE user_sub = $1`, userSub); err != nil {
			t.Logf("cleanup messages: %v", err)
		}
		if _, err := h.pool.Exec(ctx, `DELETE FROM agent_chat_sessions WHERE user_sub = $1`, userSub); err != nil {
			t.Logf("cleanup sessions: %v", err)
		}
		if _, err := h.pool.Exec(ctx, `DELETE FROM agent_chat_user_memory WHERE user_sub = $1`, userSub); err != nil {
			t.Logf("cleanup memory: %v", err)
		}
		if _, err := h.pool.Exec(ctx, `DELETE FROM agent_chat_context_resets WHERE user_sub = $1`, userSub); err != nil {
			t.Logf("cleanup resets: %v", err)
		}
	})
}

// TestAgentChatSessionID_BoundaryConditions is the whole session contract in
// one test: consecutive turns continue the same conversation, an idle gap ends
// it, a different project is a different conversation, and clearing the context
// ends it immediately. Every one of these is what decides whether the next turn
// sees the transcript or only the summary, so a regression here is invisible in
// the UI and catastrophic in the answers.
func TestAgentChatSessionID_BoundaryConditions(t *testing.T) {
	h := newSessionTestHandler(t)
	ctx := context.Background()
	userSub := "session-test-" + uuid.NewString()
	cleanupSessionUser(t, h, userSub)

	projectA := uuid.New()
	projectB := uuid.New()

	first := h.agentChatSessionID(ctx, userSub, &projectA, nil)
	if first == uuid.Nil {
		t.Fatal("the first turn must open a session")
	}
	if again := h.agentChatSessionID(ctx, userSub, &projectA, nil); again != first {
		t.Errorf("a consecutive turn must continue the same session, got %s then %s", first, again)
	}

	if other := h.agentChatSessionID(ctx, userSub, &projectB, nil); other == first {
		t.Error("another project is another conversation and must not share a session")
	}

	if _, err := h.pool.Exec(ctx,
		`UPDATE agent_chat_sessions SET last_message_at = now() - interval '2 hours' WHERE id = $1`, first,
	); err != nil {
		t.Fatalf("age the session: %v", err)
	}
	afterIdle := h.agentChatSessionID(ctx, userSub, &projectA, nil)
	if afterIdle == first {
		t.Error("a session idle past the gap must not be resumed")
	}

	h.agentChatEndSessions(ctx, userSub, &projectA, nil)
	afterClear := h.agentChatSessionID(ctx, userSub, &projectA, nil)
	if afterClear == afterIdle {
		t.Error("clearing the context must end the conversation regardless of the idle gap")
	}
}

// TestAgentChatOpenSessionID_NeverStartsOrExtendsOne guards the read-only half.
// The history endpoint runs on every page load; if it opened or touched a
// session, a user who only refreshed the panel would keep a dead conversation
// alive forever and it would never be folded into their memory.
func TestAgentChatOpenSessionID_NeverStartsOrExtendsOne(t *testing.T) {
	h := newSessionTestHandler(t)
	ctx := context.Background()
	userSub := "session-ro-" + uuid.NewString()
	cleanupSessionUser(t, h, userSub)
	projectID := uuid.New()

	if got := h.agentChatOpenSessionID(ctx, userSub, &projectID, nil); got != uuid.Nil {
		t.Errorf("with nothing said yet there is no open session, got %s", got)
	}
	var count int
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM agent_chat_sessions WHERE user_sub = $1`, userSub,
	).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Fatalf("a read must not create a session, found %d", count)
	}

	live := h.agentChatSessionID(ctx, userSub, &projectID, nil)
	if got := h.agentChatOpenSessionID(ctx, userSub, &projectID, nil); got != live {
		t.Errorf("the live session must be found, want %s got %s", live, got)
	}

	var before, after string
	if err := h.pool.QueryRow(ctx,
		`SELECT last_message_at::text FROM agent_chat_sessions WHERE id = $1`, live,
	).Scan(&before); err != nil {
		t.Fatalf("read last_message_at: %v", err)
	}
	h.agentChatOpenSessionID(ctx, userSub, &projectID, nil)
	if err := h.pool.QueryRow(ctx,
		`SELECT last_message_at::text FROM agent_chat_sessions WHERE id = $1`, live,
	).Scan(&after); err != nil {
		t.Fatalf("re-read last_message_at: %v", err)
	}
	if before != after {
		t.Errorf("a read must not extend the session: %s -> %s", before, after)
	}
}

// TestAgentChatSessionHistory_IsScopedToOneConversation is the reason sessions
// exist at all. The old behaviour was "the last N rows in this scope", which
// leaked last week's conversation into today's context; the new one must hand
// back exactly the turns of the session it was asked about, oldest first.
func TestAgentChatSessionHistory_IsScopedToOneConversation(t *testing.T) {
	h := newSessionTestHandler(t)
	ctx := context.Background()
	userSub := "session-hist-" + uuid.NewString()
	cleanupSessionUser(t, h, userSub)
	projectID := uuid.New()

	older := h.agentChatSessionID(ctx, userSub, &projectID, nil)
	insertSessionMessage(t, h, userSub, &projectID, older, "user", "what did I ask last week")

	if _, err := h.pool.Exec(ctx,
		`UPDATE agent_chat_sessions SET last_message_at = now() - interval '2 hours' WHERE id = $1`, older,
	); err != nil {
		t.Fatalf("age the session: %v", err)
	}

	current := h.agentChatSessionID(ctx, userSub, &projectID, nil)
	if current == older {
		t.Fatal("the idle gap should have started a new session")
	}
	insertSessionMessage(t, h, userSub, &projectID, current, "user", "deploy the shop")
	insertSessionMessage(t, h, userSub, &projectID, current, "assistant", "which environment")

	history := h.agentChatSessionHistory(ctx, current)
	if len(history) != 2 {
		t.Fatalf("expected exactly this conversation's two turns, got %d: %+v", len(history), history)
	}
	if history[0].Content != "deploy the shop" || history[1].Content != "which environment" {
		t.Errorf("history must be oldest-first and scoped to the session, got %+v", history)
	}
}

// TestAgentChatClaimMemory_OnlyOneFolderWinsPerUser proves the claim is a real
// mutual exclusion and not a read-then-write. Two replicas fold in parallel;
// because the summary is computed from the summary, a lost race silently
// discards a whole conversation's worth of memory.
func TestAgentChatClaimMemory_OnlyOneFolderWinsPerUser(t *testing.T) {
	h := newSessionTestHandler(t)
	ctx := context.Background()
	userSub := "session-claim-" + uuid.NewString()
	cleanupSessionUser(t, h, userSub)

	if !h.agentChatClaimMemory(ctx, userSub) {
		t.Fatal("the first folder must take the claim")
	}
	if h.agentChatClaimMemory(ctx, userSub) {
		t.Error("a second folder must not get the claim while the first holds it")
	}

	h.agentChatReleaseMemoryClaim(ctx, userSub)
	if !h.agentChatClaimMemory(ctx, userSub) {
		t.Error("a released claim must be takeable again")
	}

	if _, err := h.pool.Exec(ctx,
		`UPDATE agent_chat_user_memory SET folding_started_at = now() - interval '1 hour' WHERE user_sub = $1`, userSub,
	); err != nil {
		t.Fatalf("age the claim: %v", err)
	}
	if !h.agentChatClaimMemory(ctx, userSub) {
		t.Error("a claim abandoned by a dead pod must be taken over, not honoured forever")
	}
}

// TestAgentChatConfirmSessionID_RejoinsThePausedConversation covers the shape
// that is easiest to get wrong: a turn stops on a confirmation card, the user
// answers it later, and the resumed half must land in the same conversation.
// Re-deriving it from the pending row's scope would not do -- that scope is
// where the tool acts, which is legitimately not always where the conversation
// started -- and neither would the idle gap, which the card can easily outlast.
func TestAgentChatConfirmSessionID_RejoinsThePausedConversation(t *testing.T) {
	h := newSessionTestHandler(t)
	ctx := context.Background()
	userSub := "session-confirm-" + uuid.NewString()
	cleanupSessionUser(t, h, userSub)

	consoleProject := uuid.New()
	toolProject := uuid.New()

	paused := h.agentChatSessionID(ctx, userSub, &consoleProject, nil)
	row := &agentChatPendingRow{sessionID: paused, projectID: &toolProject}

	if _, err := h.pool.Exec(ctx,
		`UPDATE agent_chat_sessions SET last_message_at = now() - interval '2 hours' WHERE id = $1`, paused,
	); err != nil {
		t.Fatalf("age the paused session: %v", err)
	}
	if got := h.agentChatConfirmSessionID(ctx, userSub, row); got != paused {
		t.Errorf("a confirmed write must resume the paused session even past the idle gap, want %s got %s", paused, got)
	}

	if _, err := h.pool.Exec(ctx,
		`UPDATE agent_chat_sessions SET folded_at = now() WHERE id = $1`, paused,
	); err != nil {
		t.Fatalf("mark folded: %v", err)
	}
	if got := h.agentChatConfirmSessionID(ctx, userSub, row); got == paused {
		t.Error("a folded session must not be written into again; those messages would never reach the user's memory")
	}

	legacy := &agentChatPendingRow{projectID: &toolProject}
	if got := h.agentChatConfirmSessionID(ctx, userSub, legacy); got == uuid.Nil {
		t.Error("a pending row queued before sessions existed must still get one")
	}
}

func insertSessionMessage(t *testing.T, h *Handler, userSub string, projectID *uuid.UUID, sessionID uuid.UUID, role, content string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`INSERT INTO agent_chat_messages (user_sub, project_id, role, content, session_id)
		 VALUES ($1, $2, $3, $4, $5)`,
		userSub, projectID, role, content, sessionID,
	); err != nil {
		t.Fatalf("insert %s message: %v", role, err)
	}
}
