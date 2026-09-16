package agentruntime

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"
)

// Plan 3.4: two counters the model must not be able to write, because they
// exist to limit the model.
//
//   - questions_in_row: how many of the agent's own last turns ended with a
//     question. A live operator asks, gets no answer, and drops the slot for
//     a turn; the model asks again every time, which is the "script" tell.
//   - used_phrases: the last closing sentences, so a repeated closing is
//     visible to the prompt.
//
// Where they live: conversations.metadata, the runtime-owned JSONB that
// idle_fired_at and escalation_ack_sent_at already use. RuntimeState was the
// other candidate and was rejected -- its seven columns are the model's
// working state, StatePatch deliberately lets the model write two of them,
// and a counter the model can edit is not a budget. metadata needs no
// migration and has no path from the model at all.
const (
	questionsInRowKey = "questions_in_row"
	usedPhrasesKey    = "used_phrases"

	// questionBudgetLimit is the plan's number: from the second question in a
	// row on, the prompt is told to answer without asking.
	questionBudgetLimit = 2
	// usedPhrasesKept bounds the memory: enough to see a repetition, short
	// enough not to grow the envelope.
	usedPhrasesKept = 5
	// amountFactKey is the slot whose absence makes the budget unsafe: with
	// no amount recorded the dialogue has not reached its own question yet,
	// and muting the agent's question there stalls the lead (pre-mortem 2).
	amountFactKey = "amount"
)

// turnCounters reads the pair off the conversation, tolerating anything
// unexpected in the JSONB: a broken value must not change how the agent
// talks.
func turnCounters(conv Conversation) (questionsInRow int, usedPhrases []string) {
	switch v := conv.Metadata[questionsInRowKey].(type) {
	case float64:
		questionsInRow = int(v)
	case int:
		questionsInRow = v
	case int64:
		questionsInRow = int(v)
	}
	if questionsInRow < 0 {
		questionsInRow = 0
	}
	raw, _ := conv.Metadata[usedPhrasesKey].([]any)
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			usedPhrases = append(usedPhrases, s)
		}
	}
	return questionsInRow, usedPhrases
}

// endsWithQuestion reports whether the turn closes on a question, which is
// what the counter counts (a question in the middle of a turn is part of the
// explanation, not the hook).
func endsWithQuestion(text string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(text), `"'»)]`)
	return strings.HasSuffix(trimmed, "?") || strings.HasSuffix(trimmed, "？")
}

// closingSentence is the last sentence of the turn, normalised only by
// trimming: used_phrases is meant to be read by a human and by the prompt.
func closingSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.LastIndexAny(strings.TrimRight(text, ".!?？…"), ".!?？\n"); idx >= 0 {
		text = strings.TrimSpace(text[idx+1:])
	}
	return text
}

// nextCounters is the pure step: what the pair becomes after this reply.
func nextCounters(questionsInRow int, usedPhrases []string, reply string) (int, []string) {
	if endsWithQuestion(reply) {
		questionsInRow++
	} else {
		questionsInRow = 0
	}
	if phrase := closingSentence(reply); phrase != "" {
		usedPhrases = append(usedPhrases, phrase)
	}
	if len(usedPhrases) > usedPhrasesKept {
		usedPhrases = usedPhrases[len(usedPhrases)-usedPhrasesKept:]
	}
	return questionsInRow, usedPhrases
}

// questionBudgetSpent reports whether the prompt should be told to skip its
// question this turn: the agent has already asked twice running AND the
// amount slot is filled, so the dialogue has something to move on.
func questionBudgetSpent(questionsInRow int, state RuntimeState) bool {
	if questionsInRow < questionBudgetLimit {
		return false
	}
	_, known := state.ReportedFacts[amountFactKey]
	return known
}

// recordTurnCounters persists the pair after a delivered turn. It runs only
// with the budget on, so an off flag writes nothing at all and the metadata
// column stays exactly as it is today. A failure is logged, never fatal: a
// missed counter is a slightly more repetitive bot, a failed turn is silence.
func (r *Runtime) recordTurnCounters(ctx context.Context, conv Conversation, reply string) {
	if !r.flags.QuestionBudget {
		return
	}
	questions, phrases := turnCounters(conv)
	questions, phrases = nextCounters(questions, phrases, reply)
	if err := r.store.RecordTurnCounters(ctx, conv.ID, questions, phrases); err != nil {
		log.Warn().Err(err).Str("conversation", conv.ID.String()).Msg("agentruntime: turn counters not recorded")
	}
}
