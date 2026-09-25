package agentruntime

import (
	"context"
	"time"
)

// runtimeExt holds the dependencies of the pre-delivery check (plan
// 2026-09-25): the judge that checks a draft before delivery and the
// runtime-side hand-off. Every field may be nil; each consumer treats that
// as "off".
type runtimeExt struct {
	// precheck is the judge Check runs on. It is built apart from the
	// scoring judge: a check must not depend on the Langfuse keys or the
	// unit budget.
	precheck TurnJudge
	// precheckBudget is the deadline of one turn's check path (0 = the
	// 30 s default).
	precheckBudget time.Duration
	handoff        func(ctx context.Context, conv Conversation, req HandoffRequest) (HandoffResult, error)
	// logSlots bounds the background Checks of log mode (and of follow-ups
	// under block); nil runs them unbounded (a Runtime not built by
	// NewRuntime).
	logSlots chan struct{}
}

// HandoffRequest is a hand-off the runtime decides on by itself (a check
// verdict, the refusal threshold): the escalation code, the operator summary,
// the line the client gets, and whether the pause must bypass the narrow
// mode. NoClientLine skips the client line (the turn's draft already is the
// reply); NoCard skips the operator card (the model's own call with the same
// code already sent it).
type HandoffRequest struct {
	Code, Summary, ClientLine string
	ForcePause                bool
	NoClientLine, NoCard      bool
}
