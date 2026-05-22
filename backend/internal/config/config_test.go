package config

import "testing"

func TestLoadUsesDatabaseURLFallbackAndHttpPort(t *testing.T) {
	t.Setenv("DB_URL", "")
	t.Setenv("DATABASE_URL", "postgres://fallback")
	t.Setenv("JWT_SECRET", "secret")
	t.Setenv("HTTP_PORT", "9090")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DBURL != "postgres://fallback" {
		t.Fatalf("cfg.DBURL = %q, want %q", cfg.DBURL, "postgres://fallback")
	}
	if cfg.Port != "9090" {
		t.Fatalf("cfg.Port = %q, want %q", cfg.Port, "9090")
	}
}

func TestInferenceMaxBodyBytesDefault(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "s")
	t.Setenv("INFERENCE_MAX_BODY_BYTES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.InferenceMaxBodyBytes != 10*1024*1024 {
		t.Errorf("default = %d, want 10485760", cfg.InferenceMaxBodyBytes)
	}
}

func TestInferenceMaxBodyBytesOverride(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "s")
	t.Setenv("INFERENCE_MAX_BODY_BYTES", "52428800") // 50 MB
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.InferenceMaxBodyBytes != 52428800 {
		t.Errorf("override = %d, want 52428800", cfg.InferenceMaxBodyBytes)
	}
}

func TestInferenceMaxBodyBytesInvalidFallsBack(t *testing.T) {
	// Negative / non-numeric / zero must fall back to the default rather
	// than uncapping the proxy. A typo here used to mean "no cap" which
	// defeats the whole point.
	for _, v := range []string{"-1", "0", "ten", "abc"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("DB_URL", "postgres://x")
			t.Setenv("JWT_SECRET", "s")
			t.Setenv("INFERENCE_MAX_BODY_BYTES", v)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.InferenceMaxBodyBytes != 10*1024*1024 {
				t.Errorf("%q: got %d, want default 10485760",
					v, cfg.InferenceMaxBodyBytes)
			}
		})
	}
}

