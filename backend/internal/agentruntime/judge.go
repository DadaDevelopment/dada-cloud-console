package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
	"github.com/dada-tuda/console/backend/internal/langfuse"
	"github.com/dada-tuda/console/backend/internal/langfusebudget"
	"github.com/rs/zerolog/log"
)

// TurnJudge is what the runtime hands a delivered turn to; the agentjudge
// package implements it, tests inject a recorder.
type TurnJudge interface {
	Submit(agent string, t agentjudge.Turn)
}

// langfuseFromEnv is the project the runtime's judge scores into and whose
// unit budget the runtime guards; unconfigured keys yield a client that
// reports !Configured.
func langfuseFromEnv() *langfuse.Client {
	return langfuse.New(os.Getenv("LANGFUSE_HOST"), os.Getenv("LANGFUSE_PUBLIC_KEY"), os.Getenv("LANGFUSE_SECRET_KEY"), true)
}

// judgeFromEnv builds the judge when AGENT_JUDGE_ENABLED=1 and both the LLM
// gateway and the Langfuse keys are configured; otherwise nil, which keeps
// the runtime exactly as it was. A budget guard mutes the judge while the
// project is over its unit budget.
func judgeFromEnv(basePath string, lf *langfuse.Client, budget *langfusebudget.Guard) TurnJudge {
	if os.Getenv("AGENT_JUDGE_ENABLED") != "1" {
		return nil
	}
	url, key, model := os.Getenv("AGENT_JUDGE_LLM_URL"), os.Getenv("AGENT_JUDGE_LLM_KEY"), os.Getenv("AGENT_JUDGE_LLM_MODEL")
	if !lf.Configured() || url == "" || key == "" || model == "" {
		log.Warn().Msg("agentruntime: AGENT_JUDGE_ENABLED but LANGFUSE_* or AGENT_JUDGE_LLM_* incomplete; judge off")
		return nil
	}
	log.Info().Str("model", model).Msg("agentruntime: turn judge on")
	j := agentjudge.New(basePath, &agentjudge.OpenAIChat{BaseURL: url, APIKey: key, Model: model}, lf)
	j.MuteWhen(budget.Exceeded)
	return j
}

// judgeTurn packs the delivered turn for the judge: the client messages of
// this turn, the earlier dialog, the state the agent had, the flags that
// change what counts as a violation, and the reply as it was cut for
// delivery.
func (r *Runtime) judgeTurn(ctx context.Context, conv Conversation, run AgentRunRequest, pending, history []Message, reply string, parts []string, traced A2AReply) {
	if r.judge == nil || traced.TraceID == "" {
		return
	}
	pendingIDs := map[string]bool{}
	for _, m := range pending {
		pendingIDs[m.ID.String()] = true
	}
	t := agentjudge.Turn{TraceID: traced.TraceID, ObservationID: traced.ObservationID, Username: conv.ActorUsername, Reply: reply, Parts: parts,
		NoQuestionThisTurn: run.ConversationContext.NoQuestionThisTurn, ReplyError: run.ConversationContext.ReplyError != ""}
	for _, m := range pending {
		if text := strings.TrimSpace(m.Content); text != "" {
			t.Incoming = append(t.Incoming, text)
		}
	}
	for _, m := range history {
		if pendingIDs[m.ID.String()] || strings.TrimSpace(m.Content) == "" {
			continue
		}
		role := "roman"
		if m.Role == "user" {
			role = "client"
		}
		t.History = append(t.History, agentjudge.Exchange{Role: role, Text: m.Content})
	}
	state := run.ConversationContext.State
	ctxJSON, _ := json.Marshal(map[string]any{"reported_facts": state.ReportedFacts, "open_loops": state.OpenLoops,
		"no_question_this_turn": t.NoQuestionThisTurn, "reply_error": t.ReplyError, "username": conv.ActorUsername})
	t.Context = string(ctxJSON)
	r.judge.Submit(conv.AgentName, t)
}
