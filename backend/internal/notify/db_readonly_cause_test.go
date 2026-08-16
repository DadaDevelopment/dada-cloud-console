package notify

import (
	"strings"
	"testing"
)

// TestClassifyCrashCauseDBReadOnlyRealCrashLog is the live shape of the
// fonbet-value crash (external user artemmendeleev@gmail.com, 2026-08-16):
// our own quota enforcement flipped default_transaction_read_only on for the
// app's Postgres role after the database crossed its plan's storage limit,
// so every write now fails with psycopg's ReadOnlySqlTransaction wrapped
// inside a normal Python traceback. Before dbReadOnlyCrashSignatures existed
// this matched pythonCrashSignatures' bare "Traceback" entry first and told
// the owner their own code was broken, which is false -- we caused this.
func TestClassifyCrashCauseDBReadOnlyRealCrashLog(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n" +
		"  File \"/app/collector.py\", line 42, in run\n" +
		"    cur.execute(\"INSERT INTO collector_runs (...) VALUES (...)\")\n" +
		"psycopg.errors.ReadOnlySqlTransaction: InternalError: cannot execute INSERT in a read-only transaction\n" +
		"[SQL: INSERT INTO collector_runs (...)]"
	kind, text := ClassifyCrashCause(excerpt)
	if kind != CauseKindDBReadOnly {
		t.Fatalf("expected cause_kind %q, got %q (text=%q)", CauseKindDBReadOnly, kind, text)
	}
	if kind == CauseKindAppCode {
		t.Fatalf("a database read-only refusal caused by our own quota enforcement must never be labelled app_code")
	}
}

// TestClassifyCrashCauseDBReadOnlyEachSignatureMatches pins every pattern
// this classifier relies on: if one stops matching (e.g. a driver changes
// its wording), this test catches the regression instead of the classifier
// silently going quiet on that pattern.
func TestClassifyCrashCauseDBReadOnlyEachSignatureMatches(t *testing.T) {
	for _, pattern := range dbReadOnlyCrashSignatures {
		kind, text := ClassifyCrashCause(pattern)
		if kind != CauseKindDBReadOnly {
			t.Fatalf("pattern %q: expected cause_kind %q, got %q (text=%q)", pattern, CauseKindDBReadOnly, kind, text)
		}
	}
}

// TestClassifyCrashCauseDBReadOnlyTextStaysHonestAboutCertainty guards the
// verdict wording: it must name our own quota enforcement as the usual
// reason without claiming certainty, because a user-configured read-only
// role or a replica connection endpoint produces the identical server
// refusal and the text must stay true in both cases.
func TestClassifyCrashCauseDBReadOnlyTextStaysHonestAboutCertainty(t *testing.T) {
	_, text := ClassifyCrashCause("ReadOnlySqlTransaction")
	if text == "" {
		t.Fatalf("expected non-empty verdict text")
	}
	mustContain := []string{"только для чтения", "лимит", "тариф"}
	for _, want := range mustContain {
		if !strings.Contains(text, want) {
			t.Fatalf("expected verdict text to mention %q, got %q", want, text)
		}
	}
}

// TestClassifyCrashCauseDBReadOnlyDoesNotBreakPlainPythonTraceback is the
// no-regression check: an ordinary Python crash with no read-only line at
// all must still classify app_code exactly as before this change.
func TestClassifyCrashCauseDBReadOnlyDoesNotBreakPlainPythonTraceback(t *testing.T) {
	excerpt := "Traceback (most recent call last):\n  File \"app.py\", line 3\nModuleNotFoundError: No module named 'flask'"
	kind, _ := ClassifyCrashCause(excerpt)
	if kind != CauseKindAppCode {
		t.Fatalf("expected cause_kind %q for a plain python traceback with no read-only line, got %q", CauseKindAppCode, kind)
	}
}

// TestClassifyCrashCauseDBReadOnlyUnrelatedLogReturnsEmpty pins that a log
// with none of the recognized signatures still returns ("", "") rather than
// a false db_read_only classification.
func TestClassifyCrashCauseDBReadOnlyUnrelatedLogReturnsEmpty(t *testing.T) {
	kind, text := ClassifyCrashCause("Listening on port 8080\nConnection refused to database")
	if kind != "" || text != "" {
		t.Fatalf("expected empty classification for an unrelated log, got kind=%q text=%q", kind, text)
	}
}
