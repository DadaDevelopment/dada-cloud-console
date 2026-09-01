package agentruntime

import (
	"context"
	"fmt"
	"strings"
)

type MessageRequest struct {
	AgentName  string
	Channel    string
	ExternalID string
	Actor      Actor
	Content    string
}

type MessageResponse struct {
	Text string
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
	conv, created, err := r.store.GetOrCreateConversation(ctx, req.AgentName, req.Channel, req.ExternalID, req.Actor)
	if err != nil {
		return MessageResponse{}, fmt.Errorf("get or create conversation: %w", err)
	}

	if created {
		if err := r.hooks.Execute(ctx, "conversation.created", conv, nil); err != nil {
			return MessageResponse{}, fmt.Errorf("conversation.created hook: %w", err)
		}
	}

	if err := r.hooks.Execute(ctx, "message.received", conv, req.Content); err != nil {
		return MessageResponse{}, fmt.Errorf("message.received hook: %w", err)
	}

	if _, err := r.store.SaveMessage(ctx, conv.ID, "user", req.Content, nil); err != nil {
		return MessageResponse{}, fmt.Errorf("save user message: %w", err)
	}

	history, err := r.store.GetRecentMessages(ctx, conv.ID, 20)
	if err != nil {
		return MessageResponse{}, fmt.Errorf("get recent messages: %w", err)
	}

	reply, err := r.a2a.Send(ctx, req.AgentName, history)
	if err != nil {
		return MessageResponse{}, fmt.Errorf("a2a send: %w", err)
	}

	if _, err := r.store.SaveMessage(ctx, conv.ID, "assistant", reply, nil); err != nil {
		return MessageResponse{}, fmt.Errorf("save assistant message: %w", err)
	}

	if err := r.hooks.Execute(ctx, "agent.run.completed", conv, nil); err != nil {
		return MessageResponse{}, fmt.Errorf("agent.run.completed hook: %w", err)
	}

	if err := r.store.Touch(ctx, conv.ID); err != nil {
		return MessageResponse{}, fmt.Errorf("touch conversation: %w", err)
	}

	return MessageResponse{Text: reply}, nil
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
