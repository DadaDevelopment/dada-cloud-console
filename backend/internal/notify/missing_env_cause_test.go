package notify

import "testing"

// TestClassifyMissingEnvVar_RealCrashLog runs the classifier against the exact
// stdout of a production container that crashlooped 55 times over four hours
// (tvkassistantbot-prod/sevarateambot, 2026-08-16) while the console showed no
// verdict at all. If this stops matching, that user's failure mode is silent
// again.
func TestClassifyMissingEnvVar_RealCrashLog(t *testing.T) {
	const excerpt = "[DEBUG] TOKEN loaded: False\n" +
		"=== КРИТИЧЕСКАЯ ОШИБКА ===\n" +
		"Не найден TELEGRAM_API_TOKEN в переменных окружения\n"

	key, ok := ClassifyMissingEnvVar(excerpt, map[string]string{})
	if !ok {
		t.Fatal("real production crash log was not classified as a missing env var")
	}
	if key != "TELEGRAM_API_TOKEN" {
		t.Fatalf("key = %q, want TELEGRAM_API_TOKEN", key)
	}
}

func TestClassifyMissingEnvVar(t *testing.T) {
	cases := []struct {
		name    string
		excerpt string
		env     map[string]string
		wantKey string
		wantOK  bool
	}{
		{
			name:    "english environment variable not set",
			excerpt: "Error: environment variable BOT_TOKEN is not set\n",
			wantKey: "BOT_TOKEN",
			wantOK:  true,
		},
		{
			name:    "node process env",
			excerpt: "throw new Error('Missing required process.env.STRIPE_SECRET_KEY')\n",
			wantKey: "STRIPE_SECRET_KEY",
			wantOK:  true,
		},
		{
			name:    "key already set is not a missing variable",
			excerpt: "Не найден TELEGRAM_API_TOKEN в переменных окружения\n",
			env:     map[string]string{"TELEGRAM_API_TOKEN": "123:abc"},
			wantOK:  false,
		},
		{
			name:    "key set to an empty value still counts as missing",
			excerpt: "Не найден TELEGRAM_API_TOKEN в переменных окружения\n",
			env:     map[string]string{"TELEGRAM_API_TOKEN": "   "},
			wantKey: "TELEGRAM_API_TOKEN",
			wantOK:  true,
		},
		{
			name:    "missing something that is not configuration",
			excerpt: "ValueError: не найден пользователь с таким ID\n",
			wantOK:  false,
		},
		{
			name:    "log level prefix is never nominated as the key",
			excerpt: "[ERROR] переменная окружения не задана\n",
			wantOK:  false,
		},
		{
			name:    "unrelated crash",
			excerpt: "Traceback (most recent call last):\n  File \"bot.py\", line 4\nAttributeError: module has no attribute\n",
			wantOK:  false,
		},
		{
			name:    "marker and subject on different lines do not combine",
			excerpt: "reading environment\nuser SOME_RECORD not found\n",
			wantOK:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, ok := ClassifyMissingEnvVar(tc.excerpt, tc.env)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (key=%q)", ok, tc.wantOK, key)
			}
			if ok && key != tc.wantKey {
				t.Fatalf("key = %q, want %q", key, tc.wantKey)
			}
		})
	}
}

// TestClassifyMissingEnvVar_LosesToRealTraceback pins the ordering the watcher
// relies on: a Python traceback is already classified as app_code by
// ClassifyCrashCause, and this classifier must not fire on it and overwrite
// that verdict with a fabricated variable name.
func TestClassifyMissingEnvVar_LosesToRealTraceback(t *testing.T) {
	const excerpt = "Traceback (most recent call last):\n" +
		"  File \"/app/bot.py\", line 12, in <module>\n" +
		"    bot.polling()\n" +
		"AttributeError: module 'telebot.util' has no attribute 'message_handler'\n"

	if key, ok := ClassifyMissingEnvVar(excerpt, map[string]string{}); ok {
		t.Fatalf("traceback misclassified as missing env var %q", key)
	}
}
