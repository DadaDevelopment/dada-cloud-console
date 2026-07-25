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
