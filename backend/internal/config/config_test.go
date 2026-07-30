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
