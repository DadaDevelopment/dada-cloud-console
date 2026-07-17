package notify

import (
	"strings"
	"testing"
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
