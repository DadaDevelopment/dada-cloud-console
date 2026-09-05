package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RuntimeLinkMeta mirrors tggateway.RuntimeLinkMeta: one URL found in a
// message plus its best-effort page title.
type RuntimeLinkMeta struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

// RuntimeAttachment mirrors tggateway.RuntimeAttachment across the
// contract: media metadata plus resolver outputs (transcript/description
// with availability flags).
type RuntimeAttachment struct {
	Kind                 string `json:"kind"`
	FileID               string `json:"file_id,omitempty"`
	FilePath             string `json:"file_path,omitempty"`
	MimeType             string `json:"mime_type,omitempty"`
	FileName             string `json:"file_name,omitempty"`
	DurationSec          int    `json:"duration_seconds,omitempty"`
	SizeBytes            int64  `json:"size_bytes,omitempty"`
	Transcript           string `json:"transcript,omitempty"`
	TranscriptAvailable  bool   `json:"transcript_available"`
	Description          string `json:"description,omitempty"`
	DescriptionAvailable bool   `json:"description_available"`
}

// InboundMessage is one message of a (possibly debounced) batch: each keeps
// its own channel identity and gets its own conversation_messages row, while
// the whole batch shares one agent run and one reply. Links carries the
// gateway-extracted URL entities (Agent Harness v2, Step 5): persisted into
// the row's entities column and rendered into the A2A context block.
// Attachment (Step 6) carries media: persisted to the attachments JSONB and
// rendered as a typed line (voice/image/document) in the context.
type InboundMessage struct {
	Content                 string
	ChannelMessageID        string
	ThreadID                string
	SourceSentAt            *time.Time
	ReplyToChannelMessageID string
	Links                   []RuntimeLinkMeta
	Attachment              *RuntimeAttachment
}

// MessageRequest is one inbound TURN from a channel gateway: one or more
// messages (a debounced batch), one agent invocation, one reply. Every
// message's channel identity is preserved on its own row; the fields the
// single-message shortcut used to carry (Content etc) are folded into
// Messages by the server layer.
type MessageRequest struct {
	AgentName  string
	Channel    string
	ExternalID string
	Actor      Actor
	Messages   []InboundMessage
}

// MessageResponse carries the agent's reply plus the reply anchor: the
// channel id of the LAST user message of the batch, so the gateway can send
// the answer as a native Telegram reply to the right message. Empty when
// the batch carried no channel ids (manual/system messages).
type MessageResponse struct {
	Text                    string
	ReplyToChannelMessageID string
	Suppressed              bool
}

type A2AClient interface {
	Send(ctx context.Context, run AgentRunRequest) (reply string, err error)
}

type DomainProvider interface {
	GetDomain(ctx context.Context, agentName, domain string) (content string, err error)
}

type Runtime struct {
	store      ConversationStore
	hooks      HookExecutor
	a2a        A2AClient
	domains    DomainProvider
	states     StateStore
	contextKey []byte
	runLocks   [256]sync.Mutex
}

func NewRuntime(store ConversationStore, hooks HookExecutor, a2a A2AClient, domains DomainProvider) *Runtime {
	states, _ := store.(StateStore)
	return &Runtime{
		states:  states,
		store:   store,
		hooks:   hooks,
		a2a:     a2a,
		domains: domains,
	}
}

func (r *Runtime) ProcessMessage(ctx context.Context, req MessageRequest) (MessageResponse, error) {
	if len(req.Messages) == 0 || req.AgentName == "" || req.Channel == "" || req.ExternalID == "" {
		return MessageResponse{}, fmt.Errorf("agent, channel, identity and messages are required")
	}
	if r.states == nil || len(r.contextKey) < 32 {
		return MessageResponse{}, fmt.Errorf("runtime state or context signing is not configured")
	}
	conv, created, err := r.store.GetOrCreateConversation(ctx, req.AgentName, req.Channel, req.ExternalID, req.Actor)
	if err != nil {
		return MessageResponse{}, fmt.Errorf("get conversation: %w", err)
	}
	// The service is deployed as a single replica; serialize concurrent requests
	// for the same conversation while tools mutate state through independent calls.
	lock := &r.runLocks[conv.ID[0]]
	lock.Lock()
	defer lock.Unlock()
	state, err := r.states.GetState(ctx, conv.ID)
	if err != nil {
		return MessageResponse{}, err
	}
	var fresh []Message
	anchor := ""
	for _, m := range req.Messages {
		anchor = m.ChannelMessageID
		if m.ChannelMessageID != "" {
			_, err := r.store.FindMessageByChannelID(ctx, conv.ID, m.ChannelMessageID)
			if err == nil {
				continue
			}
			if !errors.Is(err, ErrMessageNotFound) {
				return MessageResponse{}, err
			}
		}
		saved, err := r.store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: m.Content,
			ChannelMessageID: m.ChannelMessageID, ThreadID: m.ThreadID, SourceSentAt: m.SourceSentAt,
			ReplyToChannelMessageID: m.ReplyToChannelMessageID, Entities: linksToEntities(m.Links), Attachments: attachmentToEntity(m.Attachment)})
		if err != nil {
			return MessageResponse{}, err
		}
		fresh = append(fresh, saved)
	}
	if !state.AgentEnabled {
		return MessageResponse{Suppressed: true}, nil
	}
	if created {
		if err := r.hooks.Execute(ctx, "conversation.created", conv, nil); err != nil {
			return r.pauseAfterHookFailure(ctx, conv, "conversation.created", err)
		}
	}
	for _, m := range fresh {
		if err := r.hooks.Execute(ctx, "message.received", conv, m.Content); err != nil {
			return r.pauseAfterHookFailure(ctx, conv, "message.received", err)
		}
		if err := r.store.ClearIdleFlag(ctx, conv.ID); err != nil {
			return MessageResponse{}, fmt.Errorf("clear idle flag: %w", err)
		}
	}
	conv, err = r.store.GetConversation(ctx, conv.ID)
	if err != nil {
		return MessageResponse{}, err
	}
	state, err = r.states.GetState(ctx, conv.ID)
	if err != nil {
		return MessageResponse{}, err
	}
	if !state.AgentEnabled {
		return MessageResponse{Suppressed: true}, nil
	}
	inbox, ok := r.store.(interface {
		PendingRuntimeMessages(context.Context, uuid.UUID) ([]Message, error)
	})
	if !ok {
		return MessageResponse{}, fmt.Errorf("runtime pending input storage is not configured")
	}
	pending, err := inbox.PendingRuntimeMessages(ctx, conv.ID)
	if err != nil {
		return MessageResponse{}, err
	}
	if len(pending) == 0 {
		return MessageResponse{Suppressed: true}, nil
	}
	var skills []string
	if catalog, ok := r.domains.(DomainCatalog); ok {
		skills, err = catalog.ListDomains(ctx, conv.AgentName)
		if err != nil {
			return MessageResponse{}, err
		}
	}
	token, err := issueContextToken(r.contextKey, conv, time.Now().Add(15*time.Minute))
	if err != nil {
		return MessageResponse{}, err
	}
	run := AgentRunRequest{AgentName: conv.AgentName, ContextID: "runtime-" + conv.ID.String(), Messages: pending,
		ConversationContext: AgentConversationContext{ConversationID: conv.ID.String(), Channel: conv.Channel,
			ExternalID: conv.ExternalID, Username: conv.ActorUsername, State: state, AvailableSkills: skills, ContextToken: token}}
	reply, err := r.a2a.Send(ctx, run)
	if err != nil {
		return MessageResponse{}, fmt.Errorf("a2a send: %w", err)
	}
	// A tool may have paused this conversation during the run. Never emit the
	// generated answer after that transition, even when the model ignored it.
	after, err := r.states.GetState(ctx, conv.ID)
	if err != nil {
		return MessageResponse{}, err
	}
	if !after.AgentEnabled {
		return MessageResponse{Suppressed: true}, nil
	}
	reply = redactContextToken(reply, token)
	if _, err := r.store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "assistant", Content: reply}); err != nil {
		return MessageResponse{}, err
	}
	if err := r.hooks.Execute(ctx, "agent.run.completed", conv, nil); err != nil {
		return r.pauseAfterHookFailure(ctx, conv, "agent.run.completed", err)
	}
	if err := r.store.Touch(ctx, conv.ID); err != nil {
		return MessageResponse{}, err
	}
	if receipts, ok := r.store.(interface {
		MarkRuntimeHandled(context.Context, []Message) error
	}); ok {
		if err := receipts.MarkRuntimeHandled(ctx, pending); err != nil {
			return MessageResponse{}, err
		}
	} else {
		return MessageResponse{}, fmt.Errorf("runtime receipt storage is not configured")
	}
	return MessageResponse{Text: reply, ReplyToChannelMessageID: anchor}, nil
}

// linksToEntities converts gateway link metadata into the generic entity
// objects the entities JSONB column stores: {"url": ..., "title": ...}.
func linksToEntities(links []RuntimeLinkMeta) []any {
	if len(links) == 0 {
		return nil
	}
	out := make([]any, 0, len(links))
	for _, l := range links {
		if l.URL == "" {
			continue
		}
		e := map[string]any{"url": l.URL}
		if l.Title != "" {
			e["title"] = l.Title
		}
		out = append(out, e)
	}
	return out
}

// attachmentToEntity converts the attachment descriptor into the single
// object the attachments JSONB column stores (nil when no attachment).
func attachmentToEntity(a *RuntimeAttachment) []any {
	if a == nil {
		return nil
	}
	obj := map[string]any{"kind": a.Kind}
	if a.FileID != "" {
		obj["file_id"] = a.FileID
	}
	if a.FilePath != "" {
		obj["file_path"] = a.FilePath
	}
	if a.MimeType != "" {
		obj["mime_type"] = a.MimeType
	}
	if a.FileName != "" {
		obj["file_name"] = a.FileName
	}
	if a.DurationSec > 0 {
		obj["duration_seconds"] = a.DurationSec
	}
	if a.SizeBytes > 0 {
		obj["size_bytes"] = a.SizeBytes
	}
	if a.TranscriptAvailable {
		obj["transcript"] = a.Transcript
	}
	if a.DescriptionAvailable {
		obj["description"] = a.Description
	}
	return []any{obj}
}

// An external hook can have an unknown outcome after a timeout. Pause rather
// than replay a potentially non-idempotent action or bypass it on the next turn.
// Recovery is explicit; the simple CRM status sync is retried by the service.
func (r *Runtime) pauseAfterHookFailure(ctx context.Context, conv Conversation, event string, cause error) (MessageResponse, error) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := r.states.PauseAgent(persistCtx, conv.ID, "integration hook failed: "+event); err != nil {
		return MessageResponse{}, fmt.Errorf("hook failed and pause unavailable: %w", err)
	}
	return MessageResponse{}, cause
}
