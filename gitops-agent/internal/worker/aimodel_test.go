package worker

import "testing"

// resolveProfile and stageFromEnv are pure helpers used on every AIModel
// render. Regressions here would silently mis-render manifests, so the
// matrix is worth pinning explicitly.

func TestResolveProfile(t *testing.T) {
	cases := []struct {
		name string
		in   string
		cpu  string
		mem  string
		gpu  string
	}{
		{"cpu-small", "cpu-small", "1", "2Gi", ""},
		{"cpu-medium", "cpu-medium", "2", "4Gi", ""},
		{"gpu-t4", "gpu-t4", "4", "16Gi", "1"},
		{"gpu-a100", "gpu-a100", "8", "32Gi", "1"},
		// Unknown profile falls back to cpu-small rather than rendering empty
		// resources blocks (which Crossplane would reject).
		{"unknown falls back to cpu-small", "totally-bogus", "1", "2Gi", ""},
		{"empty falls back to cpu-small", "", "1", "2Gi", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveProfile(c.in)
			if got.cpu != c.cpu || got.memory != c.mem || got.gpu != c.gpu {
				t.Errorf("resolveProfile(%q) = {%q, %q, %q}, want {%q, %q, %q}",
					c.in, got.cpu, got.memory, got.gpu, c.cpu, c.mem, c.gpu)
			}
		})
	}
}

func TestStageFromEnv(t *testing.T) {
	cases := []struct {
		envName, want string
	}{
		{"dev", "development"},
		{"DEV", "development"},
		{"Dev", "development"},
		{"development", "development"},
		{"prod", "production"},
		{"production", "production"},
		{"staging", "production"},
		{"qa", "production"},
		{"", "production"},
	}
	for _, c := range cases {
		if got := stageFromEnv(c.envName); got != c.want {
			t.Errorf("stageFromEnv(%q) = %q, want %q", c.envName, got, c.want)
		}
	}
}

func TestGenerateAPIKey(t *testing.T) {
	plain, prefix, err := generateAPIKey()
	if err != nil {
		t.Fatalf("generateAPIKey: %v", err)
	}
	// prefix is what we persist in aimodel_api_keys.key_prefix and surface
	// to the UI as `aim_xxxxxxxx`. 12 chars = "aim_" + 8 hex digits.
	if len(prefix) != 12 {
		t.Errorf("prefix len = %d, want 12 (got %q)", len(prefix), prefix)
	}
	if string(plain[:12]) != prefix {
		t.Errorf("plain[:12] = %q, want prefix %q", plain[:12], prefix)
	}
	if string(plain[:4]) != "aim_" {
		t.Errorf("plain prefix = %q, want %q", plain[:4], "aim_")
	}
	// 4 chars "aim_" + 64 hex chars from 32 random bytes = 68
	if len(plain) != 68 {
		t.Errorf("plain len = %d, want 68", len(plain))
	}

	// Two consecutive calls must not collide — would be catastrophic if they did.
	plain2, _, err := generateAPIKey()
	if err != nil {
		t.Fatalf("generateAPIKey #2: %v", err)
	}
	if string(plain) == string(plain2) {
		t.Error("two calls to generateAPIKey returned identical keys")
	}
}

func TestDefaultIfEmpty(t *testing.T) {
	if defaultIfEmpty("", "fallback") != "fallback" {
		t.Error("empty input did not return fallback")
	}
	if defaultIfEmpty("set", "fallback") != "set" {
		t.Error("non-empty input was overwritten by fallback")
	}
}

func TestAsString(t *testing.T) {
	if asString("hello") != "hello" {
		t.Error("string input not returned as-is")
	}
	if asString(nil) != "" {
		t.Error("nil should produce empty string, not panic")
	}
	if asString(42) != "" {
		t.Error("non-string should produce empty string, not panic")
	}
}
