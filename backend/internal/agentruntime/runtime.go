package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// InboundMessage is one message of a (possibly debounced) batch: each keeps
// its own channel identity and gets its own conversation_messages row, while
// the whole batch shares one agent run and one reply.
type InboundMessage struct {
	Content                 string
	ChannelMessageID        string
	ThreadID                string
	SourceSentAt            *time.Time
	ReplyToChannelMessageID string
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
}

type A2AClient interface {
	Send(ctx context.Context, agentName string, messages []Message) (reply string, err error)
}

type DomainProvider interface {
	GetDomain(ctx context.Context, agentName, domain string) (content string, err error)
}

type Runtime struct {
	store   ConversationStore
	hooks   HookExecutor
	a2a     A2AClient
	domains DomainProvider
}

func NewRuntime(store ConversationStore, hooks HookExecutor, a2a A2AClient, domains DomainProvider) *Runtime {
	return &Runtime{
		store:   store,
		hooks:   hooks,
		a2a:     a2a,
		domains: domains,
	}
}

func (r *Runtime) ProcessMessage(ctx context.Context, req MessageRequest) (MessageResponse, error) {
	if len(req.Messages) == 0 {
		return MessageResponse{}, fmt.Errorf("process message: no messages in request")
	}

	conv, created, err := r.store.GetOrCreateConversation(ctx, req.AgentName, req.Channel, req.ExternalID, req.Actor)
	if err != nil {
		return MessageResponse{}, fmt.Errorf("get or create conversation: %w", err)
	}

	if created {
		if err := r.hooks.Execute(ctx, "conversation.created", conv, nil); err != nil {
			return MessageResponse{}, fmt.Errorf("conversation.created hook: %w", err)
		}
	}

	for _, m := range req.Messages {
		if err := r.hooks.Execute(ctx, "message.received", conv, m.Content); err != nil {
			return MessageResponse{}, fmt.Errorf("message.received hook: %w", err)
		}
		if _, err := r.store.SaveMessage(ctx, conv.ID, SaveMessageInput{
			Role:                    "user",
			Content:                 m.Content,
			ChannelMessageID:        m.ChannelMessageID,
			ThreadID:                m.ThreadID,
			SourceSentAt:            m.SourceSentAt,
			ReplyToChannelMessageID: m.ReplyToChannelMessageID,
		}); err != nil {
			return MessageResponse{}, fmt.Errorf("save user message: %w", err)
		}
	}

	history, err := r.store.GetRecentMessages(ctx, conv.ID, 20)
	if err != nil {
		return MessageResponse{}, fmt.Errorf("get recent messages: %w", err)
	}

	reply, err := r.a2a.Send(ctx, req.AgentName, history)
	if err != nil {
		return MessageResponse{}, fmt.Errorf("a2a send: %w", err)
	}

	if _, err := r.store.SaveMessage(ctx, conv.ID, SaveMessageInput{
		Role:    "assistant",
		Content: reply,
	}); err != nil {
		return MessageResponse{}, fmt.Errorf("save assistant message: %w", err)
	}

	if err := r.hooks.Execute(ctx, "agent.run.completed", conv, nil); err != nil {
		return MessageResponse{}, fmt.Errorf("agent.run.completed hook: %w", err)
	}

	if err := r.store.Touch(ctx, conv.ID); err != nil {
		return MessageResponse{}, fmt.Errorf("touch conversation: %w", err)
	}

	anchor := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].ChannelMessageID != "" {
			anchor = req.Messages[i].ChannelMessageID
			break
		}
	}

	return MessageResponse{Text: reply, ReplyToChannelMessageID: anchor}, nil
}

func (r *Runtime) buildSystemPrompt(agentName string, conv Conversation) string {
	var sb strings.Builder

	sb.WriteString("You are a helpful assistant.\n\n")

	if len(conv.Metadata) > 0 {
		sb.WriteString("## Conversation Context\n")
		if crmID, ok := conv.Metadata["crm_person_id"].(string); ok {
			sb.WriteString(fmt.Sprintf("CRM Person ID: %s\n", crmID))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Available Tools\n")
	sb.WriteString("- get_domain_instruction(domain: str) -> str\n")
	sb.WriteString("  Available domains: jurisdiction, kyc, registration, objections, handoff\n\n")

	return sb.String()
}
