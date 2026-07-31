package api

import "testing"

// TestValidEnvKey pins the server-side key check that bulk entry depends on.
// The single-variable form validates in the browser via a pattern attribute;
// a pasted .env reaches the server as free text, so a key like "MY KEY" or
// "9LIVES" must be rejected here rather than stored and then silently ignored
// by the container runtime.
func TestValidEnvKey(t *testing.T) {
	valid := []string{"A", "_", "BOT_TOKEN", "_private", "PORT8080", "a1_B2"}
	for _, k := range valid {
		if !validEnvKey(k) {
			t.Errorf("validEnvKey(%q) = false, want true", k)
		}
	}

	invalid := []string{"", "9LIVES", "MY KEY", "MY-KEY", "KEY=VALUE", "KÖLN", "a.b", "#comment"}
	for _, k := range invalid {
		if validEnvKey(k) {
			t.Errorf("validEnvKey(%q) = true, want false", k)
		}
	}

	long := make([]byte, 257)
	for i := range long {
		long[i] = 'A'
	}
	if validEnvKey(string(long)) {
		t.Error("validEnvKey(257 chars) = true, want false")
	}
}
