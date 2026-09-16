package agentruntime

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Plan 3.3: "done", "sent it", "registered" is a receipt, and a person
// answers a receipt with a receipt. The bot answers it with a paragraph,
// which is the second-loudest form tell after reply length in general.
//
// This is deliberately NOT an extension of courtesyOnly (reply_policy.go).
// That white list ("спасибо", "благодарю", "👍") makes the agent say nothing
// at all, and it already works; widening it would turn today's silences into
// text. The ten courtesy phrases are untouched here: the ack limit only sees
// the confirmations that reach the model today as a full turn.
var ackConfirmationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(всё |все )?готово$`),
	regexp.MustCompile(`(?i)^(ок,? )?(всё |все )?сделал(а)?$`),
	regexp.MustCompile(`(?i)^ок,? сделаю$`),
	regexp.MustCompile(`(?i)^(уже )?отправил(а)?$`),
	regexp.MustCompile(`(?i)^(уже )?(зарегался|зарегалась|зарегистрировался|зарегистрировалась|зарегистрировал(а)?ся)$`),
	regexp.MustCompile(`(?i)^(уже )?(установил|скачал|создал|завёл|завел|выполнил|оплатил|заполнил)(а)?$`),
	regexp.MustCompile(`(?i)^(ок|окей|ok)$`),
	regexp.MustCompile(`(?i)^done$`),
}

// ackDepositPattern is the carve-out the owner insisted on: a message about
// money in flight is never "just a receipt", it is the hand-off moment, and
// the answer there may be as long as it needs to be.
var ackDepositPattern = regexp.MustCompile(`(?i)пополн|депозит|deposit|перевёл|перевел|закинул|занёс|занес|оплат|платёж|платеж|внёс|внес`)

// ackConfirmationBatch reports a turn that is nothing but a confirmation of
// an action. Whole batch, same rule as courtesyOnly: one substantive message
// in the batch and the turn is substantive.
func ackConfirmationBatch(pending []Message) bool {
	if len(pending) == 0 {
		return false
	}
	for _, m := range pending {
		if len(m.Attachments) > 0 || len(m.Entities) > 0 {
			return false
		}
		text := strings.TrimFunc(m.Content, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune(".!…)", r)
		})
		if ackDepositPattern.MatchString(text) {
			return false
		}
		matched := false
		for _, re := range ackConfirmationPatterns {
			if re.MatchString(text) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// ackLimitReason reports a reply that is too long for the receipt it answers.
// limit <= 0 (AGENT_RUNTIME_ACK_LIMIT unset) switches the whole rule off, an
// open loop switches it off for this turn (something is still unanswered, so
// the turn has real work to do), and so does an empty confirmation batch.
func ackLimitReason(reply string, pending []Message, state RuntimeState, limit int) string {
	if limit <= 0 {
		return ""
	}
	for _, loop := range state.OpenLoops {
		if loop.Status == "open" {
			return ""
		}
	}
	if !ackConfirmationBatch(pending) {
		return ""
	}
	if runes := len([]rune(strings.TrimSpace(reply))); runes > limit {
		return fmt.Sprintf("reply is %d runes for a bare confirmation, limit %d", runes, limit)
	}
	return ""
}

// ackRepairHint is written in the same voice as repeatRepairHint: what was
// wrong, what to do instead, and nothing about the machinery.
func ackRepairHint(limit int) string {
	return fmt.Sprintf("Предыдущий черновик клиенту не отправлен: клиент сообщил, что действие выполнено, и ждёт короткого подтверждения, а не объяснения. Ответь одной короткой репликой до %d знаков, без нового вопроса и без пересказа того, что он только что сделал.", limit)
}
