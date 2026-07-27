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

// Usage is the summed LLM token usage for a whole turn. A turn is a ReAct loop
// of several gateway calls; PromptTokens/CompletionTokens/TotalTokens add up
// every call's trailing usage chunk, Calls counts the gateway round-trips, and
// Model is the last resolved model id the gateway reported.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Model            string
	Calls            int
}

func RunTurn(
	ctx context.Context,
	llm *llmchat.Client,
	tools *Toolset,
	bearer string,
	endUser string,
	systemPrompt string,
	history []llmchat.Message,
	userMessage string,
	emit Emitter,
) (assistantText string, toolLog []ToolLogEntry, pending *PendingWrite, usage Usage, err error) {
	messages := make([]llmchat.Message, 0, len(history)+2)
	messages = append(messages, llmchat.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, history...)
	messages = append(messages, llmchat.Message{Role: "user", Content: userMessage})

	return runLoop(ctx, llm, tools, bearer, endUser, messages, 0, 0, emit)
}

func ResumeTurn(
	ctx context.Context,
	llm *llmchat.Client,
	tools *Toolset,
	bearer string,
	endUser string,
	messages []llmchat.Message,
	toolCallCount int,
	writeCallCount int,
	emit Emitter,
) (assistantText string, toolLog []ToolLogEntry, pending *PendingWrite, usage Usage, err error) {
	return runLoop(ctx, llm, tools, bearer, endUser, messages, toolCallCount, writeCallCount, emit)
}

func runLoop(
	ctx context.Context,
	llm *llmchat.Client,
	tools *Toolset,
	bearer string,
	endUser string,
	messages []llmchat.Message,
	toolCallCount int,
	writeCallCount int,
	emit Emitter,
) (assistantText string, toolLog []ToolLogEntry, pending *PendingWrite, usage Usage, err error) {
	for round := 0; round < maxRounds; round++ {
		var toolDefs []llmchat.ToolDef
		if toolCallCount < MaxToolCallsPerTurn {
			toolDefs = tools.Defs
		}

		result, streamErr := llm.StreamChatCompletion(ctx, messages, toolDefs, endUser, emit.Token)
		if streamErr != nil {
			return "", toolLog, nil, usage, streamErr
		}
		usage.PromptTokens += result.PromptTokens
		usage.CompletionTokens += result.CompletionTokens
		usage.TotalTokens += result.TotalTokens
		usage.Calls++
		if result.Model != "" {
			usage.Model = result.Model
		}

		if len(result.ToolCalls) == 0 {
			return result.Content, toolLog, nil, usage, nil
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
			return "", toolLog, pending, usage, nil
		}
	}

	return "", toolLog, nil, usage, fmt.Errorf("agent loop exceeded %d rounds without a final answer", maxRounds)
}
