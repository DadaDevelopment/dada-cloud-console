package agentruntime

import (
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
)

// Plan 3.1/3.2: a live operator writes a thought as two or three messages,
// the bot always writes one. The model marks the seams with a line holding
// nothing but "---", and the runtime cuts there.
//
// The cut happens AFTER every existing guard, on the glued turn, and the
// order of the guards themselves is untouched (runtime.go: stripEmDash first,
// then leakReason, linkLeakReason, repeatedHookReason, languageMismatchReason).
// That is deliberate: the semantic rules were written against a whole turn,
// and re-running them per message would both weaken them (a rule needs the
// context of the other parts) and multiply the repair budget, which stays one
// re-run per TURN.
//
// Per part only two cheap checks run, and both only warn: they describe a
// draft the prompt should not have produced, and holding a turn back over
// formatting would trade a visible bot tell for a silence.
const (
	// replySplitMaxParts is the ceiling from the plan: three messages is a
	// person writing, four is a feed.
	replySplitMaxParts = 3
	// replySplitPartRunes is the per-message length the form gate measures.
	replySplitPartRunes = 250
)

// replySplitSeparator matches a line that is nothing but three or more
// dashes. A dash inside a sentence, and an em dash the guard already
// rewrote, are not seams.
var replySplitSeparator = regexp.MustCompile(`(?m)^[ \t]*-{3,}[ \t]*\r?$`)

var replySplitLink = regexp.MustCompile(`(?i)https?://\S+|\b[a-z0-9-]+\.(?:ru|com|org|net|io|me)\b\S*`)

// splitReplyParts cuts the turn on separator lines and drops the empties. A
// turn with no separator comes back as one part; an all-separator turn comes
// back empty, and the caller keeps the original text.
func splitReplyParts(reply string) []string {
	out := make([]string, 0, replySplitMaxParts)
	for _, part := range replySplitSeparator.Split(reply, -1) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// capReplyParts folds everything past the ceiling into the last kept part, so
// an over-eager draft turns into a longer third message rather than a fourth
// one or a truncated turn: nothing the model wrote is ever dropped.
func capReplyParts(parts []string) []string {
	if len(parts) <= replySplitMaxParts {
		return parts
	}
	kept := append([]string{}, parts[:replySplitMaxParts]...)
	kept[replySplitMaxParts-1] = strings.Join(append([]string{kept[replySplitMaxParts-1]}, parts[replySplitMaxParts:]...), " ")
	return kept
}

// glueReplyParts is the single-message form of the turn: what the transcript
// keeps, what a gateway that knows nothing about series sends, and what the
// turn looks like when the flag is off.
func glueReplyParts(parts []string) string {
	return strings.Join(parts, " ")
}

// warnOnPartForm logs the two per-message rules. Warnings only, by design
// (see the file comment).
func warnOnPartForm(conversationID, agentName string, parts []string) {
	for i, part := range parts {
		if runes := len([]rune(part)); runes > replySplitPartRunes {
			log.Warn().Str("conversation", conversationID).Str("agent", agentName).
				Int("part", i+1).Int("runes", runes).Int("limit", replySplitPartRunes).
				Msg("agentruntime: reply part is longer than one message should be")
		}
		if loc := replySplitLink.FindStringIndex(part); loc != nil {
			bare := strings.TrimSpace(part[:loc[0]]) == "" && strings.TrimSpace(part[loc[1]:]) == ""
			if !bare {
				log.Warn().Str("conversation", conversationID).Str("agent", agentName).
					Int("part", i+1).Msg("agentruntime: reply part mixes a link with other text")
			}
		}
	}
}

// splitForDelivery is the one place that decides whether this turn is cut at
// all, so the decision exists once and a test can exercise the same code the
// turn path runs. With
// the flag off the reply is not touched at all -- not trimmed, not reglued,
// not stripped of a stray line of dashes -- because "the default is exactly
// today" has to mean the bytes, not just the shape.
//
// parts is what the gateway sends, nil when there is nothing to split; text
// is what goes to the transcript and to any consumer that only understands
// one message.
func (r *Runtime) splitForDelivery(conv Conversation, reply string) (text string, parts []string) {
	if !r.flags.SplitReply || r.structuredAgents[conv.AgentName] {
		return reply, nil
	}
	return r.splitTurn(conv.ID.String(), conv.AgentName, reply)
}

// splitTurn is the cut itself; splitForDelivery owns the "should we" part.
func (r *Runtime) splitTurn(conversationID, agentName, reply string) (text string, parts []string) {
	pieces := splitReplyParts(reply)
	if len(pieces) == 0 {
		// Nothing but separators: keep the model's own text rather than
		// silently turning the turn into a blank message.
		return reply, nil
	}
	pieces = capReplyParts(pieces)
	warnOnPartForm(conversationID, agentName, pieces)
	if len(pieces) == 1 {
		return pieces[0], nil
	}
	return glueReplyParts(pieces), pieces
}
