package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// RuntimeLinkMeta mirrors tggateway.RuntimeLinkMeta: one URL found in a
// message plus its best-effort page title.
type RuntimeLinkMeta struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

// RuntimeAttachment mirrors tggateway.RuntimeAttachment across the
// contract: media metadata plus resolver outputs (transcript/description
// with availability flags).
type RuntimeAttachment struct {
	Kind                 string `json:"kind"`
	FileID               string `json:"file_id,omitempty"`
	FilePath             string `json:"file_path,omitempty"`
	MimeType             string `json:"mime_type,omitempty"`
	FileName             string `json:"file_name,omitempty"`
	DurationSec          int    `json:"duration_seconds,omitempty"`
	SizeBytes            int64  `json:"size_bytes,omitempty"`
	Transcript           string `json:"transcript,omitempty"`
	TranscriptAvailable  bool   `json:"transcript_available"`
	Description          string `json:"description,omitempty"`
	DescriptionAvailable bool   `json:"description_available"`
}

// InboundMessage is one message of a (possibly debounced) batch: each keeps
// its own channel identity and gets its own conversation_messages row, while
// the whole batch shares one agent run and one reply. Links carries the
// gateway-extracted URL entities (Agent Harness v2, Step 5): persisted into
// the row's entities column and rendered into the A2A context block.
// Attachment (Step 6) carries media: persisted to the attachments JSONB and
// rendered as a typed line (voice/image/document) in the context.
type InboundMessage struct {
	Content                 string
	ChannelMessageID        string
	ThreadID                string
	SourceSentAt            *time.Time
	ReplyToChannelMessageID string
	Links                   []RuntimeLinkMeta
	Attachment              *RuntimeAttachment
}

// MessageRequest is one inbound TURN from a channel gateway: one or more
// messages (a debounced batch), one agent invocation, one reply. Every
// message's channel identity is preserved on its own row; the fields the
// single-message shortcut used to carry (Content etc) are folded into
// Messages by the server layer.
type MessageRequest struct {
	AgentName  string
	Channel    string
	ExternalID string
	Actor      Actor
	Messages   []InboundMessage
	// DelaySeconds is the extra pause the gateway chose for this turn; it
	// travels into runtime_context as delay_s and changes nothing in the
	// runtime's own timing.
	DelaySeconds int
	OnProcessing func() // transport presence; called only after reply admission
}

// MessageResponse carries the agent's reply plus the reply anchor: the
// channel id of the LAST user message of the batch, so the gateway can send
// the answer as a native Telegram reply to the right message. Empty when
// the batch carried no channel ids (manual/system messages).
// Messages is the same turn cut into the messages a person would have sent
// (plan 3.1), newest gateway only: Text stays the whole turn glued with
// spaces, so a gateway that does not know about series keeps working and the
// transcript keeps one row per turn. Empty means "nothing to split" -- one
// message, exactly as before.
type MessageResponse struct {
	Text                    string
	Messages                []string
	ReplyToChannelMessageID string
	Suppressed              bool
}

type A2AClient interface {
	Send(ctx context.Context, run AgentRunRequest) (reply string, err error)
}

type DomainProvider interface {
	GetDomain(ctx context.Context, agentName, domain string) (content string, err error)
}

type Runtime struct {
	store            ConversationStore
	hooks            HookExecutor
	a2a              A2AClient
	domains          DomainProvider
	states           StateStore
	contextKey       []byte
	contacts         *ContactSync
	stateSync        *StateSync
	courtesyAgents   map[string]bool
	structuredAgents map[string]bool
	factSkills       map[string]string
	linkAllowlist    []string
	judge            TurnJudge
	syncPause        func(context.Context, Conversation) error
	// notifyOperator raises the operator card from inside the runtime (the
	// narrow mode's only way out of a silent turn). nil = no operator
	// configured, which logs instead of failing the turn.
	notifyOperator func(ctx context.Context, conv Conversation, text string) error
	outbound       func(ctx context.Context, agentName, externalID, text, mediaURL string) error
	runLocks       [256]sync.Mutex
	flags          runtimeFlags
	recoveryDelays []time.Duration
	recoveryMu     sync.Mutex
	recovering     map[uuid.UUID]bool
	ext            runtimeExt
}

func NewRuntime(store ConversationStore, hooks HookExecutor, a2a A2AClient, domains DomainProvider) *Runtime {
	states, _ := store.(StateStore)
	return &Runtime{
		states:         states,
		flags:          runtimeFlagsFromEnv(),
		store:          store,
		hooks:          hooks,
		a2a:            a2a,
		domains:        domains,
		recoveryDelays: defaultRecoveryDelays,
		recovering:     map[uuid.UUID]bool{},
		ext:            runtimeExt{logSlots: make(chan struct{}, precheckLogParallel)},
	}
}

// finishAcknowledgement is the only reply the platform itself ever authors on
// the message path; the agent is not consulted, since its memory is exactly
// what /finish discards.
const finishAcknowledgement = "Готово. Я забыл этот диалог целиком — следующее сообщение начнёт разговор с нуля."

const finishAuditNote = "conversation finished by user command"

func (r *Runtime) ProcessMessage(ctx context.Context, req MessageRequest) (MessageResponse, error) {
	if len(req.Messages) == 0 || req.AgentName == "" || req.Channel == "" || req.ExternalID == "" {
		return MessageResponse{}, fmt.Errorf("agent, channel, identity and messages are required")
	}
	if r.states == nil || len(r.contextKey) < 32 {
		return MessageResponse{}, fmt.Errorf("runtime state or context signing is not configured")
	}
	conv, created, err := r.store.GetOrCreateConversation(ctx, req.AgentName, req.Channel, req.ExternalID, req.Actor)
	if err != nil {
		return MessageResponse{}, fmt.Errorf("get conversation: %w", err)
	}
	// The service is deployed as a single replica; serialize concurrent requests
	// for the same conversation while tools mutate state through independent calls.
	lock := &r.runLocks[conv.ID[0]]
	lock.Lock()
	defer lock.Unlock()
	state, err := r.states.GetState(ctx, conv.ID)
	if err != nil {
		return MessageResponse{}, err
	}
	var fresh []Message
	anchor := ""
	for _, m := range req.Messages {
		anchor = m.ChannelMessageID
		if m.ChannelMessageID != "" {
			_, err := r.store.FindMessageByChannelID(ctx, conv.ID, m.ChannelMessageID)
			if err == nil {
				continue
			}
			if !errors.Is(err, ErrMessageNotFound) {
				return MessageResponse{}, err
			}
		}
		saved, err := r.store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "user", Content: m.Content,
			ChannelMessageID: m.ChannelMessageID, ThreadID: m.ThreadID, SourceSentAt: m.SourceSentAt,
			ReplyToChannelMessageID: m.ReplyToChannelMessageID, Entities: linksToEntities(m.Links), Attachments: attachmentToEntity(m.Attachment)})
		if err != nil {
			return MessageResponse{}, err
		}
		fresh = append(fresh, saved)
	}
	// The reset is checked ahead of the pause gate on purpose: a paused
	// conversation must still be resettable by its own user, otherwise the only
	// way out of a pause is an operator with database access.
	if finishCommand(req.Messages) {
		if _, err := r.store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "system", Content: finishAuditNote}); err != nil {
			return MessageResponse{}, err
		}
		if err := r.store.FinishConversation(ctx, conv.ID); err != nil {
			return MessageResponse{}, fmt.Errorf("finish conversation: %w", err)
		}
		return MessageResponse{Text: finishAcknowledgement, ReplyToChannelMessageID: anchor}, nil
	}
	if r.contacts != nil {
		if err := r.contacts.Ensure(ctx, conv); err != nil {
			return MessageResponse{}, err
		}
	}
	if state.AgentEnabled && r.courtesyAgents[conv.AgentName] && explicitStop(req.Messages) {
		paused, err := r.states.PauseAgent(ctx, conv.ID, "customer requested no further replies")
		if err != nil {
			return MessageResponse{}, err
		}
		r.mirrorState(ctx, conv, paused, "")
		if r.syncPause != nil {
			_ = r.syncPause(ctx, conv)
		}
		return MessageResponse{Suppressed: true}, nil
	}
	if !state.AgentEnabled {
		r.maybeAckEscalationSilence(ctx, conv, state)
		return MessageResponse{Suppressed: true}, nil
	}
	if created {
		if err := r.hooks.Execute(ctx, "conversation.created", conv, nil); err != nil {
			return r.pauseAfterHookFailure(ctx, conv, "conversation.created", err)
		}
	}
	for _, m := range fresh {
		if err := r.hooks.Execute(ctx, "message.received", conv, m.Content); err != nil {
			return r.pauseAfterHookFailure(ctx, conv, "message.received", err)
		}
		if err := r.store.ClearIdleFlag(ctx, conv.ID); err != nil {
			return MessageResponse{}, fmt.Errorf("clear idle flag: %w", err)
		}
	}
	conv, err = r.store.GetConversation(ctx, conv.ID)
	if err != nil {
		return MessageResponse{}, err
	}
	state, err = r.states.GetState(ctx, conv.ID)
	if err != nil {
		return MessageResponse{}, err
	}
	if !state.AgentEnabled {
		return MessageResponse{Suppressed: true}, nil
	}
	inbox, ok := r.store.(interface {
		PendingRuntimeMessages(context.Context, uuid.UUID) ([]Message, error)
	})
	if !ok {
		return MessageResponse{}, fmt.Errorf("runtime pending input storage is not configured")
	}
	pending, err := inbox.PendingRuntimeMessages(ctx, conv.ID)
	if err != nil {
		return MessageResponse{}, err
	}
	if len(pending) == 0 {
		return MessageResponse{Suppressed: true}, nil
	}
	history, err := r.store.GetRecentMessages(ctx, conv.ID, 30)
	if err != nil {
		return MessageResponse{}, err
	}
	if r.courtesyAgents[conv.AgentName] && courtesyOnly(pending, history, state) {
		receipts, ok := r.store.(interface {
			MarkRuntimeHandled(context.Context, []Message) error
		})
		if !ok {
			return MessageResponse{}, fmt.Errorf("runtime receipt storage is not configured")
		}
		if err := receipts.MarkRuntimeHandled(ctx, pending); err != nil {
			return MessageResponse{}, err
		}
		if err := r.store.Touch(ctx, conv.ID); err != nil {
			return MessageResponse{}, err
		}
		return MessageResponse{Suppressed: true}, nil
	}
	if resp, handled, err := r.narrowStage(ctx, conv, state, pending); handled || err != nil {
		return resp, err
	}
	resp, err := r.runTurn(ctx, conv, state, pending, turnOptions{onProcessing: req.OnProcessing, delaySeconds: req.DelaySeconds})
	if err != nil {
		var failure *turnFailure
		if errors.As(err, &failure) {
			r.scheduleRecovery(conv, err)
		}
		return MessageResponse{}, err
	}
	resp.ReplyToChannelMessageID = anchor
	return resp, nil
}

// runTurn is the agent invocation shared by the inbound path and turn
// recovery: skills, one a2a call with a bounded repair, persistence, the
// completion hook and the runtime receipts. Agent-side failures (a2a errors,
// a reply held back twice, a broken reply contract) come back wrapped in
// turnFailure so the caller can tell them from hook failures, which pause
// the conversation and must not be replayed.
// turnOptions carries what the caller knows about this particular turn and
// the runTurn body does not: the transport presence callback and the
// gateway's chosen pause. Recovery passes the zero value.
type turnOptions struct {
	onProcessing func()
	delaySeconds int
}

func (r *Runtime) runTurn(ctx context.Context, conv Conversation, state RuntimeState, pending []Message, opts turnOptions) (MessageResponse, error) {
	var skills []string
	var err error
	if catalog, ok := r.domains.(DomainCatalog); ok {
		skills, err = catalog.ListDomains(ctx, conv.AgentName)
		if err != nil {
			return MessageResponse{}, err
		}
	}
	state, err = r.ensureFactSkills(ctx, conv, state)
	if err != nil {
		return MessageResponse{}, err
	}
	state, err = r.refreshActiveSkills(ctx, conv, state)
	if err != nil {
		return MessageResponse{}, err
	}
	if !state.AgentEnabled {
		return MessageResponse{Suppressed: true}, nil
	}
	history, err := r.store.GetRecentMessages(ctx, conv.ID, 10)
	if err != nil {
		return MessageResponse{}, err
	}
	judgeHistory := history
	if r.flags.Precheck != precheckOff && r.ext.precheck != nil {
		if longer, err := r.store.GetRecentMessages(ctx, conv.ID, precheckHistory); err == nil {
			judgeHistory = longer
		}
	}
	run := AgentRunRequest{AgentName: conv.AgentName, ContextID: "runtime-" + conv.ID.String(), Messages: pending,
		EndUserKey: conv.Channel + ":" + conv.ExternalID,
		ConversationContext: AgentConversationContext{ConversationID: conv.ID.String(), Channel: conv.Channel,
			ExternalID: conv.ExternalID, Username: conv.ActorUsername, FirstName: actorFirstName(conv.ActorMetadata),
			State: state, AvailableSkills: skills, SeamlessHandoff: r.flags.SeamlessHandoff, ReplySplit: r.flags.SplitReply && !r.structuredAgents[conv.AgentName]},
		ActorMetadata: conv.ActorMetadata}
	if r.flags.QuestionBudget {
		questions, phrases := turnCounters(conv)
		run.ConversationContext.NoQuestionThisTurn = questionBudgetSpent(questions, state)
		run.ConversationContext.UsedPhrases = phrases
	}
	if r.structuredAgents[conv.AgentName] {
		run.ConversationContext.ReplyFormat = structuredReplyFormat
	}
	run.ConversationContext.DelaySeconds = opts.delaySeconds
	if opts.onProcessing != nil {
		opts.onProcessing()
	}
	_, narrowAtEntry := narrowSince(conv)
	var pc *precheckTurn
	if r.flags.Precheck == precheckBlock && r.ext.precheck != nil {
		pc = newPrecheckTurn(precheckBlock, r.ext.precheckBudget)
	}
	var reply string
	var traced, earlier A2AReply
	var after RuntimeState
	for attempt := 0; attempt < 2; attempt++ {
		traced, err = r.send(ctx, run)
		if err != nil {
			return MessageResponse{}, &turnFailure{err: fmt.Errorf("a2a send: %w", err)}
		}
		traced = withEarlierAttempts(earlier, traced)
		earlier = traced
		reply = traced.Text
		after, err = r.states.GetState(ctx, conv.ID)
		if err != nil {
			return MessageResponse{}, err
		}
		if !after.AgentEnabled {
			r.mirrorState(ctx, conv, after, "")
			return MessageResponse{Suppressed: true}, nil
		}
		if entered, err := r.narrowEnteredDuringTurn(ctx, conv, narrowAtEntry); err != nil {
			return MessageResponse{}, err
		} else if entered {
			return MessageResponse{Suppressed: true}, nil
		}
		if !r.structuredAgents[conv.AgentName] {
			reply = stripEmDash(reply)
			reason := leakReason(reply)
			if reason == "" {
				reason = linkLeakReason(reply, r.linkAllowlist)
			}
			if reason == "" {
				if soft := repeatedHookReason(reply, history); soft != "" && attempt == 0 {
					log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Str("reason", soft).Msg("agentruntime: reply sent back for a rewrite")
					run.ConversationContext.State = after
					run.ConversationContext.ReplyError = r.precheckWithSoftHint(ctx, conv, run, pending, judgeHistory, reply, traced, pc, repeatRepairHint)
					continue
				}
				if soft := languageMismatchReason(reply, pending); soft != "" && attempt == 0 {
					log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Str("reason", soft).Msg("agentruntime: reply sent back for a rewrite")
					run.ConversationContext.State = after
					run.ConversationContext.ReplyError = r.precheckWithSoftHint(ctx, conv, run, pending, judgeHistory, reply, traced, pc, languageRepairHint)
					continue
				}
				if register := clientRegister(history, pending); attempt == 0 {
					if soft := registerMismatchReason(reply, register); soft != "" {
						log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Str("reason", soft).Msg("agentruntime: reply sent back for a rewrite")
						run.ConversationContext.State = after
						run.ConversationContext.ReplyError = r.precheckWithSoftHint(ctx, conv, run, pending, judgeHistory, reply, traced, pc, registerRepairHint(reply, register))
						continue
					}
				}
				if soft := ackLimitReason(reply, pending, history, after, r.flags.AckLimit); soft != "" && attempt == 0 {
					log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Str("reason", soft).Msg("agentruntime: reply sent back for a rewrite")
					run.ConversationContext.State = after
					run.ConversationContext.ReplyError = r.precheckWithSoftHint(ctx, conv, run, pending, judgeHistory, reply, traced, pc, ackRepairHint(r.flags.AckLimit))
					continue
				}
				if soft := funnelOrderReason(reply, after); r.flags.FunnelOrder && soft != "" && attempt == 0 {
					log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Str("reason", soft).Msg("agentruntime: reply sent back for a rewrite")
					run.ConversationContext.State = after
					run.ConversationContext.ReplyError = r.precheckWithSoftHint(ctx, conv, run, pending, judgeHistory, reply, traced, pc, funnelOrderRepairHint)
					continue
				}
				if soft := questionBudgetReason(run.ConversationContext.NoQuestionThisTurn, reply); soft != "" {
					if attempt == 0 {
						log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Str("reason", soft).Msg("agentruntime: reply sent back for a rewrite")
						run.ConversationContext.State = after
						run.ConversationContext.ReplyError = r.precheckWithSoftHint(ctx, conv, run, pending, judgeHistory, reply, traced, pc, questionBudgetRepairHint)
						continue
					}
					log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Str("reason", soft).Msg("agentruntime: rewrite still asks, delivering as is")
				}
				if pc != nil && (!isSilenceReply(reply) || attempt == 1) {
					step, resp, perr := r.precheckStep(ctx, conv, &run, after, pending, judgeHistory, &reply, traced, attempt, pc)
					if perr != nil {
						return MessageResponse{}, &turnFailure{err: perr}
					}
					if step == precheckStepRedo {
						continue
					} else if step == precheckStepDone {
						return resp, nil
					}
				}
				break
			}
			log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Int("attempt", attempt).
				Str("reason", reason).Int("runes", len([]rune(reply))).Msg("agentruntime: reply held back as internal monologue")
			if attempt == 1 {
				return MessageResponse{}, &turnFailure{err: fmt.Errorf("agent reply leaked internal reasoning twice: %s", reason)}
			}
			run.ConversationContext.State = after
			run.ConversationContext.ReplyError = leakRepairMessage(reason)
			continue
		}
		rendered, contractErr := renderReplyPlan(reply, after)
		if contractErr == nil {
			reply = rendered
			if pc != nil && (!isSilenceReply(reply) || attempt == 1) {
				step, resp, perr := r.precheckStep(ctx, conv, &run, after, pending, judgeHistory, &reply, traced, attempt, pc)
				if perr != nil {
					return MessageResponse{}, &turnFailure{err: perr}
				}
				if step == precheckStepRedo {
					continue
				} else if step == precheckStepDone {
					return resp, nil
				}
			}
			break
		}
		if attempt == 1 {
			return MessageResponse{}, &turnFailure{err: contractErr}
		}
		// One bounded protocol repair, same agent/model/context. Never deliver the
		// invalid draft, never restart lifecycle hooks or contact creation.
		run.ConversationContext.State = after
		run.ConversationContext.ReplyError = contractErr.Error()
	}
	// A turn that produced no text is not an error anywhere today: the
	// runtime saves an empty assistant message, the gateway sends nothing,
	// and the customer sees silence with no trace of why (QA 2026-09-15).
	// Under AGENT_RUNTIME_SILENCE_RECOVERY it becomes a turnFailure, which
	// leaves the input pending and puts the turn on the existing recovery
	// ladder (30s, 90s, 240s) rather than building a second watchdog.
	if r.flags.SilenceRecovery && isSilenceReply(reply) && !isDeliberateSkip(reply) {
		return MessageResponse{}, &turnFailure{err: errors.New("agent turn produced no message")}
	}
	// Cutting the turn into messages happens here and nowhere else: after
	// every guard above has seen the whole turn, before it is persisted. The
	// structured-reply branch (reply_contract.go) is out of scope by
	// decision, so it never splits.
	reply, parts := r.splitForDelivery(conv, reply)
	if _, err := r.store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "assistant", Content: reply}); err != nil {
		return MessageResponse{}, err
	}
	if err := r.hooks.Execute(ctx, "agent.run.completed", conv, nil); err != nil {
		return r.pauseAfterHookFailure(ctx, conv, "agent.run.completed", err)
	}
	if err := r.store.Touch(ctx, conv.ID); err != nil {
		return MessageResponse{}, err
	}
	if receipts, ok := r.store.(interface {
		MarkRuntimeHandled(context.Context, []Message) error
	}); ok {
		if err := receipts.MarkRuntimeHandled(ctx, pending); err != nil {
			return MessageResponse{}, err
		}
	} else {
		return MessageResponse{}, fmt.Errorf("runtime receipt storage is not configured")
	}
	r.recordTurnCounters(ctx, conv, reply)
	r.mirrorState(ctx, conv, after, "")
	if pc != nil {
		r.afterCheckedTurn(ctx, conv, pc)
	}
	if r.flags.Precheck != precheckOff {
		run.ConversationContext.State = after
	}
	r.judgeTurn(ctx, conv, run, pending, judgeHistory, reply, parts, traced, pc)
	return MessageResponse{Text: reply, Messages: parts}, nil
}

// send asks the agent for the turn, keeping the trace ids when the client can
// report them.
func (r *Runtime) send(ctx context.Context, run AgentRunRequest) (A2AReply, error) {
	if traced, ok := r.a2a.(TracedA2AClient); ok {
		return traced.SendTraced(ctx, run)
	}
	text, err := r.a2a.Send(ctx, run)
	return A2AReply{Text: text}, err
}

// linksToEntities converts gateway link metadata into the generic entity
// objects the entities JSONB column stores: {"url": ..., "title": ...}.
func linksToEntities(links []RuntimeLinkMeta) []any {
	if len(links) == 0 {
		return nil
	}
	out := make([]any, 0, len(links))
	for _, l := range links {
		if l.URL == "" {
			continue
		}
		e := map[string]any{"url": l.URL}
		if l.Title != "" {
			e["title"] = l.Title
		}
		out = append(out, e)
	}
	return out
}

// attachmentToEntity converts the attachment descriptor into the single
// object the attachments JSONB column stores (nil when no attachment).
func attachmentToEntity(a *RuntimeAttachment) []any {
	if a == nil {
		return nil
	}
	obj := map[string]any{"kind": a.Kind}
	if a.FileID != "" {
		obj["file_id"] = a.FileID
	}
	if a.FilePath != "" {
		obj["file_path"] = a.FilePath
	}
	if a.MimeType != "" {
		obj["mime_type"] = a.MimeType
	}
	if a.FileName != "" {
		obj["file_name"] = a.FileName
	}
	if a.DurationSec > 0 {
		obj["duration_seconds"] = a.DurationSec
	}
	if a.SizeBytes > 0 {
		obj["size_bytes"] = a.SizeBytes
	}
	if a.TranscriptAvailable {
		obj["transcript"] = a.Transcript
	}
	if a.DescriptionAvailable {
		obj["description"] = a.Description
	}
	return []any{obj}
}

// An external hook can have an unknown outcome after a timeout. Pause rather
// than replay a potentially non-idempotent action or bypass it on the next turn.
// Recovery is explicit; the simple CRM status sync is retried by the service.
func (r *Runtime) pauseAfterHookFailure(ctx context.Context, conv Conversation, event string, cause error) (MessageResponse, error) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := r.states.PauseAgent(persistCtx, conv.ID, "integration hook failed: "+event); err != nil {
		return MessageResponse{}, fmt.Errorf("hook failed and pause unavailable: %w", err)
	}
	return MessageResponse{}, cause
}

const escalationSilenceAck = "Коллега уже в курсе, ответит здесь."

// escalationSilenceAckSeamless is the same receipt without the hand-off: the
// customer still learns their message arrived, but nobody is announced. Used
// when AGENT_RUNTIME_SEAMLESS_HANDOFF is on (plan 4.2), where the curator
// continues in the same chat under the same name and "a colleague will
// answer here" would name a second person the customer never sees.
const escalationSilenceAckSeamless = "Принято, зафиксировал."

// silenceAckLine picks the receipt for the current flag state.
func (r *Runtime) silenceAckLine() string {
	if r.flags.SeamlessHandoff {
		return escalationSilenceAckSeamless
	}
	return escalationSilenceAck
}

var errOutboundNotConfigured = errors.New("agentruntime: outbound channel not configured")

// A conversation paused by a genuine escalation (PauseReason "escalated:
// ...") still silently persists any further inbound message while paused,
// since the early AgentEnabled gate above never calls the model again. Left
// alone the customer gets no signal their message arrived at all. This
// sends a fixed, non-generated line once per pause (claimed through
// ClaimEscalationAck, the same atomic-metadata pattern idle.go uses for
// idle_fired_at) through the same outbound path tellClient uses for the
// original hand-off. Courtesy-stop ("customer requested no further
// replies") and hook-failure pauses are intentionally excluded: the former
// is the customer's own request for silence, the latter is a platform
// fault, not a human already engaged.
func (r *Runtime) maybeAckEscalationSilence(ctx context.Context, conv Conversation, state RuntimeState) {
	if !strings.HasPrefix(state.PauseReason, "escalated:") {
		return
	}
	claimed, err := r.store.ClaimEscalationAck(ctx, conv.ID)
	if err != nil {
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: escalation ack claim failed")
		return
	}
	if !claimed {
		return
	}
	ack := r.silenceAckLine()
	if _, err := r.store.SaveMessage(ctx, conv.ID, SaveMessageInput{Role: "assistant", Content: ack}); err != nil {
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: escalation ack not saved")
		return
	}
	if r.outbound == nil {
		log.Info().Str("conversation", conv.ID.String()).Msg("agentruntime: escalation ack persisted but no outbound configured")
		return
	}
	if err := r.outbound(ctx, conv.AgentName, conv.ExternalID, ack, ""); err != nil {
		if errors.Is(err, errOutboundNotConfigured) {
			log.Info().Str("conversation", conv.ID.String()).Msg("agentruntime: escalation ack persisted but no outbound configured")
			return
		}
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: escalation ack delivery failed")
	}
}
