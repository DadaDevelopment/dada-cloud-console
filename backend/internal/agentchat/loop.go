package agentchat

import (
	"context"
	"fmt"

	"github.com/dada-tuda/console/backend/internal/llmchat"
)

const MaxToolCallsPerTurn = 10

const maxRounds = MaxToolCallsPerTurn + 4

type ToolLogEntry struct {
	Name    string
	Result  string
	IsError bool
}

type Emitter struct {
	Token    func(text string)
	ToolCall func(name string)
}

func RunTurn(
	ctx context.Context,
	llm *llmchat.Client,
	tools *Toolset,
	bearer string,
	systemPrompt string,
	history []llmchat.Message,
	userMessage string,
	emit Emitter,
) (assistantText string, toolLog []ToolLogEntry, err error) {
	messages := make([]llmchat.Message, 0, len(history)+2)
	messages = append(messages, llmchat.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, history...)
	messages = append(messages, llmchat.Message{Role: "user", Content: userMessage})

	toolCallCount := 0

	for round := 0; round < maxRounds; round++ {
		var toolDefs []llmchat.ToolDef
		if toolCallCount < MaxToolCallsPerTurn {
			toolDefs = tools.Defs
		}

		result, streamErr := llm.StreamChatCompletion(ctx, messages, toolDefs, emit.Token)
		if streamErr != nil {
			return "", toolLog, streamErr
		}

		if len(result.ToolCalls) == 0 {
			return result.Content, toolLog, nil
		}

		messages = append(messages, llmchat.Message{
			Role:      "assistant",
			Content:   result.Content,
			ToolCalls: result.ToolCalls,
		})

		for _, call := range result.ToolCalls {
			if toolCallCount >= MaxToolCallsPerTurn {
				messages = append(messages, llmchat.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    "tool budget exhausted for this turn; answer with what you already know or offer create_support_ticket",
				})
				continue
			}
			toolCallCount++
			if emit.ToolCall != nil {
				emit.ToolCall(call.Function.Name)
			}
			text, isError := tools.Execute(ctx, bearer, call.Function.Name, call.Function.Arguments)
			toolLog = append(toolLog, ToolLogEntry{Name: call.Function.Name, Result: text, IsError: isError})
			messages = append(messages, llmchat.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    text,
			})
		}
	}

	return "", toolLog, fmt.Errorf("agent loop exceeded %d rounds without a final answer", maxRounds)
}
