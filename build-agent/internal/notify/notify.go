// Package notify sends deploy-result emails to the project owner so a build
// outcome is never silent. It reuses the platform Postbox SMTP credentials.
//
// The runner constructs a Notifier ONLY when DEPLOY_NOTIFY_ENABLED is true; a
// nil *Notifier is a valid no-op, so with the flag off there is no mail path
// and zero effect on the build pipeline. Sends are best-effort: the caller
// launches them off the hot path and treats every error as non-fatal.
package notify

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/smtp"
	"strings"
	"time"
)

// Notifier holds the SMTP endpoint used for deploy-result mail.
type Notifier struct {
	host       string
	port       int
	user       string
	pass       string
	from       string
	consoleURL string
}

// New builds a Notifier. It returns nil when host or from is empty, so a
// misconfigured deployment degrades to no-op instead of erroring on every build.
func New(host string, port int, user, pass, from, consoleURL string) *Notifier {
	if host == "" || from == "" {
		return nil
	}
	return &Notifier{host: host, port: port, user: user, pass: pass, from: from, consoleURL: strings.TrimRight(consoleURL, "/")}
}

// buildURL builds a deep link straight to one build's log page in the
// console, so a deploy-result email lands the reader on that build's own
// logs instead of the bare dashboard. projectID/appName/buildID are all
// known at the runner call site.
func (n *Notifier) buildURL(projectID, appName, buildID string) string {
	return fmt.Sprintf("%s/projects/%s/apps/%s/builds/%s", n.consoleURL, projectID, appName, buildID)
}

// Compose builds the subject and plaintext body for a build result. Pure: no
// network, no clock — safe to unit-test. status is "success" or "failure".
// hostname is the app's public URL when known (success only); reason is the
// short failure cause; projectID/buildID identify the build for the deep
// link. hostname and reason may be empty.
func (n *Notifier) Compose(appName, status, hostname, reason, projectID, buildID string) (subject, body string) {
	buildLink := n.buildURL(projectID, appName, buildID)
	switch status {
	case "success":
		subject = fmt.Sprintf("%s: приложение собрано и развёрнуто", appName)
		var b strings.Builder
		fmt.Fprintf(&b, "Приложение %s успешно собрано и развёрнуто.\n\n", appName)
		if hostname != "" {
			fmt.Fprintf(&b, "Открыть: https://%s\n\n", hostname)
		}
		fmt.Fprintf(&b, "Логи сборки: %s\n\n", buildLink)
		b.WriteString("Пуш в основную ветку автоматически пересобирает и деплоит проект.\n")
		body = b.String()
	default:
		subject = fmt.Sprintf("%s: сборка не удалась", appName)
		var b strings.Builder
		fmt.Fprintf(&b, "Сборка приложения %s не завершилась успешно.\n\n", appName)
		if reason != "" {
			fmt.Fprintf(&b, "Причина: %s\n\n", reason)
		}
		fmt.Fprintf(&b, "Логи сборки и подробности: %s\n\n", buildLink)
		b.WriteString("Если нужна помощь — ответьте на это письмо.\n")
		body = b.String()
	}
	return subject, body
}

// Send delivers one message to a single recipient over SMTP with STARTTLS
// (net/smtp negotiates STARTTLS automatically when the server advertises it, as
// Postbox does on 587). Returns an error the caller logs and swallows.
func (n *Notifier) Send(to, subject, body string) error {
	if n == nil {
		return nil
	}
	if to == "" {
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
//
// Date is mandatory under RFC 5322 and its absence is a spam signal at exactly
// the receivers our users have (mail.ru, yandex). This is the only channel that
// reaches a person who has already closed the console, so a letter scored into
// a spam folder is indistinguishable from the deploy finishing in silence: the
// audit row says the send succeeded, because SMTP acceptance is all it can see.
// The backend mailer grew the header in 9e795149; this path never did.
func (n *Notifier) render(to, subject, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", n.from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeHeader(subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	b.WriteString(encodeBody(body))
	return b.String()
}

// encodeBody quoted-printable-encodes a message body so no single long line can
// kill the letter.
//
// RFC 5321 caps a line at 1000 octets and Postbox enforces it: it answers
// 500 "Line too long" and drops the connection. That is how nine operator
// alerts died before backend/internal/notify grew the same encoder. The build
// mail carries a failure reason lifted from a build log, so the same wide line
// can arrive here; writing the body raw only postponed the identical incident.
//
// It also makes the body byte-safe: quoted-printable escapes any byte the
// sender could not represent, so a truncation that split a multi-byte rune
// upstream degrades to an escape rather than to a broken message.
//
// On the encoder's own failure the raw body is returned: a letter that might be
// rejected still beats no letter at all.
func encodeBody(body string) string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	var out bytes.Buffer
	w := quotedprintable.NewWriter(&out)
	if _, err := io.WriteString(w, normalized); err != nil {
		return strings.ReplaceAll(normalized, "\n", "\r\n")
	}
	if err := w.Close(); err != nil {
		return strings.ReplaceAll(normalized, "\n", "\r\n")
	}
	return out.String()
}

// encodeHeader RFC-2047 base64-encodes a header value when it contains
// non-ASCII (the subjects are Russian), so mail clients render Cyrillic
// correctly instead of mojibake.
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
