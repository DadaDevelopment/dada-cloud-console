package notify

import (
	"io"
	"mime/quotedprintable"
	"strings"
	"testing"
	"time"
)

func testNotifier() *Notifier {
	return New("smtp.example.com", 587, "user", "pass", "development@dada-tuda.ru", "https://console.dada-tuda.ru/")
}

// TestNewNilOnMissingConfig locks the degrade-to-no-op contract: an empty host
// or from yields a nil Notifier (which Send treats as no-op), never a bad send.
func TestNewNilOnMissingConfig(t *testing.T) {
	if New("", 587, "", "", "from@x", "u") != nil {
		t.Error("empty host must return nil Notifier")
	}
	if New("h", 587, "", "", "", "u") != nil {
		t.Error("empty from must return nil Notifier")
	}
	if New("h", 587, "", "", "from@x", "u") == nil {
		t.Error("valid config must return a Notifier")
	}
}

func TestComposeSuccess(t *testing.T) {
	n := testNotifier()
	subject, body := n.Compose("a2ahub-landing", "success", "a2ahub-landing-00db4c.dada-tuda.ru", "", "proj-1", "build-1")
	if !strings.Contains(subject, "a2ahub-landing") {
		t.Errorf("subject missing app name: %q", subject)
	}
	if !strings.Contains(body, "https://a2ahub-landing-00db4c.dada-tuda.ru") {
		t.Errorf("success body missing app URL: %q", body)
	}
	wantLink := "https://console.dada-tuda.ru/projects/proj-1/apps/a2ahub-landing/builds/build-1"
	if !strings.Contains(body, wantLink) {
		t.Errorf("success body missing build deep link: %q", body)
	}
}

// TestComposeSuccessNoHostname: datastore/no-domain apps still get a clean mail
// with no dangling "https://" for a missing host.
func TestComposeSuccessNoHostname(t *testing.T) {
	n := testNotifier()
	_, body := n.Compose("myredis", "success", "", "", "proj-1", "build-1")
	if strings.Contains(body, "https://\n") || strings.Contains(body, "Открыть:") {
		t.Errorf("no-hostname success must omit the open link: %q", body)
	}
}

func TestComposeFailure(t *testing.T) {
	n := testNotifier()
	subject, body := n.Compose("myapp", "failure", "", "npm ci exited 1", "proj-1", "build-1")
	if !strings.Contains(subject, "не удалась") {
		t.Errorf("failure subject wrong: %q", subject)
	}
	if !strings.Contains(body, "npm ci exited 1") {
		t.Errorf("failure body missing reason: %q", body)
	}
	wantLink := "https://console.dada-tuda.ru/projects/proj-1/apps/myapp/builds/build-1"
	if !strings.Contains(body, wantLink) {
		t.Errorf("failure body missing build deep link: %q", body)
	}
}

// TestRenderCyrillicHeader: a Russian subject must be RFC-2047 encoded so it is
// not shipped as raw non-ASCII in the header.
func TestRenderCyrillicHeader(t *testing.T) {
	n := testNotifier()
	msg := n.render("u@x.ru", "приложение собрано", "тело")
	if !strings.Contains(msg, "Subject: =?UTF-8?B?") {
		t.Errorf("cyrillic subject not encoded: %q", msg)
	}
	if !strings.Contains(msg, "To: u@x.ru") {
		t.Errorf("missing To header: %q", msg)
	}
	if !strings.Contains(msg, "\r\n\r\n") {
		t.Errorf("missing header/body separator: %q", msg)
	}
}

// TestRenderHasDateHeader locks the RFC-5322 mandatory header. Without it the
// send still succeeds and the audit row still says "success", while the letter
// is scored as spam at the receiver — a silence no metric of ours can see.
func TestRenderHasDateHeader(t *testing.T) {
	n := testNotifier()
	msg := n.render("u@x.ru", "subject", "body")
	if !strings.Contains(msg, "\r\nDate: ") {
		t.Errorf("missing Date header: %q", msg)
	}
	header, _, ok := strings.Cut(msg, "\r\n\r\n")
	if !ok {
		t.Fatalf("missing header/body separator: %q", msg)
	}
	for _, line := range strings.Split(header, "\r\n") {
		if strings.HasPrefix(line, "Date: ") {
			if _, err := time.Parse(time.RFC1123Z, strings.TrimPrefix(line, "Date: ")); err != nil {
				t.Errorf("Date header is not RFC1123Z: %q (%v)", line, err)
			}
		}
	}
}

// TestRenderLongLineIsWrapped is the regression for the SMTP incident that
// killed nine operator alerts: Postbox answers 500 "Line too long" past the
// RFC 5321 limit of 1000 octets, so no rendered line may reach it however wide
// the build log was.
func TestRenderLongLineIsWrapped(t *testing.T) {
	n := testNotifier()
	_, body := n.Compose("myapp", "failure", "", strings.Repeat("stacktrace ", 400), "proj-1", "build-1")
	msg := n.render("u@x.ru", "сборка не удалась", body)
	if !strings.Contains(msg, "Content-Transfer-Encoding: quoted-printable") {
		t.Errorf("body not declared quoted-printable: %q", msg)
	}
	for i, line := range strings.Split(msg, "\r\n") {
		if len(line) > 998 {
			t.Errorf("line %d is %d octets, over the RFC 5321 limit", i, len(line))
		}
	}
}

// TestEncodeBodyRoundTrips: the reader must see the original text, soft line
// breaks and all, so wrapping never costs the letter its meaning.
func TestEncodeBodyRoundTrips(t *testing.T) {
	original := "Сборка приложения myapp не завершилась успешно.\n\nПричина: " + strings.Repeat("длинная строка ", 200) + "\n"
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(encodeBody(original))))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := strings.ReplaceAll(string(decoded), "\r\n", "\n"); got != original {
		t.Errorf("round trip lost content:\n got %q\nwant %q", got, original)
	}
}

func TestEncodeHeaderASCIIUnchanged(t *testing.T) {
	if got := encodeHeader("plain ascii"); got != "plain ascii" {
		t.Errorf("ASCII header must pass through, got %q", got)
	}
}

func TestSendNilNoop(t *testing.T) {
	var n *Notifier
	if err := n.Send("u@x.ru", "s", "b"); err != nil {
		t.Errorf("nil notifier Send must be no-op, got %v", err)
	}
}
