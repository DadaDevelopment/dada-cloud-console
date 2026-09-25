package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
	"github.com/dada-tuda/console/backend/internal/langfuse"
	"github.com/dada-tuda/console/backend/internal/langfusebudget"
	"github.com/rs/zerolog/log"
)

// TurnJudge is what the runtime hands a delivered turn to, and what checks a
// draft before delivery under AGENT_RUNTIME_PRECHECK; the agentjudge package
// implements it, tests inject a recorder.
type TurnJudge interface {
	Submit(agent string, t agentjudge.Turn)
	Check(ctx context.Context, agent string, t agentjudge.Turn) (agentjudge.Precheck, error)
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

// precheckRetryPause is the 429 back-off of the check model: the judge's
// default (10 s per step) would outlive the check's deadline.
const precheckRetryPause = 500 * time.Millisecond

// precheckJudgeFromEnv builds the judge that checks drafts before delivery
// (AGENT_RUNTIME_PRECHECK=log|block). Unlike judgeFromEnv it needs no
// Langfuse keys and no AGENT_JUDGE_ENABLED: a check must not switch off with
// the scoring project. The model is AGENT_PRECHECK_LLM_* when set, else the
// scoring judge's AGENT_JUDGE_LLM_*: a key of its own keeps the check's
// synchronous calls from eating the rate limit the agent itself runs on.
// nil when the flag is off; ValidateFlagsFromEnv refuses to start when the
// flag is on and no model is configured.
func precheckJudgeFromEnv(basePath string, flags runtimeFlags) TurnJudge {
	if flags.Precheck == precheckOff {
		return nil
	}
	llm := precheckLLMFromEnv()
	if llm == nil {
		log.Warn().Str("mode", flags.Precheck).Msg("agentruntime: AGENT_RUNTIME_PRECHECK on but neither AGENT_PRECHECK_LLM_* nor AGENT_JUDGE_LLM_* is complete; precheck off")
		return nil
	}
	log.Info().Str("mode", flags.Precheck).Str("synthetic", flags.PrecheckSynthetic).Str("model", llm.Model).Msg("agentruntime: precheck on")
	return agentjudge.New(basePath, llm, nil)
}

// precheckLLMFromEnv is the check model: the complete AGENT_PRECHECK_LLM_*
// triple, else the complete AGENT_JUDGE_LLM_* triple, else nil.
func precheckLLMFromEnv() *agentjudge.OpenAIChat {
	for _, prefix := range []string{"AGENT_PRECHECK_LLM_", "AGENT_JUDGE_LLM_"} {
		url, key, model := strings.TrimSpace(os.Getenv(prefix+"URL")), strings.TrimSpace(os.Getenv(prefix+"KEY")), strings.TrimSpace(os.Getenv(prefix+"MODEL"))
		if url != "" && key != "" && model != "" {
			return &agentjudge.OpenAIChat{BaseURL: url, APIKey: key, Model: model, RetryPause: precheckRetryPause}
		}
	}
	return nil
}

// judgeTurn hands the delivered turn to the judge. Under
// AGENT_RUNTIME_PRECHECK=log it is replaced by one goroutine (at most
// precheckLogParallel at a time) that checks the
// delivered reply and then submits it with the check's record, so the turn
// is scored once and the client waits for nothing. pc is the block-mode
// record of this turn (nil otherwise).
func (r *Runtime) judgeTurn(ctx context.Context, conv Conversation, run AgentRunRequest, pending, history []Message, reply string, parts []string, traced A2AReply, pc *precheckTurn) {
	if r.precheckMode(conv) == precheckLog && r.ext.precheck != nil {
		t := r.judgeInput(conv, run, pending, history, reply, parts, traced)
		r.goPrecheckLogged(conv, t, traced.TraceID != "", precheckLog)
		return
	}
	if r.judge == nil || traced.TraceID == "" {
		return
	}
	t := r.judgeInput(conv, run, pending, history, reply, parts, traced)
	if pc != nil && !pc.start.IsZero() {
		t.Precheck, t.PrecheckVerdicts, t.PrecheckReply = pc.record(), pc.verdicts, pc.reply
	}
	r.judge.Submit(conv.AgentName, t)
}

// judgeInput packs a turn for the judge: the client messages of this turn,
// the earlier dialog, the state the agent had, the flags that change what
// counts as a violation, and the reply as it was cut for delivery. The
// kb_search results and the names of the procedures loaded for the turn
// (active_skills, so a criterion can tell an objection answered without its
// procedure) ride along only while a check is on, so with
// AGENT_RUNTIME_PRECHECK=off the judge gets exactly the turn it got before.
func (r *Runtime) judgeInput(conv Conversation, run AgentRunRequest, pending, history []Message, reply string, parts []string, traced A2AReply) agentjudge.Turn {
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
		if pendingIDs[m.ID.String()] || m.Role == "system" || strings.TrimSpace(m.Content) == "" {
			continue
		}
		role := "roman"
		if m.Role == "user" {
			role = "client"
		}
		t.History = append(t.History, agentjudge.Exchange{Role: role, Text: m.Content})
	}
	state := run.ConversationContext.State
	fields := map[string]any{"reported_facts": state.ReportedFacts, "open_loops": state.OpenLoops,
		"no_question_this_turn": t.NoQuestionThisTurn, "reply_error": t.ReplyError, "username": conv.ActorUsername}
	if r.flags.Precheck != precheckOff {
		fields["active_skills"] = sortedKeys(state.ActiveSkills)
	}
	ctxJSON, _ := json.Marshal(fields)
	t.Context = string(ctxJSON)
	if r.flags.Precheck != precheckOff {
		t.KB = traced.KB
	}
	return t
}
