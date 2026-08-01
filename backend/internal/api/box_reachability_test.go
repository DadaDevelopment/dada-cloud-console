package api

import "testing"

func TestBrokerPortOfKeepsTheExposedPort(t *testing.T) {
	if got := brokerPortOf("http://10.244.0.14:8080/mcp"); got != 8080 {
		t.Fatalf("a pod endpoint published on 8080 was read as %d, so the exposure lookup would miss it", got)
	}
	if got := brokerPortOf("http://127.0.0.1:9999/mcp"); got != 9999 {
		t.Fatalf("loopback broker port read as %d", got)
	}
}

func TestBrokerPortOfRefusesWhatCannotBePublished(t *testing.T) {
	for _, raw := range []string{
		"https://console.dada-tuda.ru/api/v1/box/session/mcp",
		"",
		"::not a url",
	} {
		if got := brokerPortOf(raw); got != 0 {
			t.Fatalf("%q yielded port %d; a portless endpoint must not match an exposure row", raw, got)
		}
	}
}

func TestBrokerPathOfKeepsTheBrokerPath(t *testing.T) {
	if got := brokerPathOf("http://10.244.0.14:8080/mcp"); got != "/mcp" {
		t.Fatalf("path %q; swapping the host must not drop the path the broker answers on", got)
	}
	if got := brokerPathOf("http://10.244.0.14:8080"); got != "/mcp" {
		t.Fatalf("a pathless endpoint fell back to %q instead of the broker path", got)
	}
}
