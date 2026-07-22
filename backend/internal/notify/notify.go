// Package notify sends best-effort operator email over the shared Postbox
// SMTP relay (same credentials Keycloak uses for its own mail). Every send is
// fire-and-forget: callers launch it off the request's hot path and log
// errors instead of propagating them, so a mail outage never blocks a user
// action.
package notify

import (
	"encoding/base64"
	"fmt"
	"net/smtp"
	"strings"
	"time"
)

// Notifier holds the SMTP endpoint used for operator mail.
type Notifier struct {
	host string
	port int
	user string
	pass string
	from string
}

// New builds a Notifier. It returns nil when host or from is empty, so a
// misconfigured deployment degrades to no-op instead of erroring on every send.
func New(host string, port int, user, pass, from string) *Notifier {
	if host == "" || from == "" {
		return nil
	}
	return &Notifier{host: host, port: port, user: user, pass: pass, from: from}
}

// ComposeSignup builds the subject and plaintext body for a new-user-signup
// notification. totalUsers is -1 when the caller could not cheaply compute it
// (the line is then omitted rather than shown as a wrong number).
func ComposeSignup(email, username, createdAtUTC string, totalUsers int) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: новая регистрация %s", email)
	var b strings.Builder
	fmt.Fprintf(&b, "Новый пользователь зарегистрировался в Dada Cloud.\n\n")
	fmt.Fprintf(&b, "Email: %s\n", email)
	fmt.Fprintf(&b, "Username: %s\n", username)
	fmt.Fprintf(&b, "Создан: %s (UTC)\n", createdAtUTC)
	if totalUsers >= 0 {
		fmt.Fprintf(&b, "Всего пользователей: %d\n", totalUsers)
	}
	return subject, b.String()
}

// ComposeAudit builds the subject and plaintext body for a significant-action
// owner notification: one email per curated audit_events row (app/project/db
// create, git connect, build trigger, domain attach, app delete).
func ComposeAudit(action, actorEmail, resourceName, projectName, createdAtUTC string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: %s — %s", action, actorEmail)
	var b strings.Builder
	fmt.Fprintf(&b, "Значимое событие в Dada Cloud.\n\n")
	fmt.Fprintf(&b, "Пользователь: %s\n", actorEmail)
	fmt.Fprintf(&b, "Действие: %s\n", action)
	fmt.Fprintf(&b, "Ресурс: %s\n", resourceName)
	fmt.Fprintf(&b, "Проект: %s\n", projectName)
	fmt.Fprintf(&b, "Время: %s (UTC)\n", createdAtUTC)
	return subject, b.String()
}

// ComposeAppAlert builds the subject and plaintext body for a silent-crash
// alert: the owner's app is stuck in CrashLoopBackOff/OOMKilled/ImagePullBackOff
// and would otherwise go unnoticed until the owner happens to open the
// console. logExcerpt is the best-effort last lines of the crashed container's
// log (may be empty when the cluster read failed or there was nothing to
// read); consoleLink deep-links straight to the app in the console.
func ComposeAppAlert(appName, reason, podName, logExcerpt, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: %s не работает (%s)", appName, reason)
	var b strings.Builder
	fmt.Fprintf(&b, "Приложение %s перезапускается и, похоже, не поднимается.\n\n", appName)
	fmt.Fprintf(&b, "Причина: %s\n", reason)
	fmt.Fprintf(&b, "Под: %s\n\n", podName)
	if logExcerpt != "" {
		b.WriteString("Последние строки лога:\n")
		b.WriteString(logExcerpt)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Открыть в консоли: %s\n\n", consoleLink)
	b.WriteString("Это письмо приходит не чаще раза в 24 часа на приложение.\n")
	return subject, b.String()
}

// Send delivers one message to a single recipient over SMTP with STARTTLS
// (net/smtp negotiates STARTTLS automatically when the server advertises it,
// as Postbox does on 587). Returns an error the caller logs and swallows.
func (n *Notifier) Send(to, subject, body string) error {
	if n == nil || to == "" {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", n.host, n.port)
	msg := n.render(to, subject, body)
	var auth smtp.Auth
	if n.user != "" {
		auth = smtp.PlainAuth("", n.user, n.pass, n.host)
	}
	return smtp.SendMail(addr, auth, n.from, []string{to}, []byte(msg))
}

// render assembles RFC-5322 headers + UTF-8 body.
func (n *Notifier) render(to, subject, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", n.from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeHeader(subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return b.String()
}

// encodeHeader RFC-2047 base64-encodes a header value when it contains
// non-ASCII (Russian subjects), so mail clients render Cyrillic correctly
// instead of mojibake.
func encodeHeader(s string) string {
	if isASCII(s) {
		return s
	}
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}
