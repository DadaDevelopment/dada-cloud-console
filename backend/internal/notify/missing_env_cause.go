package notify

import (
	"regexp"
	"sort"
	"strings"
)

// CauseKindMissingEnvVar is the cause_kind value for a crash whose real root
// is a required environment variable that was never set on the app. The
// container starts, the program reads its config, finds nothing, prints its
// own fatal message and exits -- so no language runtime ever raises, no
// traceback is printed, and every other classifier in this package falls
// through to "" and the console shows the bare kube reason with no verdict
// at all.
//
// Live case 2026-08-16: a returning user connected a Telegram bot repo,
// the build passed in 82 seconds, and the container then crashlooped 55
// times over four hours printing "Не найден TELEGRAM_API_TOKEN в
// переменных окружения". The app had zero env vars set. The console said
// only "CrashLoopBackOff"; the owner looked at the app list twice and left.
//
// Distinct from CauseKindAppCode: the code is fine and the platform is fine,
// the app is simply missing one input, and the fix is a form the console
// already has -- which is why this kind carries the exact key name, so the
// banner can name it and link straight to the env-var editor.
const CauseKindMissingEnvVar = "missing_env_var"

// missingEnvMarkers are the "something is absent" halves of a missing-config
// sentence, matched case-insensitively against a single log line. Alone they
// prove nothing (an app can print "не найден пользователь" about its own
// data), which is why missingEnvSubjects must match on the SAME line.
var missingEnvMarkers = []string{
	"не найден",
	"не задан",
	"не указан",
	"не установлен",
	"не определен",
	"не определён",
	"отсутствует",
	"not set",
	"not defined",
	"not found",
	"is missing",
	"missing required",
	"is required",
	"must be set",
	"required but",
}

// missingEnvSubjects are the "and the absent thing is configuration" halves.
// Requiring one of these on the same line as a missingEnvMarker is what keeps
// this classifier from labelling every "not found" in a log as a missing
// variable -- same bar as platformCrashSignatures: a false "you forgot a
// variable" is as bad as a false "your code is broken", because it sends the
// owner to fill in a form that was never the problem.
var missingEnvSubjects = []string{
	"переменн",
	"окружени",
	"environment variable",
	"env var",
	"environment",
	"os.environ",
	"process.env",
	"getenv",
}

// missingEnvKeyPattern matches an environment-variable-shaped identifier:
// upper-case ASCII letters, digits and underscores, at least four characters
// long. Deliberately ASCII-only, so a Cyrillic word in a Russian fatal
// message can never be mistaken for a key name.
var missingEnvKeyPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{3,}\b`)

// missingEnvKeyStopWords are upper-case words that show up inside fatal
// messages and log prefixes without ever being variable names. Without this
// set, a line like "[ERROR] переменная не найдена" would nominate "ERROR" as
// the missing key and the banner would tell the owner to create a variable
// called ERROR.
var missingEnvKeyStopWords = map[string]bool{
	"ERROR":    true,
	"FATAL":    true,
	"DEBUG":    true,
	"INFO":     true,
	"WARN":     true,
	"WARNING":  true,
	"TRACE":    true,
	"CRITICAL": true,
	"NOTICE":   true,
	"PANIC":    true,
	"NONE":     true,
	"NULL":     true,
	"TRUE":     true,
	"FALSE":    true,
	"MAIN":     true,
	"ROOT":     true,
	"HTTP":     true,
	"HTTPS":    true,
	"JSON":     true,
	"YAML":     true,
	"TODO":     true,
	"NOTE":     true,
}

// ClassifyMissingEnvVar looks at a crashed container's log excerpt together
// with the env vars actually set on the app and decides whether the crash is
// a required variable that was never provided (see CauseKindMissingEnvVar).
//
// It returns ok=false unless ALL of:
//  1. some log line carries both a missingEnvMarker and a missingEnvSubject,
//  2. that same line names an environment-variable-shaped identifier that is
//     not a missingEnvKeyStopWord, and
//  3. that identifier is absent from env.
//
// Condition 3 is what makes the verdict falsifiable rather than a guess: an
// app that prints "DATABASE_URL not set" while DATABASE_URL is in fact set
// is failing for some other reason (a typo'd key it reads under a different
// name, an empty value it validates itself, a startup ordering bug), and
// telling that owner to set a variable they already set would be worse than
// saying nothing. A key set to an empty string counts as absent: from the
// program's point of view an empty token and a missing one fail identically,
// and the fix is the same.
//
// When several candidate keys appear on the line, they are sorted so the
// verdict is stable across ticks rather than flapping with map iteration
// order, matching ClassifyConnectionStringFailure's rationale.
func ClassifyMissingEnvVar(logExcerpt string, env map[string]string) (key string, ok bool) {
	for _, line := range strings.Split(logExcerpt, "\n") {
		lower := strings.ToLower(line)
		if !containsAny(lower, missingEnvMarkers) || !containsAny(lower, missingEnvSubjects) {
			continue
		}
		candidates := make([]string, 0, 2)
		for _, match := range missingEnvKeyPattern.FindAllString(line, -1) {
			if missingEnvKeyStopWords[match] {
				continue
			}
			if strings.TrimSpace(env[match]) != "" {
				continue
			}
			candidates = append(candidates, match)
		}
		if len(candidates) == 0 {
			continue
		}
		sort.Strings(candidates)
		return candidates[0], true
	}
	return "", false
}

// containsAny reports whether haystack contains any of needles. haystack is
// expected to be already lower-cased by the caller; every entry in
// missingEnvMarkers and missingEnvSubjects is written lower-case for that
// reason.
func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}
