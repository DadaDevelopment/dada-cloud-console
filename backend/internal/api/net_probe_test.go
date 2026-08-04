package api

import "testing"

func TestValidateNetProbeRequest_RejectsUnsafeTargets(t *testing.T) {
	cases := []struct {
		name string
		req  netProbeRequest
	}{
		{"empty target", netProbeRequest{Target: "", Port: 443}},
		{"url with scheme", netProbeRequest{Target: "https://example.com", Port: 443}},
		{"path suffix", netProbeRequest{Target: "example.com/foo", Port: 443}},
		{"userinfo", netProbeRequest{Target: "user@example.com", Port: 443}},
		{"whitespace", netProbeRequest{Target: "exa mple.com", Port: 443}},
		{"loopback IP", netProbeRequest{Target: "127.0.0.1", Port: 443}},
		{"private IP", netProbeRequest{Target: "10.0.0.5", Port: 443}},
		{"link-local IP", netProbeRequest{Target: "169.254.1.1", Port: 443}},
		{"cloud metadata IP", netProbeRequest{Target: "169.254.169.254", Port: 443}},
		{"cluster-internal suffix", netProbeRequest{Target: "app.default.svc.cluster.local", Port: 443}},
		{"bare svc suffix", netProbeRequest{Target: "app.default.svc", Port: 443}},
		{"port too low", netProbeRequest{Target: "example.com", Port: 0}},
		{"port too high", netProbeRequest{Target: "example.com", Port: 70000}},
		{"bad protocol", netProbeRequest{Target: "example.com", Port: 443, Protocol: "shell"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateNetProbeRequest(tc.req); err == nil {
				t.Errorf("expected %+v to be rejected, got no error", tc.req)
			}
		})
	}
}

func TestValidateNetProbeRequest_AcceptsRealTargets(t *testing.T) {
	cases := []struct {
		name         string
		req          netProbeRequest
		wantProtocol string
	}{
		{"defaults to tls on 443", netProbeRequest{Target: "s3.ru1.storage.beget.cloud", Port: 443}, "tls"},
		{"defaults to tcp on other ports", netProbeRequest{Target: "example.com", Port: 5432}, "tcp"},
		{"explicit http", netProbeRequest{Target: "example.com", Port: 80, Protocol: "HTTP"}, "http"},
		{"public IP literal", netProbeRequest{Target: "8.8.8.8", Port: 443}, "tls"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := validateNetProbeRequest(tc.req)
			if err != nil {
				t.Fatalf("expected %+v to be accepted, got error: %v", tc.req, err)
			}
			if spec.Protocol != tc.wantProtocol {
				t.Errorf("protocol = %q, want %q", spec.Protocol, tc.wantProtocol)
			}
			if spec.Target != tc.req.Target {
				t.Errorf("target = %q, want %q", spec.Target, tc.req.Target)
			}
		})
	}
}
