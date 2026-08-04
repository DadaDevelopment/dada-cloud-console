package api

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/agentchat"
	"github.com/dada-tuda/console/backend/internal/langfuse"
	"github.com/google/uuid"
)

// agentChatMessageTracePrefix is what makes a transcript trace recognisable
// among the turn traces sharing the same Langfuse project. The role is part of
// the trace name rather than of the metadata on purpose: names are a first-class
// query filter, so the daily cap becomes "count traces named chat-message-user
// for this user since midnight", which costs one request that transfers no
// message bodies at all.
const agentChatMessageTracePrefix = "chat-message-"

// agentChatStoreReadTimeout bounds a transcript read. The chat endpoint has a
// 300ms budget for everything it does besides the model call, so a store that
// has gone slow must give up rather than drag the turn down with it.
const agentChatStoreReadTimeout = 3 * time.Second

// agentChatStoreMaxMessages is how deep an unbounded read goes. Postgres can
// return a whole conversation in one query, a paged HTTP API cannot, so an
// unlimited read here would mean an unbounded number of round trips. Callers
// that pass no limit bound themselves by characters anyway; this only decides
// how much is fetched before that trimming happens, and hitting it is logged
// rather than passed off as a complete conversation.
const agentChatStoreMaxMessages = 500

// langfuseChatStore keeps the transcript in Langfuse: one trace per message,
// carrying the conversation's session id and the user.
//
// Read-after-write is the sharp edge and it is not hidden here. Langfuse
// ingestion is fire-and-forget and its read API is eventually consistent, so a
// message written at the end of one turn is not guaranteed to be readable at
// the start of the next one seconds later. For the transcript that means an
// occasionally forgotten exchange; for the daily cap it means a ceiling with
// slack rather than an exact gate. Postgres has neither problem, which is why
// AGENT_CHAT_STORE defaults to it, and why sessions, the memory summary and the
// confirmation queue are not part of the chatStore interface at all.
type langfuseChatStore struct {
	h *Handler
}

func (s langfuseChatStore) client() *langfuse.Client {
	return s.h.agentChatLangfuse()
}

func (s langfuseChatStore) AppendMessage(ctx context.Context, userSub, orgID string, projectID, envID *uuid.UUID, role, content string, toolName *string) {
	client := s.client()
	if !client.Configured() {
		return
	}
	sessionID := agentchat.SessionIDFrom(ctx)
	if sessionID == uuid.Nil {
		return
	}

	metadata := map[string]any{}
	if orgID != "" {
		metadata["org_id"] = orgID
	}
	if projectID != nil {
		metadata["project_id"] = projectID.String()
	}
	if envID != nil {
		metadata["env_id"] = envID.String()
	}
	if toolName != nil && *toolName != "" {
		metadata["tool_name"] = *toolName
	}
	if traceID := agentchat.TraceIDFrom(ctx); traceID != "" {
		metadata["turn_trace_id"] = traceID
	}

	now := langfuse.FormatTime(time.Now())
	client.IngestAsync([]langfuse.Event{{
		ID:        uuid.NewString(),
		Type:      langfuse.EventTypeTraceCreate,
		Timestamp: now,
		Body: langfuse.TraceBody{
			ID:        uuid.NewString(),
			Timestamp: now,
			Name:      agentChatMessageTracePrefix + role,
			UserID:    userSub,
			SessionID: sessionID.String(),
			Output:    content,
			Metadata:  metadata,
			Tags:      []string{"transcript"},
		},
	}})
}

func (s langfuseChatStore) SessionMessages(ctx context.Context, sessionID uuid.UUID, limit int) ([]chatMessage, error) {
	client := s.client()
	if !client.Configured() || sessionID == uuid.Nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, agentChatStoreReadTimeout)
	defer cancel()

	want := limit
	if want <= 0 || want > agentChatStoreMaxMessages {
		want = agentChatStoreMaxMessages
	}

	newestFirst := make([]chatMessage, 0, want)
	totalPages := 1
	for page := 1; page <= totalPages && len(newestFirst) < want; page++ {
		list, err := client.ListTraces(ctx, langfuse.TraceQuery{
			SessionID: sessionID.String(),
			OrderBy:   "timestamp.desc",
			Page:      page,
			Limit:     langfuse.MaxPageLimit,
		})
		if err != nil {
			return nil, err
		}
		totalPages = list.Meta.TotalPages
		if len(list.Data) == 0 {
			break
		}
		for _, tr := range list.Data {
			if msg, ok := agentChatMessageFromTrace(tr); ok {
				newestFirst = append(newestFirst, msg)
			}
		}
		if len(newestFirst) >= want && page < totalPages {
			log.Printf("agent-chat: session %s has more than %d messages in langfuse, older ones dropped", sessionID, want)
		}
	}

	if len(newestFirst) > want {
		newestFirst = newestFirst[:want]
	}
	out := make([]chatMessage, len(newestFirst))
	for i, msg := range newestFirst {
		out[len(newestFirst)-1-i] = msg
	}
	return out, nil
}

func (s langfuseChatStore) DailyUserMessageCount(ctx context.Context, userSub string) (int64, error) {
	client := s.client()
	if !client.Configured() {
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(ctx, agentChatStoreReadTimeout)
	defer cancel()

	count, err := client.CountTraces(ctx, langfuse.TraceQuery{
		UserID:        userSub,
		Name:          agentChatMessageTracePrefix + "user",
		FromTimestamp: startOfDayUTC(time.Now()),
	})
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}

// agentChatMessageFromTrace turns a transcript trace back into a message and
// rejects anything that is not one. The same Langfuse project also holds the
// per-turn traces, and replaying one of those to the model as a chat message
// would be a lie about what was said.
func agentChatMessageFromTrace(tr langfuse.Trace) (chatMessage, bool) {
	role := strings.TrimPrefix(tr.Name, agentChatMessageTracePrefix)
	switch {
	case role == tr.Name:
		return chatMessage{}, false
	case role != "user" && role != "assistant" && role != "tool":
		return chatMessage{}, false
	}

	var content string
	if err := json.Unmarshal(tr.Output, &content); err != nil {
		return chatMessage{}, false
	}

	msg := chatMessage{Role: role, Content: content}
	var meta struct {
		ToolName string `json:"tool_name"`
	}
	if err := json.Unmarshal(tr.Metadata, &meta); err == nil {
		msg.ToolName = meta.ToolName
	}
	return msg, true
}

func startOfDayUTC(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}
