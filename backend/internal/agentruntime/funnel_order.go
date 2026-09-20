package agentruntime

import "regexp"

// targetFactKey is the reported_facts slot for the customer's income goal
// (S8 of the lead's script); amountFactKey is the deposit under that goal (S9).
const targetFactKey = "target"

// funnelDepositQuestion matches the closing question of a turn that asks the
// customer about money: the size of the deposit, how much they will fund,
// what is free to start with.
var funnelDepositQuestion = regexp.MustCompile(`(?i)(депозит|сумм[аыуе]|пополн|бюджет|свободн|готов[а-яё]*\s+(внести|зайти|начать|стартовать)|с\s+какой\s+суммы|сколько\s+(готов|планиру|комфортн))`)

// funnelOrderReason reports a draft whose closing question asks about the
// deposit while neither the goal nor an amount is recorded in reported_facts.
// The lead's script (call_center docs/21, call 2026-09-17) fixes the order:
// first the desired income, then the comfortable deposit under it; a deposit
// question before the goal reads as a cash grab and the customer answers with
// the minimum. The prompt carries the same rule; this is the backstop.
func funnelOrderReason(reply string, state RuntimeState) string {
	if !endsWithQuestion(reply) {
		return ""
	}
	if _, ok := state.ReportedFacts[targetFactKey]; ok {
		return ""
	}
	if _, ok := state.ReportedFacts[amountFactKey]; ok {
		return ""
	}
	if funnelDepositQuestion.MatchString(closingSentence(reply)) {
		return "reply asks about the deposit before the income goal is recorded"
	}
	return ""
}

// funnelOrderRepairHint is written in the same voice as questionBudgetRepairHint.
const funnelOrderRepairHint = "Предыдущий черновик клиенту не отправлен: в нём вопрос о депозите или сумме, а цель клиента по доходу ещё не записана. Сначала цель: если этап скрипта уже дошёл до S8, закончи ход вопросом о цели по результатам в месяц или в год; если нет - продолжай серию без вопроса о деньгах. Напиши только сам ответ клиенту, без вопроса о сумме."
