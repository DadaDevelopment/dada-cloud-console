package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

func TestSanitizeMetricName(t *testing.T) {
	cases := map[string]string{
		"http_requests_total": "http_requests_total",
		"cpu.usage":           "cpu_usage",
		"5xx":                 "_5xx",   // leading digit gets prefixed
		"a-b/c d":             "a_b_c_d",
		"":                    "_",      // empty coerces to _
		"123":                 "_123",
		"Foo_Bar9":            "Foo_Bar9",
	}
	for in, want := range cases {
		if got := sanitizeMetricName(in); got != want {
			t.Errorf("sanitizeMetricName(%q) = %q, want %q", in, got, want)
		}
	}
	// every output must be a valid Prometheus metric name
	for in := range cases {
		out := sanitizeMetricName(in)
		if out == "" {
			t.Errorf("empty output for %q", in)
			continue
		}
		c0 := out[0]
		if !(c0 == '_' || (c0 >= 'a' && c0 <= 'z') || (c0 >= 'A' && c0 <= 'Z')) {
			t.Errorf("output %q has invalid first char", out)
		}
		for _, r := range out {
			ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !ok {
				t.Errorf("output %q has invalid char %q", out, r)
			}
		}
	}
}

func TestGenerateMonitoringKey(t *testing.T) {
	full, prefix, hash, err := generateMonitoringKey()
	if err != nil {
		t.Fatalf("generateMonitoringKey: %v", err)
	}
	if !strings.HasPrefix(full, "dmon_") {
		t.Errorf("full key %q missing dmon_ prefix", full)
	}
	if len(prefix) != 13 || !strings.HasPrefix(full, prefix) {
		t.Errorf("prefix %q invalid for full %q", prefix, full)
	}
	if len(hash) != 48 { // 16 salt + 32 digest
		t.Fatalf("hash len = %d, want 48", len(hash))
	}
	// hash must verify against the plaintext: salt||argon2id(full,salt)
	salt := hash[:16]
	want := argon2.IDKey([]byte(full), salt, 1, 64*1024, 4, 32)
	if string(want) != string(hash[16:]) {
		t.Error("argon2id digest does not verify against plaintext key")
	}

	// keys must be unique across calls
	full2, _, _, _ := generateMonitoringKey()
	if full == full2 {
		t.Error("two generated keys collided")
	}
}

func TestIngestLimiter(t *testing.T) {
	// perMin=60 -> burst 60, refill 1/sec. First 60 allowed, 61st denied.
	l := newIngestLimiter(60)
	app := uuid.New()
	allowed := 0
	for i := 0; i < 60; i++ {
		if l.Allow(app) {
			allowed++
		}
	}
	if allowed != 60 {
		t.Errorf("expected 60 allowed in burst, got %d", allowed)
	}
	if l.Allow(app) {
		t.Error("61st request should be rate-limited")
	}
	// a different app has its own bucket
	if !l.Allow(uuid.New()) {
		t.Error("separate app should have its own bucket")
	}
}
