package notify

import (
	"strings"
	"testing"
)

func TestClassifyCrashLogPython(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n  File \"app.py\", line 3\nModuleNotFoundError: No module named 'flask'"
	got := ClassifyCrashLog(excerpt)
	if got == "" {
		t.Fatalf("expected a python hint, got empty string")
	}
	if !strings.Contains(got, "Python") {
		t.Fatalf("expected hint to mention Python, got %q", got)
	}
}

func TestClassifyCrashLogNode(t *testing.T) {
	excerpt := "internal/modules/cjs/loader.js:1078\nError: Cannot find module 'express'"
	got := ClassifyCrashLog(excerpt)
	if got == "" {
		t.Fatalf("expected a node hint, got empty string")
	}
	if !strings.Contains(got, "Node.js") {
		t.Fatalf("expected hint to mention Node.js, got %q", got)
	}
}

func TestClassifyCrashLogGoPanic(t *testing.T) {
	excerpt := "panic: runtime error: index out of range [3] with length 3\n\ngoroutine 1 [running]:"
	got := ClassifyCrashLog(excerpt)
	if got == "" {
		t.Fatalf("expected a panic hint, got empty string")
	}
}

func TestClassifyCrashLogNoMatch(t *testing.T) {
	excerpt := "Listening on port 8080\nConnection refused to database"
	if got := ClassifyCrashLog(excerpt); got != "" {
		t.Fatalf("expected empty hint for unrecognized log, got %q", got)
	}
}

func TestClassifyCrashLogEmpty(t *testing.T) {
	if got := ClassifyCrashLog(""); got != "" {
		t.Fatalf("expected empty hint for empty excerpt, got %q", got)
	}
}

func TestExtractCauseLine(t *testing.T) {
	tests := []struct {
		name    string
		excerpt string
		want    string
	}{
		{
			name:    "python picks the exception line, not the traceback header",
			excerpt: "Traceback (most recent call last):\n  File \"app.py\", line 3, in <module>\nModuleNotFoundError: No module named 'flask'",
			want:    "ModuleNotFoundError: No module named 'flask'",
		},
		{
			name:    "node picks the error line",
			excerpt: "internal/modules/cjs/loader.js:1078\nError: Cannot find module 'express'",
			want:    "Error: Cannot find module 'express'",
		},
		{
			name:    "go panic",
			excerpt: "starting up\npanic: runtime error: index out of range [3] with length 3\n\ngoroutine 1 [running]:",
			want:    "panic: runtime error: index out of range [3] with length 3",
		},
		{
			name:    "last matching line wins when several signatures appear",
			excerpt: "NameError: first\nsome noise\nAttributeError: second and final",
			want:    "AttributeError: second and final",
		},
		{
			name:    "no known signature never returns a guess",
			excerpt: "Listening on port 8080\nConnection refused to database",
			want:    "",
		},
		{
			name:    "empty excerpt",
			excerpt: "",
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractCauseLine(tt.excerpt); got != tt.want {
				t.Fatalf("ExtractCauseLine(%q) = %q, want %q", tt.excerpt, got, tt.want)
			}
		})
	}
}

func TestExtractCauseLineTruncatesByRunesNotBytes(t *testing.T) {
	long := "AttributeError: " + strings.Repeat("о", 400)
	got := ExtractCauseLine(long)
	runes := []rune(got)
	if len(runes) != causeLineMaxRunes {
		t.Fatalf("expected truncation to %d runes, got %d runes (%q)", causeLineMaxRunes, len(runes), got)
	}
	if !strings.HasPrefix(got, "AttributeError: ") {
		t.Fatalf("expected truncated line to keep its prefix, got %q", got)
	}
}

func TestComposeAppAlertIncludesHintWhenPresent(t *testing.T) {
	_, body := ComposeAppAlert("web", "CrashLoopBackOff", "web-abc12", "panic: boom", "https://console.dada-tuda.ru/projects/p1/apps/web", "Судя по логам, это похоже на ошибку в коде приложения.", "https://console.dada-tuda.ru/projects/p1/apps/web#agent")
	if !strings.Contains(body, "Судя по логам, это похоже на ошибку в коде приложения.") {
		t.Fatalf("expected body to contain code hint, got: %s", body)
	}
}

func TestComposeAppAlertOmitsHintWhenEmpty(t *testing.T) {
	_, body := ComposeAppAlert("web", "OOMKilled", "web-abc12", "", "https://console.dada-tuda.ru/projects/p1/apps/web", "", "https://console.dada-tuda.ru/projects/p1/apps/web#agent")
	if strings.Contains(body, "Судя по логам") {
		t.Fatalf("expected body to omit code hint line, got: %s", body)
	}
}

func TestComposeAppAlertIncludesAgentURL(t *testing.T) {
	agentURL := "https://console.dada-tuda.ru/projects/p1/apps/web#agent"
	_, body := ComposeAppAlert("web", "CrashLoopBackOff", "web-abc12", "some log", "https://console.dada-tuda.ru/projects/p1/apps/web", "", agentURL)
	if !strings.Contains(body, agentURL) {
		t.Fatalf("expected body to contain agent URL %q, got: %s", agentURL, body)
	}
	if !strings.Contains(body, "AI-агента") {
		t.Fatalf("expected body to mention the AI agent, got: %s", body)
	}
}

func TestComposeFeedbackLeadsWithTheMessage(t *testing.T) {
	subject, body := ComposeFeedback("dev@example.com", "org-1", "/projects/p1/apps/web", "не могу забрать файлы", "web", "https://console.dada-tuda.ru/admin/feedback")
	if !strings.HasPrefix(body, "не могу забрать файлы") {
		t.Fatalf("expected the body to open with the customer's words, got: %s", body)
	}
	if !strings.Contains(subject, "dev@example.com") {
		t.Fatalf("expected the sender in the subject, got: %s", subject)
	}
	if !strings.Contains(body, "web") || !strings.Contains(body, "/admin/feedback") {
		t.Fatalf("expected app name and admin link in body, got: %s", body)
	}
}

func TestComposeFeedbackAnonymousSender(t *testing.T) {
	subject, body := ComposeFeedback("", "", "/pricing", "дорого", "", "https://console.dada-tuda.ru/admin/feedback")
	if !strings.Contains(subject, "аноним") {
		t.Fatalf("expected an anonymous sender label in subject, got: %s", subject)
	}
	if strings.Contains(body, "Приложение:") {
		t.Fatalf("expected no app line when the route names none, got: %s", body)
	}
}

func TestComposeAutofixReadySaysNothingWasDeployed(t *testing.T) {
	prURL := "https://github.com/acme/web/pull/7"
	subject, body := ComposeAutofixReady("web", prURL, "https://console.dada-tuda.ru/projects/p1/apps/web")
	if !strings.Contains(body, prURL) {
		t.Fatalf("expected the PR link in body, got: %s", body)
	}
	if !strings.Contains(body, "Ничего не задеплоено") {
		t.Fatalf("expected the body to state nothing was deployed, got: %s", body)
	}
	if !strings.Contains(subject, "web") {
		t.Fatalf("expected the app name in subject, got: %s", subject)
	}
}

func TestComposeAutofixFailedCarriesTheReason(t *testing.T) {
	subject, body := ComposeAutofixFailed("web", "Command not found on PATH: git", "https://console.dada-tuda.ru/projects/p1/apps/web")
	if !strings.Contains(body, "Command not found on PATH: git") {
		t.Fatalf("expected the failure reason in body, got: %s", body)
	}
	if !strings.Contains(subject, "АВТОФИКС УПАЛ") {
		t.Fatalf("expected an operator-facing subject, got: %s", subject)
	}
}

func TestComposeAutofixFailedOmitsEmptyReason(t *testing.T) {
	_, body := ComposeAutofixFailed("web", "", "https://console.dada-tuda.ru/projects/p1/apps/web")
	if strings.Contains(body, "Причина:") {
		t.Fatalf("expected no reason line when none is known, got: %s", body)
	}
}
