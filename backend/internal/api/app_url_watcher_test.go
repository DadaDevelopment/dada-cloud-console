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
// as its owner intended. Non-worker rows carry hostnameOriginUnknown because
// their origin is irrelevant to the decision -- only a worker's hostname
// origin ever changes the verdict.
func TestParseURLProbeCandidate(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		origin  hostnameOrigin
		wantOK  bool
		wantPor int
	}{
		{"web app", `{"url":"https://app.dada-tuda.ru","port":8080}`, hostnameOriginUnknown, true, 8080},
		{"non standard port", `{"url":"https://app.dada-tuda.ru","port":3000}`, hostnameOriginUnknown, true, 3000},
		{"worker with managed hostname", `{"url":"https://app.dada-tuda.ru","port":8080,"worker":true}`, hostnameOriginManaged, false, 0},
		{"worker with custom hostname", `{"url":"https://app.dada-tuda.ru","port":8080,"worker":true}`, hostnameOriginCustom, true, 8080},
		{"worker with unknown hostname", `{"url":"https://app.dada-tuda.ru","port":8080,"worker":true}`, hostnameOriginUnknown, false, 0},
		{"no url", `{"port":8080}`, hostnameOriginUnknown, false, 0},
		{"empty url", `{"url":"","port":8080}`, hostnameOriginUnknown, false, 0},
		{"no port", `{"url":"https://app.dada-tuda.ru"}`, hostnameOriginUnknown, false, 0},
		{"zero port", `{"url":"https://app.dada-tuda.ru","port":0}`, hostnameOriginUnknown, false, 0},
		{"negative port", `{"url":"https://app.dada-tuda.ru","port":-1}`, hostnameOriginUnknown, false, 0},
		{"port as string", `{"url":"https://app.dada-tuda.ru","port":"8080"}`, hostnameOriginUnknown, false, 0},
		{"empty summary", ``, hostnameOriginUnknown, false, 0},
		{"malformed json", `{"url":`, hostnameOriginUnknown, false, 0},
		{"json null", `null`, hostnameOriginUnknown, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseURLProbeCandidate("ns-1", "app-1", []byte(c.summary), c.origin)
			if ok != c.wantOK {
				t.Fatalf("parseURLProbeCandidate(%q, %v) ok = %v, want %v", c.summary, c.origin, ok, c.wantOK)
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

// TestNormalizeHostnameForMatch pins the url<->domain_hostnames.hostname
// comparison: the summary carries a full "https://host" URL while
// domain_hostnames stores a bare host, and both must fold to the same key
// regardless of scheme, port, trailing slash, or case.
func TestNormalizeHostnameForMatch(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare host", "fanvk-f0e6dc.dada-tuda.ru", "fanvk-f0e6dc.dada-tuda.ru"},
		{"https url", "https://fanvk-f0e6dc.dada-tuda.ru", "fanvk-f0e6dc.dada-tuda.ru"},
		{"http url", "http://fanvk-f0e6dc.dada-tuda.ru", "fanvk-f0e6dc.dada-tuda.ru"},
		{"trailing slash", "https://fanvk-f0e6dc.dada-tuda.ru/", "fanvk-f0e6dc.dada-tuda.ru"},
		{"explicit port", "https://fanvk-f0e6dc.dada-tuda.ru:443/", "fanvk-f0e6dc.dada-tuda.ru"},
		{"mixed case", "HTTPS://Fanvk-F0E6DC.Dada-Tuda.RU", "fanvk-f0e6dc.dada-tuda.ru"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeHostnameForMatch(c.in); got != c.want {
				t.Fatalf("normalizeHostnameForMatch(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestResolveHostnameOrigin pins the loadCandidates-side join: it must match
// a summary's "https://host" url against a domain_hostnames.hostname row
// regardless of scheme/port/case, and classify by that row's managed flag.
// The managed case is the fanvk scenario verbatim: a worker that still
// carries the default hostname issued back when it was a web app.
func TestResolveHostnameOrigin(t *testing.T) {
	cases := []struct {
		name       string
		summary    string
		hostnames  string
		wantOrigin hostnameOrigin
	}{
		{
			name:       "fanvk: worker carrying orphaned managed default domain",
			summary:    `{"url":"https://fanvk-f0e6dc.dada-tuda.ru","port":8080,"worker":true}`,
			hostnames:  `[{"hostname":"fanvk-f0e6dc.dada-tuda.ru","managed":true}]`,
			wantOrigin: hostnameOriginManaged,
		},
		{
			name:       "worker with a hand-attached custom domain",
			summary:    `{"url":"https://bot.example.com","port":8080,"worker":true}`,
			hostnames:  `[{"hostname":"bot.example.com","managed":false}]`,
			wantOrigin: hostnameOriginCustom,
		},
		{
			name:       "worker whose hostname has no domain_hostnames row",
			summary:    `{"url":"https://ghost.dada-tuda.ru","port":8080,"worker":true}`,
			hostnames:  `[]`,
			wantOrigin: hostnameOriginUnknown,
		},
		{
			name:       "custom domain coexists with an unrelated managed row",
			summary:    `{"url":"https://bot.example.com","port":8080,"worker":true}`,
			hostnames:  `[{"hostname":"fanvk-f0e6dc.dada-tuda.ru","managed":true},{"hostname":"bot.example.com","managed":false}]`,
			wantOrigin: hostnameOriginCustom,
		},
		{
			name:       "scheme/case/port differences still match",
			summary:    `{"url":"https://Fanvk-F0E6DC.Dada-Tuda.RU:443/","port":8080,"worker":true}`,
			hostnames:  `[{"hostname":"fanvk-f0e6dc.dada-tuda.ru","managed":true}]`,
			wantOrigin: hostnameOriginManaged,
		},
		{
			name:       "malformed hostnames json",
			summary:    `{"url":"https://fanvk-f0e6dc.dada-tuda.ru","port":8080,"worker":true}`,
			hostnames:  `not json`,
			wantOrigin: hostnameOriginUnknown,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveHostnameOrigin([]byte(c.summary), []byte(c.hostnames))
			if got != c.wantOrigin {
				t.Fatalf("resolveHostnameOrigin(%q, %q) = %v, want %v", c.summary, c.hostnames, got, c.wantOrigin)
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
