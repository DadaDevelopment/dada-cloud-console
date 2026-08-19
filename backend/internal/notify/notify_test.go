package notify

import (
	"io"
	"mime/quotedprintable"
	"strings"
	"testing"
	"time"
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

func TestClassifyCrashCausePlatformNetworkInsidePythonTraceback(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n" +
		"  File \"/app/db.py\", line 14, in connect\n" +
		"    conn = psycopg.connect(dsn)\n" +
		"psycopg.OperationalError: connection to server at \"10.96.137.111\", port 5432 failed: No route to host\n" +
		"\tIs the server running on that host and accepting TCP/IP connections?"
	kind, text := ClassifyCrashCause(excerpt)
	if kind != CauseKindPlatformNetwork {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindPlatformNetwork, kind)
	}
	if strings.Contains(text, "это ошибка в коде приложения") {
		t.Fatalf("platform_network text must not blame the app's code, got %q", text)
	}
	if !strings.Contains(text, "платформы") {
		t.Fatalf("expected platform_network text to name our platform, got %q", text)
	}
	if line := ExtractCauseLine(excerpt); !strings.Contains(line, "No route to host") {
		t.Fatalf("expected ExtractCauseLine to surface the No route to host line, got %q", line)
	}
}

func TestClassifyCrashCausePlainAppCodeStillAppCode(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n  File \"app.py\", line 3\nModuleNotFoundError: No module named 'flask'"
	kind, text := ClassifyCrashCause(excerpt)
	if kind != CauseKindAppCode {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindAppCode, kind)
	}
	if !strings.Contains(text, "коде приложения") {
		t.Fatalf("expected app_code text to blame the app's code, got %q", text)
	}
}

func TestClassifyCrashCauseEnospc(t *testing.T) {
	excerpt := "OSError: [Errno 28] no space left on device"
	kind, text := ClassifyCrashCause(excerpt)
	if kind != CauseKindPlatformStorage {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindPlatformStorage, kind)
	}
	if strings.Contains(text, "это ошибка в коде приложения") {
		t.Fatalf("platform_storage text must not blame the app's code, got %q", text)
	}
	if line := ExtractCauseLine(excerpt); !strings.Contains(line, "no space left on device") {
		t.Fatalf("expected ExtractCauseLine to surface the ENOSPC line, got %q", line)
	}
}

func TestClassifyCrashCauseWithReasonOOMKilledOverridesNodeSignature(t *testing.T) {
	excerpt := "Error: Cannot find module '/tmp/index.js'\nnode:internal/modules/cjs/loader:1080"
	kind, text := ClassifyCrashCauseWithReason("OOMKilled", excerpt)
	if kind != CauseKindResourceLimit {
		t.Fatalf("expected cause_kind %q for an OOMKilled reason even with a Node.js-looking log, got %q", CauseKindResourceLimit, kind)
	}
	if strings.Contains(text, "это ошибка в коде") {
		t.Fatalf("resource_limit text must not blame the app's code, got %q", text)
	}
	if strings.Contains(text, "сбой") || strings.Contains(text, "проблем") {
		t.Fatalf("resource_limit text must not read as a platform bug either (it is a plan limit, not a fault), got %q", text)
	}
	if !strings.Contains(text, "лимит") {
		t.Fatalf("expected resource_limit text to name the memory limit, got %q", text)
	}
}

func TestClassifyCrashCauseWithReasonNonOOMDelegatesUnchanged(t *testing.T) {
	excerpt := "Error: Cannot find module '/tmp/index.js'"
	wantKind, wantText := ClassifyCrashCause(excerpt)
	gotKind, gotText := ClassifyCrashCauseWithReason("CrashLoopBackOff", excerpt)
	if gotKind != wantKind || gotText != wantText {
		t.Fatalf("expected a non-OOMKilled reason to behave exactly like ClassifyCrashCause, got kind=%q text=%q want kind=%q text=%q",
			gotKind, gotText, wantKind, wantText)
	}
	if gotKind != CauseKindAppCode {
		t.Fatalf("sanity check failed: expected %q for a plain Node.js signature, got %q", CauseKindAppCode, gotKind)
	}
}

// TestClassifyCrashCauseWithReasonImagePullBackOffIsPlatformRegistry
// reproduces the smart-tender-ai-site incident (2026-08-13): an app stuck in
// ImagePullBackOff has no container that ever ran, so its log excerpt is
// always empty. Before this fix ClassifyCrashCauseWithReason only special-
// cased "OOMKilled" and delegated everything else, including
// ImagePullBackOff, to ClassifyCrashCause(""), which returns ("", "") --
// the owner alert would then carry no cause_kind at all, reading exactly
// like an unclassified app-code crash instead of naming the registry as the
// point of failure.
func TestClassifyCrashCauseWithReasonImagePullBackOffIsPlatformRegistry(t *testing.T) {
	for _, reason := range []string{"ImagePullBackOff", "ErrImagePull"} {
		t.Run(reason, func(t *testing.T) {
			kind, text := ClassifyCrashCauseWithReason(reason, "")
			if kind != CauseKindPlatformRegistry {
				t.Fatalf("ClassifyCrashCauseWithReason(%q, \"\") kind = %q, want %q -- an image-pull failure must never be classified as the app's own code", reason, kind, CauseKindPlatformRegistry)
			}
			if strings.Contains(text, "код приложения") {
				t.Fatalf("platform_registry text must not blame the app's own code, got %q", text)
			}
			if !strings.Contains(text, "не ошибка в вашем коде") {
				t.Fatalf("expected platform_registry text to explicitly clear the app's own code, got %q", text)
			}
			if !strings.Contains(text, "реестр") {
				t.Fatalf("expected platform_registry text to name the registry as the point of failure, got %q", text)
			}
		})
	}
}

func TestComposeAppAlertImagePullBackOffDoesNotClaimTheAppIsRestarting(t *testing.T) {
	for _, reason := range []string{"ImagePullBackOff", "ErrImagePull"} {
		t.Run(reason, func(t *testing.T) {
			_, body := ComposeAppAlert("web", reason, "web-abc12", "", "https://console.dada-tuda.ru/projects/p1/apps/web", "", "https://console.dada-tuda.ru/projects/p1/apps/web#agent")
			if strings.Contains(body, "перезапускается") {
				t.Fatalf("a container stuck in %s never started, so the alert must not claim it is restarting; got: %s", reason, body)
			}
			if !strings.Contains(body, "реестра") {
				t.Fatalf("expected the %s alert to name the registry as the mechanism, got: %s", reason, body)
			}
		})
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
			name:    "unlisted exception type is still found by traceback shape",
			excerpt: "loading model\nTraceback (most recent call last):\n  File \"/app/infer.py\", line 41, in <module>\n    load_model()\n  File \"/app/infer.py\", line 22, in load_model\n    raise RuntimeError(msg)\nRuntimeError: no objects found under 's3://models/buffalo_l' - check MODEL_S3_URI",
			want:    "RuntimeError: no objects found under 's3://models/buffalo_l' - check MODEL_S3_URI",
		},
		{
			name:    "bare traceback header is never the cause",
			excerpt: "starting\nTraceback (most recent call last):\n  File \"/app/bot.py\", line 9, in <module>",
			want:    "",
		},
		{
			name:    "chained traceback reports the final exception",
			excerpt: "Traceback (most recent call last):\n  File \"a.py\", line 1, in <module>\nKeyError: 'token'\n\nDuring handling of the above exception, another exception occurred:\n\nTraceback (most recent call last):\n  File \"b.py\", line 2, in <module>\nSystemExit: 1",
			want:    "SystemExit: 1",
		},
		{
			name:    "unindented app output after a traceback is not mistaken for the cause",
			excerpt: "Traceback (most recent call last):\n  File \"a.py\", line 1, in <module>\nImportError: cannot import name 'x'\nshutting down worker pool now",
			want:    "ImportError: cannot import name 'x'",
		},
		{
			name:    "live case: telebot attribute error",
			excerpt: "Traceback (most recent call last):\n  File \"/app/main.py\", line 12, in <module>\n    @bot.message_handler(commands=['start'])\nAttributeError: module 'telebot.util' has no attribute 'message_handler'",
			want:    "AttributeError: module 'telebot.util' has no attribute 'message_handler'",
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
		{
			name: "H08: chained sqlalchemy traceback picks the signature line, not the trailing decoy",
			excerpt: "Traceback (most recent call last):\n" +
				"  File \"/app/db.py\", line 12, in write\n" +
				"    cur.execute(query)\n" +
				"psycopg.errors.ReadOnlySqlTransaction: cannot execute INSERT in a read-only transaction\n" +
				"\n" +
				"The above exception was the direct cause of the following exception:\n" +
				"\n" +
				"Traceback (most recent call last):\n" +
				"  File \"/app/db.py\", line 40, in write\n" +
				"    raise RuntimeError(\"db write failed\") from exc\n" +
				"RuntimeError: db write failed\n" +
				"[SQL: INSERT INTO events (id) VALUES (%(id)s)]\n" +
				"[parameters: {'id': 501}]\n" +
				"(Background on this error at: https://sqlalche.me/e/20/2j85)",
			want: "psycopg.errors.ReadOnlySqlTransaction: cannot execute INSERT in a read-only transaction",
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

// TestExtractCauseLineAgreesWithClassifyCrashCause is the direct regression
// test for H08: a cause_line that does not contain the signature which
// produced cause_kind is worse than no cause_line at all, because the owner
// sees a verdict with unrelated "evidence" under it. Live case was
// fonbet-value: cause_kind=db_read_only (matched on an inner psycopg line of
// a chained SQLAlchemy traceback) paired with a cause_line pulled from a
// later, unrelated line by the old python-traceback-shape-first heuristic.
// The second half of this test is the other pole: an unlisted exception type
// with no matching signature must still fall back to the python traceback
// shape and return a meaningful line, not empty -- the signature-first
// change must not regress that path.
func TestExtractCauseLineAgreesWithClassifyCrashCause(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n" +
		"  File \"/app/db.py\", line 12, in write\n" +
		"    cur.execute(query)\n" +
		"psycopg.errors.ReadOnlySqlTransaction: cannot execute INSERT in a read-only transaction\n" +
		"\n" +
		"The above exception was the direct cause of the following exception:\n" +
		"\n" +
		"Traceback (most recent call last):\n" +
		"  File \"/app/db.py\", line 40, in write\n" +
		"    raise RuntimeError(\"db write failed\") from exc\n" +
		"RuntimeError: db write failed\n" +
		"[SQL: INSERT INTO events (id) VALUES (%(id)s)]\n" +
		"[parameters: {'id': 501}]\n" +
		"(Background on this error at: https://sqlalche.me/e/20/2j85)"

	kind, _ := ClassifyCrashCause(excerpt)
	if kind != CauseKindDBReadOnly {
		t.Fatalf("test setup broken: expected cause_kind=%s, got %q", CauseKindDBReadOnly, kind)
	}
	line := ExtractCauseLine(excerpt)
	if !strings.Contains(line, "ReadOnlySqlTransaction") && !strings.Contains(line, "read-only transaction") {
		t.Fatalf("cause_line %q does not contain the signature that produced cause_kind=%s", line, kind)
	}

	plain := "loading model\nTraceback (most recent call last):\n  File \"/app/infer.py\", line 41, in <module>\n    load_model()\nRuntimeError: no objects found under 's3://models/buffalo_l' - check MODEL_S3_URI"
	plainKind, _ := ClassifyCrashCause(plain)
	if plainKind != CauseKindAppCode {
		t.Fatalf("test setup broken: expected cause_kind=%s (matched on the bare traceback header, since RuntimeError itself is not in any signature table), got %q", CauseKindAppCode, plainKind)
	}
	plainLine := ExtractCauseLine(plain)
	want := "RuntimeError: no objects found under 's3://models/buffalo_l' - check MODEL_S3_URI"
	if plainLine != want {
		t.Fatalf("fallback regressed: ExtractCauseLine(unlisted exception) = %q, want %q", plainLine, want)
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

func TestReactivationHTMLCarriesTheBannerAndSurvivesWithoutIt(t *testing.T) {
	withHero := ComposeReactivationHTML("Startup", 30, "https://console.example/promo/tok", "", "https://console.example/email/hero-reactivation.png")
	if !strings.Contains(withHero, `src="https://console.example/email/hero-reactivation.png"`) {
		t.Fatalf("banner missing from html body: %s", withHero)
	}
	if !strings.Contains(withHero, `alt="Startup на 30 дней бесплатно"`) {
		t.Fatalf("banner alt text must repeat the offer: %s", withHero)
	}
	if !strings.Contains(withHero, "тариф Startup на 30 дней бесплатно") {
		t.Fatalf("offer must also be in the text of the body, not only in the picture")
	}

	noHero := ComposeReactivationHTML("Startup", 30, "https://console.example/promo/tok", "", "")
	if strings.Contains(noHero, "<img") {
		t.Fatalf("empty hero URL must write no image at all: %s", noHero)
	}
	if !strings.Contains(noHero, "тариф Startup на 30 дней бесплатно") {
		t.Fatalf("letter without a banner must still carry the offer")
	}
}

func TestReactivationFixHTMLCarriesTheBanner(t *testing.T) {
	got := ComposeReactivationFixHTML("Startup", "05.09.2026", "https://console.example/promo/tok", "", "https://console.example/email/hero-git-url.png")
	if !strings.Contains(got, `src="https://console.example/email/hero-git-url.png"`) {
		t.Fatalf("banner missing from fix-wave body: %s", got)
	}
	if !strings.Contains(got, "ссылку на репозиторий") {
		t.Fatalf("fix-wave body must state what changed: %s", got)
	}
}

// TestRenderKeepsLinesWithinSMTPLimit feeds the shape that actually broke
// delivery: a crash excerpt whose traceback is one unwrapped line. RFC 5321
// caps a line at 1000 octets and Postbox enforced it with 500 "Line too long",
// killing nine of ten operator-fallback alerts in 30 days.
func TestRenderKeepsLinesWithinSMTPLimit(t *testing.T) {
	n := New("smtp.example.com", 587, "u", "p", "from@example.com")
	body := "Приложение упало.\n\n" + strings.Repeat("Traceback фрагмент без переносов ", 200) + "\n\nСсылка: https://example.com\n"

	msg := n.render("to@example.com", "Тема", body)

	for i, line := range strings.Split(msg, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line %d is %d octets, SMTP allows 998", i, len(line))
		}
	}
}

// TestRenderBodySurvivesEncoding pins that the reader still sees the original
// letter: soft line breaks are the transport's business, not the text's, and a
// Cyrillic rune must never be split by the wrapping.
func TestRenderBodySurvivesEncoding(t *testing.T) {
	n := New("smtp.example.com", 587, "u", "p", "from@example.com")
	body := "Сборка приложения shop завершилась.\n" + strings.Repeat("длинная строка ", 100) + "\nконец\n"

	msg := n.render("to@example.com", "Тема", body)
	_, encoded, ok := strings.Cut(msg, "\r\n\r\n")
	if !ok {
		t.Fatal("message has no body")
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(encoded)))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := strings.ReplaceAll(string(decoded), "\r\n", "\n"); got != body {
		t.Fatalf("body changed in transit:\ngot  %q\nwant %q", got, body)
	}
}

// TestRenderAlternativeKeepsLinesWithinSMTPLimit covers the campaign path: the
// HTML part carries markup on one line far more often than a plain body does.
func TestRenderAlternativeKeepsLinesWithinSMTPLimit(t *testing.T) {
	n := New("smtp.example.com", 587, "u", "p", "from@example.com")
	text := strings.Repeat("текстовая часть ", 200)
	html := "<html><body>" + strings.Repeat("<p>абзац письма</p>", 300) + "</body></html>"

	msg := n.renderAlternative("to@example.com", "Тема", text, html)

	for i, line := range strings.Split(msg, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line %d is %d octets, SMTP allows 998", i, len(line))
		}
	}
}

// TestClassifyCrashCauseArgparseUsageIsNeedsArgs is the live shape of
// 2026-08-13: an uploaded archive whose entrypoint is an argparse CLI. The
// container starts, the program refuses an empty command line, kube reports
// the bare reason "Error", and before this classification the console showed
// the owner a permanent crashloop with no verdict at all.
func TestClassifyCrashCauseArgparseUsageIsNeedsArgs(t *testing.T) {
	excerpt := "usage: agent.py [-h] --surname SURNAME --place PLACE\n" +
		"agent.py: error: the following arguments are required: --surname, --place"
	kind, text := ClassifyCrashCause(excerpt)
	if kind != CauseKindNeedsArgs {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindNeedsArgs, kind)
	}
	if strings.Contains(text, "ошибка в коде приложения") {
		t.Fatalf("needs-args text must not blame the app's code, got %q", text)
	}
	if line := ExtractCauseLine(excerpt); !strings.Contains(line, "the following arguments are required") {
		t.Fatalf("expected the parser's own line as evidence, got %q", line)
	}
}

// TestClassifyCrashCauseClickMissingOptionBeatsTraceback pins the ordering:
// click prints a traceback-free usage error, but typer and some wrappers print
// the missing-option line after a traceback header. The parser's verdict is
// the specific one and must win over the generic "python died".
func TestClassifyCrashCauseClickMissingOptionBeatsTraceback(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n" +
		"  File \"/app/cli.py\", line 9, in <module>\n" +
		"Error: Missing option '--token'."
	if kind, _ := ClassifyCrashCause(excerpt); kind != CauseKindNeedsArgs {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindNeedsArgs, kind)
	}
}

// TestClassifyCrashCauseOrdinaryAppOutputIsNotNeedsArgs keeps the signature
// table honest: a false "your app is a CLI" sends the owner to change the one
// thing that was never wrong.
func TestClassifyCrashCauseOrdinaryAppOutputIsNotNeedsArgs(t *testing.T) {
	for _, excerpt := range []string{
		"INFO: parsed the following arguments from config: retries=3",
		"Traceback (most recent call last):\nValueError: bad payload",
	} {
		if kind, _ := ClassifyCrashCause(excerpt); kind == CauseKindNeedsArgs {
			t.Fatalf("excerpt %q must not classify as needs-args", excerpt)
		}
	}
}

func TestComposeDatabaseQuotaGraceEndingCountsDownAndOffersEveryWayOut(t *testing.T) {
	until := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	subject, body := ComposeDatabaseQuotaGraceEnding("odds-research", 1.4, 1, until, 6, "https://console.example/db")
	if !strings.Contains(subject, "6 ч") {
		t.Fatalf("subject must carry the hours left, got %q", subject)
	}
	if !strings.Contains(body, "15.08.2026 09:00 UTC") {
		t.Fatalf("body must state the deadline, got %q", body)
	}
	for _, want := range []string{"1.4", "Parquet", "Резервные копии"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body must mention %q, got %q", want, body)
		}
	}
}

func TestComposeDatabaseArchiveDoneTellsTheOwnerHowToReadItBack(t *testing.T) {
	_, body := ComposeDatabaseArchiveDone("odds-research", "public.events", "01.02.2026", 1234567, 2.5,
		"s3://dada-archive-1111/events/2026-02-01.parquet", true, "https://console.example/db")
	for _, want := range []string{"s3://dada-archive-1111/events/2026-02-01.parquet", "read_parquet", "01.02.2026"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body must mention %q, got %q", want, body)
		}
	}
}

func TestComposeDatabaseArchiveDoneSeparatesAutomaticFromRequested(t *testing.T) {
	_, auto := ComposeDatabaseArchiveDone("db", "public.events", "01.02.2026", 10, 1, "s3://b/o", true, "")
	_, manual := ComposeDatabaseArchiveDone("db", "public.events", "01.02.2026", 10, 1, "s3://b/o", false, "")
	if auto == manual {
		t.Fatal("an archive the platform started must not read like one the owner asked for")
	}
}

// TestClassifyCrashCauseEnospcCapitalized pins the ENOSPC verdict against the
// capitalization the C library — and therefore CPython, Node and Go's os
// package — actually prints. strerror(ENOSPC) is "No space left on device"
// with a capital N, so a case-sensitive strings.Contains against the
// lowercase pattern matched nothing on the only shape this error takes in a
// real container log.
//
// Live case 2026-08-19: fonbet-value (a 20Gi volume at 100%) crashlooped for
// five days printing
// "OSError: [Errno 28] No space left on device: '/data/raw_data/...'" and
// carried cause_kind NULL the whole time, so the console banner — which
// renders nothing when cause_kind is empty — stayed blank for the owner while
// the one condition the platform can actually fix went unnamed.
func TestClassifyCrashCauseEnospcCapitalized(t *testing.T) {
	excerpt := "OSError: [Errno 28] No space left on device: " +
		"'/data/raw_data/bodies/sha256/02/.02856a032fd267c3.json.gz.yl6nf1nn'"
	kind, text := ClassifyCrashCause(excerpt)
	if kind != CauseKindPlatformStorage {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindPlatformStorage, kind)
	}
	if strings.Contains(text, "это ошибка в коде приложения") {
		t.Fatalf("platform_storage text must not blame the app's code, got %q", text)
	}
	if line := ExtractCauseLine(excerpt); !strings.Contains(line, "No space left on device") {
		t.Fatalf("expected cause line to carry the ENOSPC evidence, got %q", line)
	}
}

// TestClassifyCrashCauseEioCapitalized is the same guarantee for the volume's
// other failure mode. "Input/output error" is already stored capitalized, so
// this passes today; it is pinned so a future case-normalization of the
// signature table cannot silently regress the EIO verdict that the 2026-07-21
// incident added.
func TestClassifyCrashCauseEioCapitalized(t *testing.T) {
	excerpt := "OSError: [Errno 5] Input/output error"
	kind, _ := ClassifyCrashCause(excerpt)
	if kind != CauseKindPlatformStorage {
		t.Fatalf("expected cause_kind %q, got %q", CauseKindPlatformStorage, kind)
	}
}
