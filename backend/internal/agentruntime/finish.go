package agentruntime

import "strings"

// finishAcknowledgement is the only reply the platform itself ever authors on
// the message path. The agent is deliberately not consulted: the conversation
// history it would be given is exactly what /finish discards.
const finishAcknowledgement = "Готово. Я забыл этот диалог целиком — следующее сообщение начнёт разговор с нуля."

const finishAuditNote = "conversation finished by user command"

// finishCommand reports whether the batch carries the platform-level reset: a
// whole message that is exactly /finish, optionally addressed to one bot
// (/finish@some_bot). Text that merely mentions the command, or carries
// anything besides it, does not match.
func finishCommand(messages []InboundMessage) bool {
	for _, m := range messages {
		t := strings.ToLower(strings.TrimSpace(m.Content))
		if t == "/finish" {
			return true
		}
		if strings.HasPrefix(t, "/finish@") && !strings.ContainsAny(t, " \t\n") {
			return true
		}
	}
	return false
}
