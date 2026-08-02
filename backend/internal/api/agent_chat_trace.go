package api

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/dada-tuda/console/backend/internal/agentchat"
	"github.com/dada-tuda/console/backend/internal/langfuse"
	"github.com/google/uuid"
)

const agentChatTracePersistTimeout = 5 * time.Second

const agentChatTraceMaxText = 4000

// agentChatLangfuse builds the tracing client for one turn. New is cheap (the
// HTTP client is shared package-wide), so no field on Handler is needed, and a
// nil return is fine: every Client method tolerates a nil receiver, which keeps
// tests that construct Handler as a bare literal working.
func (h *Handler) agentChatLangfuse() *langfuse.Client {
	if h == nil || h.cfg == nil {
		return nil
	}
	return langfuse.New(h.cfg.LangfuseHost, h.cfg.LangfusePublicKey, h.cfg.LangfuseSecretKey, h.cfg.LangfuseEnabled)
}

// agentChatRecordTurn closes out a turn trace: one row in agent_chat_turns and
// one fire-and-forget batch to Langfuse. It is the only entry point the SSE
// handlers need, and it never returns an error because failing to record a turn
// must never change what the user sees.
func (h *Handler) agentChatRecordTurn(tr *agentchat.TurnTrace) {
	if h == nil || tr == nil {
		return
	}
	if tr.FinishedAt.IsZero() {
		tr.Finish(agentchat.OutcomeError, "unfinished")
	}
	h.agentChatPersistTurn(tr)
	h.agentChatLangfuse().IngestAsync(agentChatLangfuseBatch(tr))
}

func (h *Handler) agentChatPersistTurn(tr *agentchat.TurnTrace) {
	if h.pool == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), agentChatTracePersistTimeout)
	defer cancel()

	var orgArg, modelArg, outcomeArg, errArg, pendingToolArg, pendingArgsArg, inputArg, outputArg any
	if tr.OrgID != "" {
		orgArg = tr.OrgID
	}
	if tr.Usage.Model != "" {
		modelArg = tr.Usage.Model
	}
	if tr.Outcome != "" {
		outcomeArg = string(tr.Outcome)
	}
	if tr.ErrorCode != "" {
		errArg = tr.ErrorCode
	}
	if tr.PendingToolName != "" {
		pendingToolArg = tr.PendingToolName
	}
	if len(tr.PendingArgs) > 0 {
		if b, err := json.Marshal(tr.PendingArgs); err == nil {
			pendingArgsArg = b
		}
	}
	if tr.InputMessage != "" {
		inputArg = agentchat.TruncateForTrace(tr.InputMessage, agentChatTraceMaxText)
	}
	if tr.OutputText != "" {
		outputArg = agentchat.TruncateForTrace(tr.OutputText, agentChatTraceMaxText)
	}

	if _, err := h.pool.Exec(ctx,
		`INSERT INTO agent_chat_turns (
			trace_id, user_sub, org_id, project_id, env_id, kind,
			input_message, output_text, tool_calls,
			tool_call_count, write_call_count, preflight_calls, gateway_calls,
			prompt_tokens, completion_tokens, total_tokens, model,
			latency_ms, outcome, error_code, pending_tool_name, pending_args,
			context_project_present, context_app_present, inventory_apps, inventory_projects
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)`,
		tr.TraceID.String(), tr.UserSub, orgArg, tr.ProjectID, tr.EnvID, string(tr.Kind),
		inputArg, outputArg, tr.ToolCallsJSON(),
		tr.ToolCallCount(), tr.WriteCallCount, tr.PreflightCalls, tr.Usage.Calls,
		tr.Usage.PromptTokens, tr.Usage.CompletionTokens, tr.Usage.TotalTokens, modelArg,
		tr.LatencyMs(), outcomeArg, errArg, pendingToolArg, pendingArgsArg,
		tr.ContextProjectPresent, tr.ContextAppPresent, tr.InventoryApps, tr.InventoryProjects,
	); err != nil {
		log.Printf("agent-chat: failed to persist turn trace: %v", err)
	}
}

// agentChatLangfuseBatch renders a finished turn as a Langfuse ingestion batch:
// one trace, one generation rolling up the whole ReAct loop, and one TOOL
// observation per tool call, in call order.
func agentChatLangfuseBatch(tr *agentchat.TurnTrace) []langfuse.Event {
	if tr == nil {
		return nil
	}

	now := langfuse.FormatTime(time.Now())
	traceID := tr.TraceID.String()
	genID := uuid.NewString()

	input := agentchat.TruncateForTrace(tr.InputMessage, agentChatTraceMaxText)
	output := agentchat.TruncateForTrace(tr.OutputText, agentChatTraceMaxText)

	var projectID, envID string
	if tr.ProjectID != nil {
		projectID = tr.ProjectID.String()
	}
	if tr.EnvID != nil {
		envID = tr.EnvID.String()
	}

	batch := make([]langfuse.Event, 0, 2+len(tr.ToolSpans))
	batch = append(batch, langfuse.Event{
		ID:        uuid.NewString(),
		Type:      langfuse.EventTypeTraceCreate,
		Timestamp: now,
		Body: langfuse.TraceBody{
			ID:        traceID,
			Timestamp: langfuse.FormatTime(tr.StartedAt),
			Name:      "agent-chat-" + string(tr.Kind),
			UserID:    tr.UserSub,
			SessionID: agentChatSessionID(tr),
			Input:     input,
			Output:    output,
			Tags:      []string{string(tr.Kind), string(tr.Outcome)},
			Metadata: map[string]any{
				"org_id":                  tr.OrgID,
				"project_id":              projectID,
				"env_id":                  envID,
				"outcome":                 string(tr.Outcome),
				"error_code":              tr.ErrorCode,
				"tool_call_count":         tr.ToolCallCount(),
				"write_call_count":        tr.WriteCallCount,
				"preflight_calls":         tr.PreflightCalls,
				"gateway_calls":           tr.Usage.Calls,
				"context_project_present": tr.ContextProjectPresent,
				"context_app_present":     tr.ContextAppPresent,
				"inventory_apps":          tr.InventoryApps,
				"inventory_projects":      tr.InventoryProjects,
				"pending_tool_name":       tr.PendingToolName,
				"latency_ms":              tr.LatencyMs(),
			},
		},
	})

	genLevel := langfuse.LevelDefault
	if tr.Outcome == agentchat.OutcomeError {
		genLevel = langfuse.LevelError
	}
	batch = append(batch, langfuse.Event{
		ID:        uuid.NewString(),
		Type:      langfuse.EventTypeObservationCreate,
		Timestamp: now,
		Body: langfuse.ObservationBody{
			ID:        genID,
			TraceID:   traceID,
			Type:      langfuse.ObservationTypeGeneration,
			Name:      "agent-loop",
			StartTime: langfuse.FormatTime(tr.StartedAt),
			EndTime:   langfuse.FormatTime(tr.FinishedAt),
			Model:     tr.Usage.Model,
			Usage: &langfuse.Usage{
				PromptTokens:     tr.Usage.PromptTokens,
				CompletionTokens: tr.Usage.CompletionTokens,
				TotalTokens:      tr.Usage.TotalTokens,
			},
			Input:         input,
			Output:        output,
			Level:         genLevel,
			StatusMessage: tr.ErrorCode,
			Metadata:      map[string]any{"gateway_calls": tr.Usage.Calls},
		},
	})

	cursor := tr.StartedAt
	for _, span := range tr.ToolSpans {
		start := cursor
		cursor = cursor.Add(time.Duration(span.DurationMs) * time.Millisecond)
		level := langfuse.LevelDefault
		if !span.OK {
			level = langfuse.LevelError
		}
		batch = append(batch, langfuse.Event{
			ID:        uuid.NewString(),
			Type:      langfuse.EventTypeObservationCreate,
			Timestamp: now,
			Body: langfuse.ObservationBody{
				ID:                  uuid.NewString(),
				TraceID:             traceID,
				ParentObservationID: genID,
				Type:                langfuse.ObservationTypeTool,
				Name:                span.Name,
				StartTime:           langfuse.FormatTime(start),
				EndTime:             langfuse.FormatTime(cursor),
				Input:               span.Args,
				Output: map[string]any{
					"ok":         span.OK,
					"result_len": span.ResultLen,
					"error":      span.Error,
				},
				Level:         level,
				StatusMessage: span.Error,
			},
		})
	}

	return batch
}

func agentChatSessionID(tr *agentchat.TurnTrace) string {
	project := "none"
	if tr.ProjectID != nil {
		project = tr.ProjectID.String()
	}
	return tr.UserSub + "|" + project
}
