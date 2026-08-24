package api

import (
	"encoding/json"
	"testing"
)

// composeSnapshot is the exact summary shape gitops-agent's composeAppSummary
// writes for a VM app: runtime/status/desired, and NO top-level "port". Every
// domain pass that keyed on summary["port"] therefore saw zero for every VM app
// and quietly decided "not an HTTP app" instead of deciding anything.
func composeSnapshot(t *testing.T, ports ...string) map[string]any {
	t.Helper()
	raw := map[string]any{
		"runtime": "compose",
		"status":  "Pending",
		"desired": map[string]any{
			"image": "nginx:alpine",
			"ports": ports,
		},
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var summary map[string]any
	if err := json.Unmarshal(encoded, &summary); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	return summary
}

func TestSummaryServicePortReadsBothSnapshotShapes(t *testing.T) {
	cases := []struct {
		name    string
		summary map[string]any
		want    int
	}{
		{"k8s top-level port", map[string]any{"port": float64(8080)}, 8080},
		{"compose host:container", composeSnapshot(t, "8080:3000"), 3000},
		{"compose bound to an interface", composeSnapshot(t, "127.0.0.1:8080:3000/tcp"), 3000},
		{"compose bare container port", composeSnapshot(t, "3000"), 3000},
		{"compose skips unparseable entries", composeSnapshot(t, "", "8080:3000"), 3000},
		{"compose with no ports", composeSnapshot(t), 0},
		{"no snapshot fields at all", map[string]any{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summaryServicePort(tc.summary); got != tc.want {
				t.Fatalf("summaryServicePort = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestAppNeedsDefaultDomainOnComposeSnapshots is the test that would have caught
// the silent no-op: before summaryServicePort learned the compose shape, every
// one of these returned false, so widening a domain pass to the VM runtime would
// have changed nothing at all while looking like it had.
func TestAppNeedsDefaultDomainOnComposeSnapshots(t *testing.T) {
	if !appNeedsDefaultDomain(composeSnapshot(t, "8080:3000")) {
		t.Fatal("an HTTP compose app must need a default domain")
	}
	if appNeedsDefaultDomain(composeSnapshot(t, "5432:5432")) {
		t.Fatal("a postgres compose app must not need a default domain")
	}
	worker := composeSnapshot(t, "8080:3000")
	worker["worker"] = true
	if appNeedsDefaultDomain(worker) {
		t.Fatal("a worker must not need a default domain regardless of its ports")
	}
}

func TestContainerPortFromPortString(t *testing.T) {
	cases := map[string]int{
		"80":                    80,
		"8080:80":               80,
		"127.0.0.1:8080:80":     80,
		"8080:80/udp":           80,
		" 8080:80 ":             80,
		"":                      0,
		"not-a-port":            0,
		"8080:":                 0,
		"8080:99999":            0,
		"8080:0":                0,
		"[::1]:8080:80":         80,
		"0.0.0.0:8080:8080/tcp": 8080,
	}
	for in, want := range cases {
		if got := containerPortFromPortString(in); got != want {
			t.Errorf("containerPortFromPortString(%q) = %d, want %d", in, got, want)
		}
	}
}
