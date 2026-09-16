package agentruntime

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/turnbudget"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// A turn that fails on the agent side (rate limit exhausted after the a2a
// retries, kagent session timeout, the 150s call deadline, a reply held back
// twice) used to end as an error to the gateway, which suppresses channel
// output because it cannot tell a failed turn from a paused conversation.
// The customer's message stays pending in the inbox and is only answered
// when the NEXT message arrives -- from the customer's side the bot went
// silent (QA 2026-09-15, 5 of 28 turns). Recovery replays the pending input
// from inside the runtime on a fixed schedule and delivers the reply through
// the same /outbound path idle follow-ups and escalation lines use. A fresh
// inbound message during the wait takes the pending input with it, so a
// recovery attempt that finds nothing pending simply stops.
var defaultRecoveryDelays = []time.Duration{30 * time.Second, 90 * time.Second, 240 * time.Second}

// turnFailure marks errors that come from the agent call itself, as opposed
// to hook failures (which pause the conversation) or storage errors.
type turnFailure struct{ err error }

func (f *turnFailure) Error() string { return f.err.Error() }
func (f *turnFailure) Unwrap() error { return f.err }

func (r *Runtime) scheduleRecovery(conv Conversation, cause error) {
	if len(r.recoveryDelays) == 0 || r.recovering == nil {
		return
	}
	r.recoveryMu.Lock()
	if r.recovering[conv.ID] {
		r.recoveryMu.Unlock()
		return
	}
	r.recovering[conv.ID] = true
	r.recoveryMu.Unlock()
	log.Warn().Err(cause).Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).
		Dur("first_retry", r.recoveryDelays[0]).Msg("agentruntime: turn failed; recovery scheduled")
	go r.runRecovery(conv.ID)
}

func (r *Runtime) runRecovery(convID uuid.UUID) {
	defer func() {
		r.recoveryMu.Lock()
		delete(r.recovering, convID)
		r.recoveryMu.Unlock()
	}()
	for i, delay := range r.recoveryDelays {
		time.Sleep(delay)
		done, err := r.recoverTurn(convID, i+1)
		if done {
			if err != nil {
				log.Warn().Err(err).Str("conversation", convID.String()).Int("attempt", i+1).Msg("agentruntime: turn recovery stopped")
			}
			return
		}
		log.Warn().Err(err).Str("conversation", convID.String()).Int("attempt", i+1).Msg("agentruntime: turn recovery attempt failed")
	}
	log.Error().Str("conversation", convID.String()).Int("attempts", len(r.recoveryDelays)).
		Msg("agentruntime: turn recovery exhausted; input stays pending until the next inbound message")
}

// recoverTurn replays the pending inbox once. done=true means there is
// nothing left to retry: the reply went out, the input was already handled
// by a newer inbound turn, the conversation got paused, or the failure is
// not an agent-side one.
func (r *Runtime) recoverTurn(convID uuid.UUID, attempt int) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), turnbudget.RuntimeTurn())
	defer cancel()
	lock := &r.runLocks[convID[0]]
	lock.Lock()
	defer lock.Unlock()
	conv, err := r.store.GetConversation(ctx, convID)
	if err != nil {
		return false, err
	}
	state, err := r.states.GetState(ctx, convID)
	if err != nil {
		return false, err
	}
	if !state.AgentEnabled {
		return true, nil
	}
	inbox, ok := r.store.(interface {
		PendingRuntimeMessages(context.Context, uuid.UUID) ([]Message, error)
	})
	if !ok {
		return true, errors.New("runtime pending input storage is not configured")
	}
	pending, err := inbox.PendingRuntimeMessages(ctx, convID)
	if err != nil {
		return false, err
	}
	if len(pending) == 0 {
		return true, nil
	}
	// A recovery replay goes through the same narrow-mode gate an inbound
	// turn does: while a curator owns the chat, a retry must not become the
	// one path that speaks past the white list.
	if _, handled, gateErr := r.narrowStage(ctx, conv, state, pending); handled || gateErr != nil {
		return true, gateErr
	}
	resp, err := r.runTurn(ctx, conv, state, pending, turnOptions{})
	if err != nil {
		var failure *turnFailure
		return !errors.As(err, &failure), err
	}
	if resp.Suppressed || isSilenceReply(resp.Text) {
		return true, nil
	}
	log.Info().Str("conversation", convID.String()).Int("attempt", attempt).Int("pending", len(pending)).
		Msg("agentruntime: turn recovered")
	if r.outbound == nil {
		log.Info().Str("conversation", convID.String()).Msg("agentruntime: recovered reply persisted but no outbound configured")
		return true, nil
	}
	if err := r.outbound(ctx, conv.AgentName, conv.ExternalID, resp.Text, ""); err != nil {
		log.Warn().Err(err).Str("conversation", convID.String()).Msg("agentruntime: recovered reply delivery failed (reply persisted)")
	}
	return true, nil
}

func isSilenceReply(text string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(text), ".!…")
	return trimmed == "" || strings.EqualFold(strings.TrimSpace(trimmed), "SKIP")
}
