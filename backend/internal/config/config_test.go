package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"
	"testing"
)

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

func TestGetEnvIntRejectsWhatWouldOtherwiseTruncate(t *testing.T) {
	// The bug this replaces was int(getEnvInt64(...)): on a 32-bit build the
	// int64 was silently narrowed, so an out-of-range port became a plausible
	// in-range one. getEnvInt parses straight into a platform int, so an
	// unrepresentable value is rejected and the default stands.
	//
	// The overflow boundary is platform-dependent by design, so the case is
	// built from math.MaxInt rather than hardcoded — on amd64 the 32-bit
	// example (4294967883) is a perfectly valid int and must be accepted.
	tooBig := "1" + strconv.Itoa(math.MaxInt)

	t.Setenv("DB_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "s")
	t.Setenv("SMTP_PORT", tooBig)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SMTPPort != 587 {
		t.Errorf("SMTP_PORT=%s gave port %d, want the default 587 — the value was narrowed instead of rejected",
			tooBig, cfg.SMTPPort)
	}
}

// TestBoxWarmPoolSizeZeroTurnsThePoolOff pins the parse this fixes.
//
// BOX_WARM_POOL_SIZE was read with getEnvInt, which folds n <= 0 into the
// default, so an operator who set 0 to stop paying for an unsold feature got a
// pool of 2. The values file, the ConfigMap and the pod env all read 0 while the
// process kept two pods and two 10Gi volumes hot around the clock, which makes a
// parse bug look like a cluster fault.
//
// Zero is the only value whose meaning changed. A negative or a typo must still
// fall back: a pool cannot be smaller than empty, and a typo must not silently
// disable a feature.
func TestBoxWarmPoolSizeZeroTurnsThePoolOff(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want int
	}{
		{"0", 0},
		{"1", 1},
		{"", 2},
		{"-1", 2},
		{"two", 2},
	} {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("DB_URL", "postgres://x")
			t.Setenv("JWT_SECRET", "s")
			t.Setenv("BOX_WARM_POOL_SIZE", tc.env)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.BoxWarmPoolSize != tc.want {
				t.Errorf("BOX_WARM_POOL_SIZE=%q gave %d, want %d",
					tc.env, cfg.BoxWarmPoolSize, tc.want)
			}
		})
	}
}

func TestNoCallSiteNarrowsAnInt64Setting(t *testing.T) {
	// The class, not the seven instances: a new int(getEnvInt64(...)) reintroduces
	// the same silent truncation, and it reads as deliberate to a reviewer.
	//
	// Checked over the AST rather than by grep, so the prose explaining the fix
	// can name the shape it forbids without failing its own guard.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "config.go", nil, 0)
	if err != nil {
		t.Fatalf("parse config.go: %v", err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		conv, ok := n.(*ast.CallExpr)
		if !ok || len(conv.Args) != 1 {
			return true
		}
		if id, ok := conv.Fun.(*ast.Ident); !ok || id.Name != "int" {
			return true
		}
		inner, ok := conv.Args[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := inner.Fun.(*ast.Ident); ok && id.Name == "getEnvInt64" {
			t.Errorf("config.go:%d narrows an int64 setting with an int(...) conversion; use getEnvInt for int-sized settings",
				fset.Position(conv.Pos()).Line)
		}
		return true
	})
}

// TestAppUsageBackfillDefaultsStayInsideBothHorizons guards the reconstruction
// window against the two limits that make a longer one pointless. The
// long-retention metrics store runs out (measured at roughly 22 days on this
// cluster), and the ledger prunes its own rows at 30 days, so hours
// reconstructed past that are deleted by the first prune they live through --
// the pass would spend hundreds of range queries writing rows nobody ever reads.
func TestAppUsageBackfillDefaultsStayInsideBothHorizons(t *testing.T) {
	const metricsStoreDays = 22
	const ledgerRetentionDays = 30
	t.Setenv("DB_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "s")
	t.Setenv("APP_USAGE_BACKFILL_DAYS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AppUsageBackfillDays <= 0 {
		t.Fatalf("backfill is off by default: %d", cfg.AppUsageBackfillDays)
	}
	if cfg.AppUsageBackfillDays > metricsStoreDays {
		t.Errorf("backfill reaches past the metrics store: %d days", cfg.AppUsageBackfillDays)
	}
	if cfg.AppUsageBackfillDays >= ledgerRetentionDays {
		t.Errorf("backfill reaches into rows the retention prune deletes: %d days", cfg.AppUsageBackfillDays)
	}
	if cfg.AppUsageBackfillTenant == "" {
		t.Error("backfill tenant must default to the tenant cluster-state metrics are written under")
	}
}

// TestAppUsageBackfillCanBeTurnedOff pins the off switch as a real zero rather
// than a value that silently collapses back to the default, which is exactly
// how the box pool lost its own kill switch (a3b5061).
func TestAppUsageBackfillCanBeTurnedOff(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "s")
	t.Setenv("APP_USAGE_BACKFILL_DAYS", "0")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AppUsageBackfillDays != 0 {
		t.Fatalf("explicit 0 did not disable the backfill: %d", cfg.AppUsageBackfillDays)
	}
}
