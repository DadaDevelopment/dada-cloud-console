package notify

import "testing"

import "strings"

// TestClassifyCrashCauseEntrypointImportIsNotAppCode is the live shape of
// 2026-08-20: gulyaev-ai-core, a repo whose entry point lives inside the
// package "app", started by our own generated launch line "python
// app/main.py". The package is right there in the image -- the crashing frame
// itself sits under it -- so this is our launch line, not the owner's code,
// and the console must offer the start-command lever instead of an
// accusation.
func TestClassifyCrashCauseEntrypointImportIsNotAppCode(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n" +
		"  File \"/app/app/main.py\", line 3, in <module>\n" +
		"    from app.core.config import settings\n" +
		"ModuleNotFoundError: No module named 'app'"
	kind, text := ClassifyCrashCause(excerpt)
	if kind != CauseKindEntrypointImport {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindEntrypointImport, kind)
	}
	if strings.Contains(text, "ошибка в коде приложения") {
		t.Fatalf("entrypoint-import text must not blame the app's code, got %q", text)
	}
	if !strings.Contains(text, "app") {
		t.Fatalf("expected the missing module named in the text, got %q", text)
	}
	if line := ExtractCauseLine(excerpt); !strings.Contains(line, "No module named 'app'") {
		t.Fatalf("expected the import failure line as evidence, got %q", line)
	}
}

// TestClassifyCrashCauseEntrypointImportDottedModule covers the other wording
// CPython uses for the same failure, where the missing name carries the whole
// dotted path: only its root names a real package, and that is what the fix
// text has to talk about.
func TestClassifyCrashCauseEntrypointImportDottedModule(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n" +
		"  File \"/srv/backend/api/server.py\", line 1, in <module>\n" +
		"ImportError: No module named 'backend.api'"
	kind, text := ClassifyCrashCause(excerpt)
	if kind != CauseKindEntrypointImport {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindEntrypointImport, kind)
	}
	if !strings.Contains(text, "backend.main") {
		t.Fatalf("expected the root package in the suggested command, got %q", text)
	}
}

// TestClassifyCrashCauseMissingDependencyStaysAppCode is the control: an
// absent third-party requirement prints the very same exception, and it is
// genuinely the owner's to fix. The package name is not a directory of the
// crashing file's path, which is the whole discriminator, so this must keep
// its app_code verdict.
func TestClassifyCrashCauseMissingDependencyStaysAppCode(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n" +
		"  File \"/app/main.py\", line 1, in <module>\n" +
		"    import fastapi\n" +
		"ModuleNotFoundError: No module named 'fastapi'"
	kind, text := ClassifyCrashCause(excerpt)
	if kind != CauseKindAppCode {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindAppCode, kind)
	}
	if !strings.Contains(text, "ошибка в коде приложения") {
		t.Fatalf("expected the app_code verdict text, got %q", text)
	}
}

// TestClassifyCrashCauseOtherPythonFaultsStayAppCode keeps every genuine
// code fault on the old path: only an import of a package that is present
// beside the entry point may claim our launch line caused it.
func TestClassifyCrashCauseOtherPythonFaultsStayAppCode(t *testing.T) {
	for _, excerpt := range []string{
		"Traceback (most recent call last):\n  File \"/app/app/main.py\", line 7\nSyntaxError: invalid syntax",
		"Traceback (most recent call last):\n  File \"/app/app/main.py\", line 12, in run\nAttributeError: 'NoneType' object has no attribute 'get'",
		"Traceback (most recent call last):\n  File \"/app/worker.py\", line 4, in <module>\nModuleNotFoundError: No module named 'redis'",
	} {
		if kind, _ := ClassifyCrashCause(excerpt); kind != CauseKindAppCode {
			t.Fatalf("excerpt %q must stay app_code, got %q", excerpt, kind)
		}
	}
}

// TestClassifyCrashCausePlatformStillWinsOverEntrypointImport pins the
// ordering: a platform failure wrapped in a traceback that also happens to
// carry an import line must keep naming the platform.
func TestClassifyCrashCausePlatformStillWinsOverEntrypointImport(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n" +
		"  File \"/app/app/main.py\", line 3, in <module>\n" +
		"OSError: [Errno 113] No route to host\n" +
		"ModuleNotFoundError: No module named 'app'"
	if kind, _ := ClassifyCrashCause(excerpt); kind != CauseKindPlatformNetwork {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindPlatformNetwork, kind)
	}
}
