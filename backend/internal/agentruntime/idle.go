package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// idleScanIntervalDefault is the scheduler tick used when
// AGENT_RUNTIME_IDLE_TICK_SECONDS is unset or unparseable. A tick <= 0 is not
// a 60s tick: it means "do not start the scheduler", and the gate for that
// lives in IdleEnabled / Server.StartIdleScheduler, not here. NewIdleScheduler
// still clamps a non-positive interval because a time.Ticker panics on one,
// and a scheduler that was constructed anyway must not take the process down.
const idleScanIntervalDefault = 60 * time.Second

// IdleEnabled decides whether the proactive follow-up scheduler runs at all.
//
// enabled is AGENT_RUNTIME_IDLE_ENABLED. Unset (the default) keeps today's
// behaviour exactly: the tick alone decides, and a tick <= 0 disables. Set, it
// wins in both directions, so the rollback switch works without touching the
// tick: "0"/"false"/"off"/"no" force the scheduler off, "1"/"true"/"on"/"yes"
// force it on (with the default tick when the tick is not a positive number).
func IdleEnabled(enabled string, tickSeconds int) bool {
	switch strings.ToLower(strings.TrimSpace(enabled)) {
	case "0", "false", "off", "no":
		return false
	case "1", "true", "on", "yes":
		return true
	}
	return tickSeconds > 0
}

// idleHookRow is one conversation.idle hook joined with its due
// conversation. ConversationID identifies what to invoke; AgentName and
// ChatExternalID route the delivery; HookMessage is the harness instruction
// for the follow-up content.
type idleHookRow struct {
	HookID         string
	AgentName      string
	ConversationID string
	ChatExternalID string
	ActorUsername  string
	IdleMinutes    int
	HookMessage    string
	Step           int
}

// IdleScheduler runs proactive agent invocations for conversations that have
// been quiet past a lifecycle hook's idle threshold. Detection and scheduling
// are deterministic platform work (this file); the follow-up CONTENT is the
// agent's job, invoked with an explicit reason rather than a fake user turn.
//
// A hook is a ladder: trigger_config.ladder_minutes lists how long the
// conversation must stay quiet before each step fires, measured from the last
// activity (the customer's message for step 0, our previous follow-up for the
// rest). conversations.metadata.idle_step counts the steps already sent in
// the current silence; the scheduler claims a step by incrementing the
// counter before invoking, so concurrent ticks cannot double-fire, and the
// next inbound user message resets it (ProcessMessage), re-arming the ladder
// from the top. A hook without ladder_minutes is a one-step ladder of
// idle_minutes, which is the pre-ladder behaviour.
type IdleScheduler struct {
	pool     *pgxpool.Pool
	runtime  *Runtime
	a2a      A2AClient
	outbound ChannelOutbound
	interval time.Duration
	now      func() time.Time
}

// ChannelOutbound delivers a finished agent reply to the channel the
// conversation lives in. tg-gateway exposes the real implementation; a nil
// outbound means "persist but do not deliver" (logs say so) -- a delivery
// failure is never data loss because the reply is already persisted.
type ChannelOutbound interface {
	SendOutbound(ctx context.Context, agentName, chatExternalID, text, replyToChannelMessageID string) error
}

func NewIdleScheduler(pool *pgxpool.Pool, runtime *Runtime, a2a A2AClient, outbound ChannelOutbound, interval time.Duration) *IdleScheduler {
	if interval <= 0 {
		interval = idleScanIntervalDefault
	}
	return &IdleScheduler{pool: pool, runtime: runtime, a2a: a2a, outbound: outbound, interval: interval, now: time.Now}
}

func (s *IdleScheduler) send(ctx context.Context, run AgentRunRequest) (A2AReply, error) {
	if traced, ok := s.a2a.(TracedA2AClient); ok {
		return traced.SendTraced(ctx, run)
	}
	text, err := s.a2a.Send(ctx, run)
	return A2AReply{Text: text}, err
}

// Run blocks ticking the scheduler until ctx is cancelled.
func (s *IdleScheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Tick(ctx); err != nil {
				log.Warn().Err(err).Msg("agentruntime: idle scheduler tick failed")
			}
		}
	}
}

// idleMaxMinutesDefault bounds how far back a hook reaches when it is first
// enabled: a conversation quiet longer than this is a lost lead, not a
// follow-up candidate. Overridable per hook via trigger_config.max_idle_minutes.
const idleMaxMinutesDefault = "1440"

// dueIdleHooks joins enabled conversation.idle hooks with conversations that
// still have a ladder step left and have been quiet past that step's wait.
// The reach bound (max_idle_minutes) applies to the first step only: a
// conversation that already received a follow-up stays on the ladder however
// long the customer keeps quiet. The ladder falls back to a single
// idle_minutes step when the hook has no ladder_minutes. Paused conversations
// (agent_enabled = false) are not candidates: a human owns them. With
// trigger_config.direct_only the hook skips Telegram groups and channels
// (negative external ids), which also keeps synthetic eval chats off the
// ladder: follow-ups are for a private dialogue with one lead.
const dueIdleHooksSQL = `
WITH candidates AS (
SELECT h.id::text AS hook_id, h.agent_name, c.id::text AS conversation_id, c.external_id, c.actor_username,
       COALESCE((h.trigger_config->>'idle_minutes')::int, 30) AS idle_minutes,
       COALESCE(h.action_config->>'agent_message', '') AS agent_message,
       COALESCE((c.metadata->>'idle_step')::int, 0) AS step,
       COALESCE(h.trigger_config->'ladder_minutes',
                jsonb_build_array(COALESCE((h.trigger_config->>'idle_minutes')::int, 30))) AS ladder,
       COALESCE((h.trigger_config->>'max_idle_minutes')::int, ` + idleMaxMinutesDefault + `) AS max_idle_minutes,
       c.updated_at
FROM lifecycle_hooks h
JOIN conversations c
  ON c.agent_name = h.agent_name
 AND c.status = 'active'
 AND (NOT COALESCE((h.trigger_config->>'direct_only')::bool, false) OR c.external_id NOT LIKE '-%')
LEFT JOIN conversation_runtime_state st ON st.conversation_id = c.id
WHERE h.trigger_event = 'conversation.idle'
  AND h.enabled = true
  AND h.action_type = 'schedule'
  AND COALESCE(st.agent_enabled, true)
)
SELECT hook_id, agent_name, conversation_id, external_id, actor_username,
       (ladder->>step)::int, agent_message, step
FROM candidates
WHERE jsonb_typeof(ladder) = 'array'
  AND step < jsonb_array_length(ladder)
  AND updated_at < NOW() - make_interval(mins => (ladder->>step)::int)
  AND (step > 0 OR updated_at > NOW() - make_interval(mins => max_idle_minutes))
LIMIT 50`

// nextIdleStepSQL is the ladder row of one conversation with the timers
// dropped: InvokeNow claims the next step on demand.
const nextIdleStepSQL = `
SELECT h.id::text, h.agent_name, c.id::text, c.external_id, c.actor_username,
       (COALESCE(h.trigger_config->'ladder_minutes',
                 jsonb_build_array(COALESCE((h.trigger_config->>'idle_minutes')::int, 30)))->>COALESCE((c.metadata->>'idle_step')::int, 0))::int,
       COALESCE(h.action_config->>'agent_message', ''),
       COALESCE((c.metadata->>'idle_step')::int, 0)
FROM lifecycle_hooks h
JOIN conversations c ON c.agent_name = h.agent_name AND c.id = $1
WHERE h.trigger_event = 'conversation.idle' AND h.enabled = true AND h.action_type = 'schedule'
ORDER BY h.created_at
LIMIT 1`

// claimIdle advances the conversation to the next ladder step. The WHERE
// clause re-checks the step so two concurrent ticks cannot both pass: exactly
// one UPDATE wins. updated_at moves to now so the next step waits its own
// interval from this follow-up.
const claimIdleSQL = `
UPDATE conversations SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object(
		'idle_fired_at', to_char(NOW() AT TIME ZONE 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		'idle_step', $2::int + 1),
	updated_at = NOW()
WHERE id = $1 AND COALESCE((metadata->>'idle_step')::int, 0) = $2`

// idleSendLocation is the customers' clock: the send windows below are hours
// in Moscow, where the lead's team and the audience live.
var idleSendLocation = mustLoadLocation("Europe/Moscow")

// idleQuietFromHour..idleQuietToHour is the night: no follow-up of any step
// goes out then; the step waits for the morning and fires on the first tick
// after quiet hours end.
const (
	idleQuietFromHour = 23
	idleQuietToHour   = 7
)

// idleWindowHours are the daytime slots the lead asked for («окна 7-8, 13:00
// и 19:00»): steps from idleWindowFromStep on fire only inside them, so the
// evening and daily nudges land when people read Telegram, not whenever the
// interval happens to expire. The first two steps (20 minutes, an hour) stay
// on their own timers: they follow up on a conversation that was live minutes
// ago.
var idleWindowHours = map[int]bool{7: true, 8: true, 13: true, 19: true}

const idleWindowFromStep = 2

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("MSK", 3*60*60)
	}
	return loc
}

// idleStepAllowedAt reports whether a ladder step may be sent at the given
// wall-clock moment.
func idleStepAllowedAt(step int, at time.Time) bool {
	hour := at.In(idleSendLocation).Hour()
	if hour >= idleQuietFromHour || hour < idleQuietToHour {
		return false
	}
	if step >= idleWindowFromStep && !idleWindowHours[hour] {
		return false
	}
	return true
}

// Tick runs one scheduler pass: find due conversations, skip those outside
// their send window, claim each remaining step, invoke the agent with the
// idle-reason envelope, persist the turn, deliver.
func (s *IdleScheduler) Tick(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, dueIdleHooksSQL)
	if err != nil {
		return fmt.Errorf("query due idle hooks: %w", err)
	}
	var due []idleHookRow
	for rows.Next() {
		var r idleHookRow
		if err := rows.Scan(&r.HookID, &r.AgentName, &r.ConversationID, &r.ChatExternalID, &r.ActorUsername, &r.IdleMinutes, &r.HookMessage, &r.Step); err != nil {
			rows.Close()
			return fmt.Errorf("scan idle hook row: %w", err)
		}
		due = append(due, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	now := s.now()
	for _, r := range due {
		if !idleStepAllowedAt(r.Step, now) {
			continue
		}
		tag, err := s.pool.Exec(ctx, claimIdleSQL, r.ConversationID, r.Step)
		if err != nil {
			log.Warn().Err(err).Str("conversation", r.ConversationID).Msg("agentruntime: idle claim failed")
			continue
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		s.invoke(ctx, r, true)
	}
	return nil
}

// InvokeNow fires the next ladder step of one conversation regardless of
// the timers and returns the follow-up without delivering it. Evals call it
// through POST /idle to see what the scheduler would send after a silence;
// the step is claimed exactly as on a tick, so the ladder state is real.
// Empty reply with nil error means the ladder is exhausted or the agent is
// paused, the same silence a tick would produce.
func (s *IdleScheduler) InvokeNow(ctx context.Context, conv Conversation) (string, error) {
	var r idleHookRow
	var minutes *int
	err := s.pool.QueryRow(ctx, nextIdleStepSQL, conv.ID).Scan(&r.HookID, &r.AgentName, &r.ConversationID, &r.ChatExternalID, &r.ActorUsername, &minutes, &r.HookMessage, &r.Step)
	if err != nil {
		return "", fmt.Errorf("idle hook for conversation: %w", err)
	}
	if minutes == nil {
		return "", nil
	}
	r.IdleMinutes = *minutes
	tag, err := s.pool.Exec(ctx, claimIdleSQL, r.ConversationID, r.Step)
	if err != nil {
		return "", fmt.Errorf("claim idle step: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", nil
	}
	return s.invoke(ctx, r, false), nil
}

// invocationEnvelope renders the outbound-run reason the agent receives.
// Owner's spec: the agent must see WHY it was invoked, not a fake user
// message. step is 1-based in the envelope: it is the number of the follow-up
// the agent is about to write, which is how the continuity skill counts its
// ladder.
func invocationEnvelope(r idleHookRow) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[invocation: cause=conversation_idle, idle=%dm, step=%d]\n", r.IdleMinutes, r.Step+1))
	msg := r.HookMessage
	if strings.TrimSpace(msg) == "" {
		msg = "Диалог давно без ответа. Составь короткое уместное follow-up сообщение клиенту: мягко вернись к его последнему вопросу и предложи продолжить."
	}
	sb.WriteString(msg)
	return sb.String()
}

// invoke persists the system turn, calls the agent, persists the reply, and
// hands it to the channel when deliver is set. Errors are logged
// per-conversation: one broken follow-up must not stop the rest of the pass.
// The persisted reply is returned, empty when nothing was produced.
func (s *IdleScheduler) invoke(ctx context.Context, r idleHookRow, deliver bool) string {
	convID := r.ConversationID
	conv, err := s.runtime.store.GetConversation(ctx, parseUUID(convID))
	if err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle invoke: load conversation")
		return ""
	}

	lock := &s.runtime.runLocks[conv.ID[0]]
	lock.Lock()
	defer lock.Unlock()
	if s.runtime.states == nil {
		log.Warn().Str("conversation", convID).Msg("agentruntime: idle state unavailable")
		return ""
	}
	state, err := s.runtime.states.GetState(ctx, conv.ID)
	if err != nil || !state.AgentEnabled {
		return ""
	}
	state, err = s.runtime.refreshActiveSkills(ctx, conv, state)
	if err != nil || !state.AgentEnabled {
		log.Warn().Err(err).Msg("agentruntime: idle active skill refresh unavailable")
		return ""
	}
	var skills []string
	if catalog, ok := s.runtime.domains.(DomainCatalog); ok {
		skills, err = catalog.ListDomains(ctx, conv.AgentName)
		if err != nil {
			return ""
		}
	}

	envelope := invocationEnvelope(r)
	if _, err := s.runtime.store.SaveMessage(ctx, conv.ID, SaveMessageInput{
		Role:    "system",
		Content: envelope,
	}); err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle invoke: save system message")
		return ""
	}

	history, err := s.runtime.store.GetRecentMessages(ctx, conv.ID, 20)
	if err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle invoke: history")
		return ""
	}

	run := AgentRunRequest{
		AgentName: conv.AgentName, ContextID: "runtime-" + conv.ID.String(), Messages: history,
		EndUserKey: conv.Channel + ":" + conv.ExternalID,
		ConversationContext: AgentConversationContext{ConversationID: conv.ID.String(),
			Channel: conv.Channel, ExternalID: conv.ExternalID, Username: conv.ActorUsername,
			FirstName: actorFirstName(conv.ActorMetadata), State: state, AvailableSkills: skills,
			SeamlessHandoff: s.runtime.flags.SeamlessHandoff},
		ActorMetadata: conv.ActorMetadata, Trigger: "idle",
	}
	register := clientRegister(history, nil)
	var pc *precheckTurn
	if s.runtime.flags.Precheck == precheckBlock && s.runtime.ext.precheck != nil {
		pc = newPrecheckTurn(precheckIdleBlock, s.runtime.ext.precheckBudget)
	}
	var reply string
	var traced, earlier A2AReply
	for attempt := 0; attempt < 2; attempt++ {
		traced, err = s.send(ctx, run)
		if err != nil {
			log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle invoke: a2a")
			return ""
		}
		traced = withEarlierAttempts(earlier, traced)
		earlier = traced
		state, err = s.runtime.states.GetState(ctx, conv.ID)
		if err != nil || !state.AgentEnabled {
			return ""
		}
		reply = strings.TrimSpace(stripEmDash(traced.Text))
		if reply == "" {
			log.Warn().Str("conversation", convID).Msg("agentruntime: idle follow-up empty, nothing sent")
			return ""
		}
		if reason := leakReason(reply); reason != "" {
			if attempt == 0 {
				log.Warn().Str("conversation", convID).Str("reason", reason).Msg("agentruntime: idle follow-up held back as internal text, sent back for a rewrite")
				run.ConversationContext.State = state
				run.ConversationContext.ReplyError = leakRepairMessage(reason)
				continue
			}
			log.Warn().Str("conversation", convID).Str("reason", reason).Msg("agentruntime: idle follow-up dropped as internal monologue")
			return ""
		}
		if pc != nil {
			if reason := linkLeakReason(reply, s.runtime.linkAllowlist); reason != "" {
				log.Warn().Str("conversation", convID).Str("reason", reason).Msg("agentruntime: idle follow-up dropped: link outside the allowlist")
				return ""
			}
		}
		if soft := registerMismatchReason(reply, register); soft != "" && attempt == 0 {
			log.Warn().Str("conversation", convID).Str("reason", soft).Msg("agentruntime: idle follow-up sent back for a rewrite")
			run.ConversationContext.State = state
			run.ConversationContext.ReplyError = registerRepairHint(reply, register)
			continue
		}
		if pc != nil {
			checked := run
			checked.ConversationContext.State = state
			ask, drop := s.runtime.precheckFollowUp(ctx, conv, s.runtime.judgeInput(conv, checked, nil, history, reply, splitReplyParts(reply), traced), attempt, pc)
			if drop {
				logPrecheck(conv, pc.record())
				log.Warn().Str("conversation", convID).Msg("agentruntime: idle follow-up dropped by the precheck after its rewrite")
				return ""
			}
			if ask != "" {
				run.ConversationContext.State = state
				run.ConversationContext.ReplyError = ask
				continue
			}
		}
		break
	}
	switch {
	case pc != nil:
		logPrecheck(conv, pc.record())
		s.runtime.judgeTurn(ctx, conv, run, nil, history, reply, splitReplyParts(reply), traced, pc)
	case s.runtime.flags.Precheck == precheckLog && s.runtime.ext.precheck != nil:
		s.runtime.goPrecheckLogged(conv, s.runtime.judgeInput(conv, run, nil, history, reply, splitReplyParts(reply), traced), traced.TraceID != "", precheckIdleLog)
	default:
		s.runtime.judgeTurn(ctx, conv, run, nil, history, reply, splitReplyParts(reply), traced, nil)
	}
	if _, err := s.runtime.store.SaveMessage(ctx, conv.ID, SaveMessageInput{
		Role:    "assistant",
		Content: reply,
	}); err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle invoke: save reply")
		return ""
	}

	if !deliver {
		return reply
	}
	if s.outbound == nil {
		log.Info().Str("conversation", convID).Msg("agentruntime: idle follow-up persisted but no outbound configured")
		return reply
	}
	state, err = s.runtime.states.GetState(ctx, conv.ID)
	if err != nil || !state.AgentEnabled {
		return ""
	}
	if err := s.outbound.SendOutbound(ctx, r.AgentName, r.ChatExternalID, reply, ""); err != nil {
		log.Warn().Err(err).Str("conversation", convID).Msg("agentruntime: idle follow-up delivery failed (reply persisted)")
	}
	return reply
}

func parseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}
