package api

import (
	"strings"
	"testing"
)

// TestLooksLikeHTTPResponse pins the whole classification the watcher makes
// from a raw TCP read. The false cases are the ones that matter: each is a
// live user app that binds its port and answers, so only this check keeps it
// from being reported as healthy web traffic.
func TestLooksLikeHTTPResponse(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"http11 ok", "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n", true},
		{"http10 not found", "HTTP/1.0 404 Not Found\r\n\r\n", true},
		{"http2 prefix", "HTTP/2 200\r\n\r\n", true},
		{"error status still http", "HTTP/1.1 500 Internal Server Error\r\n\r\n", true},
		{"empty read", "", false},
		{"binary protocol", "\x00\x01\x02\xef\xbb\xbf", false},
		{"ssh banner", "SSH-2.0-OpenSSH_9.6\r\n", false},
		{"lowercase prefix", "http/1.1 200 OK\r\n", false},
		{"leading whitespace", " HTTP/1.1 200 OK\r\n", false},
		{"mentions http later", "PROXY over HTTP/1.1 here\r\n", false},
		{"truncated prefix", "HTTP", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := looksLikeHTTPResponse([]byte(c.data)); got != c.want {
				t.Fatalf("looksLikeHTTPResponse(%q) = %v, want %v", c.data, got, c.want)
			}
		})
	}
}

// TestNotHTTPDetailBounded checks the detail column can never be grown
// without limit by a chatty binary protocol, and that only the first line is
// kept.
func TestNotHTTPDetailBounded(t *testing.T) {
	if got := notHTTPDetail(nil); !strings.Contains(got, "no HTTP response") {
		t.Fatalf("empty read detail = %q, want it to say no response was sent", got)
	}

	multi := notHTTPDetail([]byte("SSH-2.0-OpenSSH_9.6\r\nsecond line\r\nthird line"))
	if strings.Contains(multi, "second line") {
		t.Fatalf("detail kept more than the first line: %q", multi)
	}
	if !strings.Contains(multi, "SSH-2.0-OpenSSH_9.6") {
		t.Fatalf("detail dropped the first line: %q", multi)
	}
	if strings.Contains(multi, "\r") {
		t.Fatalf("detail kept a trailing CR: %q", multi)
	}

	long := notHTTPDetail([]byte(strings.Repeat("x", 5000)))
	if len(long) > 200 {
		t.Fatalf("detail for a 5000-byte response is %d bytes, want it bounded", len(long))
	}
}

// TestURLProbeStateAntiFlap is the anti-flap contract the owner's rule
// ("wrong alert is worse than no alert") rests on: two failures must stay
// silent, the third arms the alert, and any single success resets the streak
// so the next failure starts counting from one again.
func TestURLProbeStateAntiFlap(t *testing.T) {
	var s urlProbeState

	s, armed := s.recordFailure()
	if armed || s.ConsecutiveFailures != 1 {
		t.Fatalf("after 1 failure: armed=%v failures=%d, want false/1", armed, s.ConsecutiveFailures)
	}

	s, armed = s.recordFailure()
	if armed || s.ConsecutiveFailures != 2 {
		t.Fatalf("after 2 failures: armed=%v failures=%d, want false/2", armed, s.ConsecutiveFailures)
	}

	s, armed = s.recordFailure()
	if !armed || s.ConsecutiveFailures != appURLAlertFailureThreshold {
		t.Fatalf("after 3 failures: armed=%v failures=%d, want true/%d", armed, s.ConsecutiveFailures, appURLAlertFailureThreshold)
	}

	s, wasArmed := s.recordSuccess()
	if !wasArmed {
		t.Fatal("success after an armed alert must report wasArmed so the row gets cleared")
	}
	if s.ConsecutiveFailures != 0 {
		t.Fatalf("success left failures=%d, want 0", s.ConsecutiveFailures)
	}

	s, armed = s.recordFailure()
	if armed || s.ConsecutiveFailures != 1 {
		t.Fatalf("first failure after a reset: armed=%v failures=%d, want false/1", armed, s.ConsecutiveFailures)
	}
}

// TestURLProbeStateSuccessBelowThreshold covers the common case of a slow
// app that fails once and recovers: nothing was armed, so the caller must
// not be told to clear a row that never existed.
func TestURLProbeStateSuccessBelowThreshold(t *testing.T) {
	var s urlProbeState
	s, _ = s.recordFailure()
	s, _ = s.recordFailure()

	next, wasArmed := s.recordSuccess()
	if wasArmed {
		t.Fatal("success after 2 failures reported wasArmed, but no alert was ever armed")
	}
	if next.ConsecutiveFailures != 0 {
		t.Fatalf("success left failures=%d, want 0", next.ConsecutiveFailures)
	}
}

// TestParseURLProbeCandidate pins which snapshot rows are worth probing. The
// skip cases each prevent a false alert on an app that is behaving exactly
// as its owner intended.
func TestParseURLProbeCandidate(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		wantOK  bool
		wantPor int
	}{
		{"web app", `{"url":"https://app.dada-tuda.ru","port":8080}`, true, 8080},
		{"non standard port", `{"url":"https://app.dada-tuda.ru","port":3000}`, true, 3000},
		{"declared worker", `{"url":"https://app.dada-tuda.ru","port":8080,"worker":true}`, false, 0},
		{"no url", `{"port":8080}`, false, 0},
		{"empty url", `{"url":"","port":8080}`, false, 0},
		{"no port", `{"url":"https://app.dada-tuda.ru"}`, false, 0},
		{"zero port", `{"url":"https://app.dada-tuda.ru","port":0}`, false, 0},
		{"negative port", `{"url":"https://app.dada-tuda.ru","port":-1}`, false, 0},
		{"port as string", `{"url":"https://app.dada-tuda.ru","port":"8080"}`, false, 0},
		{"empty summary", ``, false, 0},
		{"malformed json", `{"url":`, false, 0},
		{"json null", `null`, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseURLProbeCandidate("ns-1", "app-1", []byte(c.summary))
			if ok != c.wantOK {
				t.Fatalf("parseURLProbeCandidate(%q) ok = %v, want %v", c.summary, ok, c.wantOK)
			}
			if !c.wantOK {
				return
			}
			if got.Port != c.wantPor {
				t.Fatalf("port = %d, want %d", got.Port, c.wantPor)
			}
			if got.Namespace != "ns-1" || got.AppName != "app-1" {
				t.Fatalf("identity = %s/%s, want ns-1/app-1", got.Namespace, got.AppName)
			}
		})
	}
}

// TestGroupAppAlertsCarriesURLType makes sure the new alert type survives the
// shaping step untouched: the console filters on this string, so a dropped
// or rewritten type is an invisible feature.
func TestGroupAppAlertsCarriesURLType(t *testing.T) {
	rows := []appAlertRow{
		{AppName: "bot", Type: "url", Reason: urlProbeReasonNoListener},
		{AppName: "proxy", Type: "url", Reason: urlProbeReasonNotHTTP, Detail: "connected but response did not start with HTTP/"},
	}

	out := groupAppAlerts(rows)

	if len(out["bot"]) != 1 || out["bot"][0].Type != "url" {
		t.Fatalf("bot alerts = %+v, want one url alert", out["bot"])
	}
	if out["bot"][0].Reason != urlProbeReasonNoListener {
		t.Fatalf("bot reason = %q, want %q", out["bot"][0].Reason, urlProbeReasonNoListener)
	}
	if len(out["proxy"]) != 1 || out["proxy"][0].Detail == "" {
		t.Fatalf("proxy alerts = %+v, want one url alert carrying a detail", out["proxy"])
	}
}
