package agentchat

import (
	"context"
	"fmt"

	"github.com/dada-tuda/console/backend/internal/llmchat"
)

const MaxToolCallsPerTurn = 10

const maxRounds = MaxToolCallsPerTurn + 4

const MaxWriteCallsPerTurn = 3

const writeInterruptSkipMessage = "skipped: this turn paused for a pending user confirmation on an earlier action in the same round"

const writeBudgetExhaustedMessage = "write action budget exhausted for this turn; answer with what you already know or offer create_support_ticket"

type ToolLogEntry struct {
	Name    string
	Result  string
	IsError bool
}

type PendingWrite struct {
	ToolName       string
	ToolCallID     string
	ArgsJSON       string
	Messages       []llmchat.Message
	ToolCallCount  int
	WriteCallCount int
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
) (assistantText string, toolLog []ToolLogEntry, pending *PendingWrite, err error) {
	messages := make([]llmchat.Message, 0, len(history)+2)
	messages = append(messages, llmchat.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, history...)
	messages = append(messages, llmchat.Message{Role: "user", Content: userMessage})

	return runLoop(ctx, llm, tools, bearer, messages, 0, 0, emit)
}

func ResumeTurn(
	ctx context.Context,
	llm *llmchat.Client,
	tools *Toolset,
	bearer string,
	messages []llmchat.Message,
	toolCallCount int,
	writeCallCount int,
	emit Emitter,
) (assistantText string, toolLog []ToolLogEntry, pending *PendingWrite, err error) {
	return runLoop(ctx, llm, tools, bearer, messages, toolCallCount, writeCallCount, emit)
}

func runLoop(
	ctx context.Context,
	llm *llmchat.Client,
	tools *Toolset,
	bearer string,
	messages []llmchat.Message,
	toolCallCount int,
	writeCallCount int,
	emit Emitter,
) (assistantText string, toolLog []ToolLogEntry, pending *PendingWrite, err error) {
	for round := 0; round < maxRounds; round++ {
		var toolDefs []llmchat.ToolDef
		if toolCallCount < MaxToolCallsPerTurn {
			toolDefs = tools.Defs
		}

		result, streamErr := llm.StreamChatCompletion(ctx, messages, toolDefs, emit.Token)
		if streamErr != nil {
			return "", toolLog, nil, streamErr
		}

		if len(result.ToolCalls) == 0 {
			return result.Content, toolLog, nil, nil
		}

		messages = append(messages, llmchat.Message{
			Role:      "assistant",
			Content:   result.Content,
			ToolCalls: result.ToolCalls,
		})

		for i, call := range result.ToolCalls {
			if toolCallCount >= MaxToolCallsPerTurn {
				messages = append(messages, llmchat.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    "tool budget exhausted for this turn; answer with what you already know or offer create_support_ticket",
				})
				continue
			}

			if tools.IsWrite(call.Function.Name) {
				if writeCallCount >= MaxWriteCallsPerTurn {
					messages = append(messages, llmchat.Message{
						Role:       "tool",
						ToolCallID: call.ID,
						Content:    writeBudgetExhaustedMessage,
					})
					continue
				}

				pendingToolName := call.Function.Name
				pendingToolCallID := call.ID
				pendingArgsJSON := call.Function.Arguments

				for j := i + 1; j < len(result.ToolCalls); j++ {
					messages = append(messages, llmchat.Message{
						Role:       "tool",
						ToolCallID: result.ToolCalls[j].ID,
						Content:    writeInterruptSkipMessage,
					})
				}

				snapshot := make([]llmchat.Message, len(messages))
				copy(snapshot, messages)
				pending = &PendingWrite{
					ToolName:       pendingToolName,
					ToolCallID:     pendingToolCallID,
					ArgsJSON:       pendingArgsJSON,
					Messages:       snapshot,
					ToolCallCount:  toolCallCount,
					WriteCallCount: writeCallCount,
				}
				break
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

		if pending != nil {
			return "", toolLog, pending, nil
		}
	}

	return "", toolLog, nil, fmt.Errorf("agent loop exceeded %d rounds without a final answer", maxRounds)
}
