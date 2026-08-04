package api

import (
	"context"
	"log"
	"strings"

	"github.com/dada-tuda/console/backend/internal/agentchat"
	"github.com/google/uuid"
)

// chatMessage is one transcript row as the chat code cares about it: who said
// it, what they said, and which tool produced it when the role is "tool".
type chatMessage struct {
	Role     string
	Content  string
	ToolName string
}

// chatStore is where a conversation lives.
//
// The interface exists so the transcript can move off Postgres without the chat
// code knowing. Everything above it works in whole conversations identified by
// a session id; nothing above it writes SQL.
//
// AppendMessage archives one message and never fails the caller: losing a
// transcript row must not fail the turn the user is waiting on.
//
// SessionMessages returns one conversation oldest-first. A limit above zero
// keeps the NEWEST limit messages and still returns them oldest-first, which is
// what the panel wants after a reload; zero or less returns the whole
// conversation, which is what the model and the memory folder want -- they
// bound themselves by characters instead. Unlike AppendMessage it does report
// failure, because a store that cannot be read is not the same thing as a
// conversation with nothing in it and the caller is the one that knows whether
// that difference is worth a 500.
//
// DailyUserMessageCount counts today's user messages for the daily cap,
// including messages in conversations the user has since cleared: clearing
// hides a conversation, it does not refund the quota.
//
// Deliberately not in here: sessions, the per-user memory summary and the
// pending confirmation queue. Those need a conditional UPDATE whose row count
// is the answer (see agentChatSessionID, agentChatClaimMemory,
// agentChatConsumePendingAction) -- they are locks, not a transcript, and an
// eventually consistent store cannot hold them without losing the mutual
// exclusion they exist for.
type chatStore interface {
	AppendMessage(ctx context.Context, userSub, orgID string, projectID, envID *uuid.UUID, role, content string, toolName *string)
	SessionMessages(ctx context.Context, sessionID uuid.UUID, limit int) ([]chatMessage, error)
	DailyUserMessageCount(ctx context.Context, userSub string) (int64, error)
}

// newChatStore builds the store AGENT_CHAT_STORE asks for. An unrecognised
// value is refused loudly rather than silently treated as the default: getting
// this wrong means the transcript is being written somewhere nobody reads, and
// that is not a thing to discover from a user complaining the assistant forgot
// the conversation.
func newChatStore(h *Handler) chatStore {
	switch name := strings.ToLower(strings.TrimSpace(h.cfg.AgentChatStore)); name {
	case "", "postgres":
		return pgChatStore{h}
	case "langfuse":
		store := langfuseChatStore{h}
		if !store.client().Configured() {
			log.Printf("agent-chat: AGENT_CHAT_STORE=langfuse but no langfuse keys are configured, falling back to postgres")
			return pgChatStore{h}
		}
		log.Printf("agent-chat: transcript stored in langfuse")
		return store
	default:
		log.Printf("agent-chat: unknown AGENT_CHAT_STORE=%q, using postgres", name)
		return pgChatStore{h}
	}
}

// pgChatStore keeps the transcript in agent_chat_messages, in the same database
// as the sessions that index it.
type pgChatStore struct {
	h *Handler
}

func (s pgChatStore) AppendMessage(ctx context.Context, userSub, orgID string, projectID, envID *uuid.UUID, role, content string, toolName *string) {
	h := s.h
	if h.pool == nil {
		return
	}
	var orgArg, toolArg, traceArg, sessionArg any
	if orgID != "" {
		orgArg = orgID
	}
	if toolName != nil {
		toolArg = *toolName
	}
	if traceID := agentchat.TraceIDFrom(ctx); traceID != "" {
		traceArg = traceID
	}
	if sessionID := agentchat.SessionIDFrom(ctx); sessionID != uuid.Nil {
		sessionArg = sessionID
	}
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO agent_chat_messages (user_sub, org_id, project_id, env_id, role, content, tool_name, trace_id, session_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		userSub, orgArg, projectID, envID, role, content, toolArg, traceArg, sessionArg,
	); err != nil {
		log.Printf("agent-chat: failed to persist %s message: %v", role, err)
	}
}

func (s pgChatStore) SessionMessages(ctx context.Context, sessionID uuid.UUID, limit int) ([]chatMessage, error) {
	h := s.h
	if h.pool == nil || sessionID == uuid.Nil {
		return nil, nil
	}

	query := `SELECT role, content, tool_name FROM agent_chat_messages
	          WHERE session_id = $1 AND role IN ('user', 'assistant', 'tool')
	          ORDER BY created_at ASC`
	args := []any{sessionID}
	if limit > 0 {
		query = `SELECT role, content, tool_name FROM (
		           SELECT role, content, tool_name, created_at FROM agent_chat_messages
		           WHERE session_id = $1 AND role IN ('user', 'assistant', 'tool')
		           ORDER BY created_at DESC
		           LIMIT $2
		         ) recent ORDER BY created_at ASC`
		args = append(args, limit)
	}

	rows, err := h.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []chatMessage
	for rows.Next() {
		var m chatMessage
		var toolName *string
		if err := rows.Scan(&m.Role, &m.Content, &toolName); err != nil {
			continue
		}
		if toolName != nil {
			m.ToolName = *toolName
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s pgChatStore) DailyUserMessageCount(ctx context.Context, userSub string) (int64, error) {
	var count int64
	err := s.h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_chat_messages
		 WHERE user_sub = $1 AND role = 'user' AND created_at >= date_trunc('day', now())`,
		userSub,
	).Scan(&count)
	return count, err
}
