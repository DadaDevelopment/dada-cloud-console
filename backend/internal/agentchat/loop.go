package agentchat

import (
	"context"
	"fmt"
	"time"

	"github.com/dada-tuda/console/backend/internal/llmchat"
)

const MaxToolCallsPerTurn = 10

const maxRounds = MaxToolCallsPerTurn + 4

const MaxWriteCallsPerTurn = 3

const writeInterruptSkipMessage = "skipped: this turn paused for a pending user confirmation on an earlier action in the same round"

const writeBudgetExhaustedMessage = "write action budget exhausted for this turn; answer with what you already know or offer create_support_ticket"

// ToolLogEntry is one executed tool call. ArgsJSON holds the raw arguments and
// may contain secrets (setEnvVar values, database credentials): any consumer
// that persists or renders it must redact it first. DurationMs is wall time
// measured around Toolset.Execute. Preflight marks entries the engine produced
// itself before the first LLM call -- those spend none of MaxToolCallsPerTurn.
type ToolLogEntry struct {
	Name       string
	ArgsJSON   string
	Result     string
	IsError    bool
	DurationMs int64
	Preflight  bool
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

// TurnResult is everything one turn produced. ToolCallCount/WriteCallCount are
// the budget counters as of the end of the turn (preflight calls excluded, they
// are free). Inventory* report what the engine saw on its own before the first
// LLM call: InventoryAppsLookedUp distinguishes "zero apps" from "listApps was
// never reached".
type TurnResult struct {
	AssistantText         string
	ToolLog               []ToolLogEntry
	Pending               *PendingWrite
	Usage                 Usage
	ToolCallCount         int
	WriteCallCount        int
	InventoryProjects     int
	InventoryApps         int
	InventoryAppsLookedUp bool
	PreflightCalls        int
}

// RunTurn runs one user turn. Before the first LLM call it grounds itself with
// runInventoryPreflight and, when anything was found, injects the inventory as
// a system message immediately before the user's message.
//
// tools is a per-turn ToolView, not the whole catalog: the model is offered a
// handful of navigation tools plus search_tools and grows its own list from
// there, so a prompt costs a fraction of shipping every definition every round.
func RunTurn(
	ctx context.Context,
	llm *llmchat.Client,
	tools *ToolView,
	bearer string,
	endUser string,
	systemPrompt string,
	history []llmchat.Message,
	userMessage string,
	turnCtx TurnContext,
	emit Emitter,
) (TurnResult, error) {
	inv, preflightLog := runInventoryPreflight(ctx, tools, bearer, turnCtx, emit)

	messages := make([]llmchat.Message, 0, len(history)+3)
	messages = append(messages, llmchat.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, history...)
	if inv != nil {
		if invMsg := inv.systemMessage(); invMsg != "" {
			messages = append(messages, llmchat.Message{Role: "system", Content: invMsg})
		}
	}
	messages = append(messages, llmchat.Message{Role: "user", Content: userMessage})

	res, err := runLoop(ctx, llm, tools, bearer, endUser, messages, 0, 0, emit)

	res.ToolLog = append(append([]ToolLogEntry{}, preflightLog...), res.ToolLog...)
	res.PreflightCalls = len(preflightLog)
	if inv != nil {
		res.InventoryProjects = len(inv.Projects)
		res.InventoryApps = len(inv.Apps)
		res.InventoryAppsLookedUp = inv.AppsLookedUp
	}
	return res, err
}

// ResumeTurn continues a turn that paused on a write confirmation. It runs no
// inventory preflight: the turn context is already baked into the messages
// snapshot taken when the turn paused, and re-grounding would both duplicate
// the inventory and contradict the snapshot.
func ResumeTurn(
	ctx context.Context,
	llm *llmchat.Client,
	tools *ToolView,
	bearer string,
	endUser string,
	messages []llmchat.Message,
	toolCallCount int,
	writeCallCount int,
	emit Emitter,
) (TurnResult, error) {
	return runLoop(ctx, llm, tools, bearer, endUser, messages, toolCallCount, writeCallCount, emit)
}

func runLoop(
	ctx context.Context,
	llm *llmchat.Client,
	tools *ToolView,
	bearer string,
	endUser string,
	messages []llmchat.Message,
	toolCallCount int,
	writeCallCount int,
	emit Emitter,
) (TurnResult, error) {
	var toolLog []ToolLogEntry
	var pending *PendingWrite
	var usage Usage

	for round := 0; round < maxRounds; round++ {
		var toolDefs []llmchat.ToolDef
		if toolCallCount < MaxToolCallsPerTurn {
			toolDefs = tools.Defs()
		}

		result, streamErr := llm.StreamChatCompletion(ctx, messages, toolDefs, endUser, emit.Token)
		if streamErr != nil {
			return TurnResult{
				ToolLog:        toolLog,
				Usage:          usage,
				ToolCallCount:  toolCallCount,
				WriteCallCount: writeCallCount,
			}, streamErr
		}
		usage.PromptTokens += result.PromptTokens
		usage.CompletionTokens += result.CompletionTokens
		usage.TotalTokens += result.TotalTokens
		usage.Calls++
		if result.Model != "" {
			usage.Model = result.Model
		}

		if len(result.ToolCalls) == 0 {
			return TurnResult{
				AssistantText:  result.Content,
				ToolLog:        toolLog,
				Usage:          usage,
				ToolCallCount:  toolCallCount,
				WriteCallCount: writeCallCount,
			}, nil
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

			if !IsMetaTool(call.Function.Name) {
				toolCallCount++
			}
			if emit.ToolCall != nil {
				emit.ToolCall(call.Function.Name)
			}
			started := time.Now()
			text, isError := tools.Execute(ctx, bearer, call.Function.Name, call.Function.Arguments)
			toolLog = append(toolLog, ToolLogEntry{
				Name:       call.Function.Name,
				ArgsJSON:   call.Function.Arguments,
				Result:     text,
				IsError:    isError,
				DurationMs: time.Since(started).Milliseconds(),
			})
			messages = append(messages, llmchat.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    text,
			})
		}

		if pending != nil {
			return TurnResult{
				ToolLog:        toolLog,
				Pending:        pending,
				Usage:          usage,
				ToolCallCount:  toolCallCount,
				WriteCallCount: writeCallCount,
			}, nil
		}
	}

	return TurnResult{
		ToolLog:        toolLog,
		Usage:          usage,
		ToolCallCount:  toolCallCount,
		WriteCallCount: writeCallCount,
	}, fmt.Errorf("agent loop exceeded %d rounds without a final answer", maxRounds)
}
