package agentchat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dada-tuda/console/backend/internal/llmchat"
)

const MaxToolCallsPerTurn = 10

const maxRounds = MaxToolCallsPerTurn + 4

const MaxWriteCallsPerTurn = 3

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

// PendingWrite is one write waiting for the user's decision. Queued holds the
// other writes the model asked for in the same round, in order: they are shown
// as their own confirmation cards once this one is resolved, so every call the
// model made gets a real result instead of a skip stub. Messages is the
// conversation as of the pause and is set only on the card currently open.
type PendingWrite struct {
	ToolName       string
	ToolCallID     string
	ArgsJSON       string
	Messages       []llmchat.Message
	ToolCallCount  int
	WriteCallCount int
	Queued         []PendingWrite
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
// runInventoryPreflight and, when anything was found, prefixes the inventory to
// the user's message. Per-turn context rides on that message rather than on a
// system message so the system prompt is one stable string the whole session.
//
// tools is a ToolView, not the whole catalog: the model starts with the base
// navigation tools plus load_tool and grows the array itself by loading what it
// needs. The tools block is re-sent on every gateway call, so shipping all 90
// schemas would cost ~12.6k tokens per call against ~1.1k for the base set.
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

	messages := make([]llmchat.Message, 0, len(history)+2)
	messages = append(messages, llmchat.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, history...)
	if inv != nil {
		if invMsg := inv.systemMessage(); invMsg != "" {
			userMessage = invMsg + "\n\n" + userMessage
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
// the inventory and contradict the snapshot. It does restore the tools the
// paused turn had loaded, since the view is rebuilt per HTTP request and the
// model would otherwise find the tool it just used missing from its array.
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
	tools.Load(loadedToolNames(messages)...)
	return runLoop(ctx, llm, tools, bearer, endUser, messages, toolCallCount, writeCallCount, emit)
}

// loadedToolNames replays the load_tool calls recorded in a conversation
// snapshot. Names the view cannot dispatch are dropped by Load itself.
func loadedToolNames(messages []llmchat.Message) []string {
	var out []string
	for _, m := range messages {
		for _, call := range m.ToolCalls {
			if call.Function.Name != LoadToolTool {
				continue
			}
			var args struct {
				Names []string `json:"names"`
			}
			if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				continue
			}
			out = append(out, args.Names...)
		}
	}
	return out
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
	var queued []PendingWrite
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

		for _, call := range result.ToolCalls {
			effName, effArgs := call.Function.Name, call.Function.Arguments

			if toolCallCount >= MaxToolCallsPerTurn {
				messages = append(messages, llmchat.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    "tool budget exhausted for this turn; answer with what you already know or offer create_support_ticket",
				})
				continue
			}

			isWrite := tools.IsWrite(effName)
			if isWrite && writeCallCount+len(queued) >= MaxWriteCallsPerTurn {
				messages = append(messages, llmchat.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    writeBudgetExhaustedMessage,
				})
				continue
			}

			if tools.NeedsConfirmation(effName) {
				queued = append(queued, PendingWrite{
					ToolName:   effName,
					ToolCallID: call.ID,
					ArgsJSON:   effArgs,
				})
				continue
			}

			if !IsMetaTool(effName) {
				toolCallCount++
			}
			if emit.ToolCall != nil {
				emit.ToolCall(effName)
			}
			started := time.Now()
			text, isError := tools.Execute(ctx, bearer, effName, effArgs)
			if isWrite && !isError {
				writeCallCount++
			}
			toolLog = append(toolLog, ToolLogEntry{
				Name:       effName,
				ArgsJSON:   effArgs,
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

		if len(queued) > 0 {
			snapshot := make([]llmchat.Message, len(messages))
			copy(snapshot, messages)
			pending = &PendingWrite{
				ToolName:       queued[0].ToolName,
				ToolCallID:     queued[0].ToolCallID,
				ArgsJSON:       queued[0].ArgsJSON,
				Messages:       snapshot,
				ToolCallCount:  toolCallCount,
				WriteCallCount: writeCallCount,
				Queued:         append([]PendingWrite{}, queued[1:]...),
			}
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
