package worker

import (
	"net/url"
	"testing"
	"time"
)

// TestClassifyLivenessResponse_NeverMarksErrorHealthy is the falsification
// guard for the whole classifier: whatever the HTTP method or transport
// layer decides, a 4xx/5xx status must never come back with an empty
// http_reason (which is how the backend's live_urls aggregation reads
// "healthy"). A single status class answering identically for every probed
// app -- which is exactly what shipped when the probe recorded the
// ingress's 308 scheme-upgrade redirect as the terminal answer -- is the
// signature this test exists to catch.
func TestClassifyLivenessResponse_NeverMarksErrorHealthy(t *testing.T) {
	checkedAt := time.Now().UTC()
	cases := []struct {
		status      int
		wantHealthy bool
	}{
		{200, true},
		{204, true},
		{301, true},
		{302, true},
		{308, true},
		{400, false},
		{404, false},
		{429, false},
		{500, false},
		{502, false},
		{503, false},
	}
	for _, c := range cases {
		res := classifyLivenessResponse(c.status, checkedAt)
		if res.status != c.status {
			t.Fatalf("status %d: recorded status = %d, want %d", c.status, res.status, c.status)
		}
		healthy := res.reason == ""
		if healthy != c.wantHealthy {
			t.Fatalf("status %d: reason = %q (healthy=%v), want healthy=%v", c.status, res.reason, healthy, c.wantHealthy)
		}
		if !healthy && res.reason != "status_"+itoa(c.status) {
			t.Fatalf("status %d: reason = %q, want status_%d", c.status, res.reason, c.status)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestNextRedirectTarget_FollowsSameHost proves the scheme-upgrade case this
// whole fix exists for: an ingress answering plain HTTP with a redirect to
// the HTTPS form of the SAME tenant hostname is followed, and the resolved
// target keeps the original in-cluster host:port (the ingress Service)
// rather than dialing the tenant's public hostname from the redirect.
func TestNextRedirectTarget_FollowsSameHost(t *testing.T) {
	cur, err := url.Parse("http://ingress-nginx-pub-controller.network.svc.cluster.local/")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	next, follow := nextRedirectTarget(cur, "https://fanvk-f0e6dc.dada-tuda.ru/", "fanvk-f0e6dc.dada-tuda.ru")
	if !follow {
		t.Fatalf("follow = false, want true for a same-host scheme upgrade")
	}
	if next.Scheme != "https" {
		t.Fatalf("next.Scheme = %q, want https", next.Scheme)
	}
	if next.Host != cur.Host {
		t.Fatalf("next.Host = %q, want %q (must stay on the in-cluster ingress Service, not the public hostname from Location)", next.Host, cur.Host)
	}
}

// TestNextRedirectTarget_RefusesOffHost proves the guardrail: a redirect
// naming a different host than the app being probed is never followed, so a
// misbehaving or malicious redirect can never steer the probe at a
// different app or an arbitrary external target.
func TestNextRedirectTarget_RefusesOffHost(t *testing.T) {
	cur, err := url.Parse("http://ingress-nginx-pub-controller.network.svc.cluster.local/")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	_, follow := nextRedirectTarget(cur, "https://evil.example.com/", "fanvk-f0e6dc.dada-tuda.ru")
	if follow {
		t.Fatalf("follow = true, want false for a redirect to a different host")
	}
}

// TestNextRedirectTarget_FollowsRelativePath proves an app's own same-host
// redirect (e.g. a trailing-slash or canonical-path bounce, carrying only a
// relative Location) is still chased.
func TestNextRedirectTarget_FollowsRelativePath(t *testing.T) {
	cur, err := url.Parse("http://ingress-nginx-pub-controller.network.svc.cluster.local/old")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	next, follow := nextRedirectTarget(cur, "/new", "profi.dada-tuda.ru")
	if !follow {
		t.Fatalf("follow = false, want true for a relative Location")
	}
	if next.Path != "/new" {
		t.Fatalf("next.Path = %q, want /new", next.Path)
	}
	if next.Host != cur.Host {
		t.Fatalf("next.Host = %q, want unchanged %q", next.Host, cur.Host)
	}
}

// TestNextRedirectTarget_RefusesMissingLocation proves a 3xx with no
// Location header (malformed but real-world possible) is treated as
// terminal rather than crashing or looping.
func TestNextRedirectTarget_RefusesMissingLocation(t *testing.T) {
	cur, err := url.Parse("http://ingress-nginx-pub-controller.network.svc.cluster.local/")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	_, follow := nextRedirectTarget(cur, "", "profi.dada-tuda.ru")
	if follow {
		t.Fatalf("follow = true, want false for an empty Location")
	}
}
