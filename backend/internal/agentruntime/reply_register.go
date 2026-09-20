package agentruntime

import (
	"fmt"

	"github.com/dada-tuda/console/backend/internal/agentjudge"
)

// clientRegister reads the client's «ты»/«вы» from the dialog, newest message
// first; pending is appended after history because history may or may not
// contain it yet depending on the caller.
func clientRegister(history, pending []Message) string {
	var texts []string
	for _, m := range history {
		if m.Role == "user" {
			texts = append(texts, m.Content)
		}
	}
	for _, m := range pending {
		texts = append(texts, m.Content)
	}
	return agentjudge.ClientRegister(texts)
}

// registerMismatchReason reports a reply that addresses the client in the
// other register («Давайте продолжим» to a client who writes «ты», live
// 2026-09-20 idle step 1). The client register is decided by the client's
// own words, so the check is silent until the client has shown one.
func registerMismatchReason(reply, register string) string {
	if register == "" {
		return ""
	}
	word := agentjudge.RegisterMismatch(reply, register)
	if word == "" {
		return ""
	}
	return fmt.Sprintf("client writes «%s», reply says «%s»", registerWord(register), word)
}

func registerWord(register string) string {
	if register == agentjudge.RegisterTy {
		return "ты"
	}
	return "вы"
}

const registerRepairHintTy = "Предыдущий черновик клиенту не отправлен: клиент пишет на «ты», а в черновике было обращение на «вы» («%s»). Перепиши на «ты» целиком, включая строки скрипта и ступени дожима («давай продолжим», «подскажи», «ты»). Напиши только сам ответ клиенту: 1-3 коротких предложения по-русски."

const registerRepairHintVy = "Предыдущий черновик клиенту не отправлен: клиент пишет на «вы», а в черновике было обращение на «ты» («%s»). Перепиши на «вы» целиком («давайте продолжим», «подскажите», «вы»). Напиши только сам ответ клиенту: 1-3 коротких предложения по-русски."

func registerRepairHint(reply, register string) string {
	word := agentjudge.RegisterMismatch(reply, register)
	if register == agentjudge.RegisterTy {
		return fmt.Sprintf(registerRepairHintTy, word)
	}
	return fmt.Sprintf(registerRepairHintVy, word)
}
