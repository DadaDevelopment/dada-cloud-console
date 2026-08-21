package api

import "testing"

func strp(s string) *string { return &s }

func TestFailureSignature_EmptyWhenNoData(t *testing.T) {
	if sig := failureSignature(nil, nil); sig != "" {
		t.Fatalf("signature = %q, want empty", sig)
	}
	if sig := failureSignature(nil, strp("   ")); sig != "" {
		t.Fatalf("signature = %q, want empty for blank message", sig)
	}
}

func TestFailureSignature_StripsFailReasonPrefix(t *testing.T) {
	a := failureSignature(strp("dockerfile_build_failed"), strp("dockerfile_build_failed: npm install exited 1"))
	b := failureSignature(strp("dockerfile_build_failed"), strp("npm install exited 1"))
	if a != b {
		t.Fatalf("signatures differ after stripping prefix: %q vs %q", a, b)
	}
}

func TestCountFailureRepeat_SingleFailure(t *testing.T) {
	history := []buildFailureRecord{
		{Status: "failed", FailReason: strp("dockerfile_build_failed"), ErrorMessage: strp("dockerfile_build_failed: npm install exited 1")},
	}
	if got := countFailureRepeat(history); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}

func TestCountFailureRepeat_ThreeIdenticalInARow(t *testing.T) {
	rec := buildFailureRecord{Status: "failed", FailReason: strp("dockerfile_build_failed"), ErrorMessage: strp("dockerfile_build_failed: npm install exited 1")}
	history := []buildFailureRecord{rec, rec, rec}
	if got := countFailureRepeat(history); got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}
}

func TestCountFailureRepeat_SuccessInMiddleBreaksStreak(t *testing.T) {
	rec := buildFailureRecord{Status: "failed", FailReason: strp("dockerfile_build_failed"), ErrorMessage: strp("dockerfile_build_failed: npm install exited 1")}
	history := []buildFailureRecord{rec, {Status: "success"}, rec}
	if got := countFailureRepeat(history); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}

func TestCountFailureRepeat_DifferentFailReasonBreaksStreak(t *testing.T) {
	history := []buildFailureRecord{
		{Status: "failed", FailReason: strp("dockerfile_build_failed"), ErrorMessage: strp("dockerfile_build_failed: npm install exited 1")},
		{Status: "failed", FailReason: strp("git_auth_failed"), ErrorMessage: strp("git_auth_failed: could not read Username")},
	}
	if got := countFailureRepeat(history); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}

func TestCountFailureRepeat_SameFailReasonDifferentDetailBreaksStreak(t *testing.T) {
	history := []buildFailureRecord{
		{Status: "failed", FailReason: strp("dockerfile_build_failed"), ErrorMessage: strp("dockerfile_build_failed: npm install exited 1")},
		{Status: "failed", FailReason: strp("dockerfile_build_failed"), ErrorMessage: strp("dockerfile_build_failed: tsc exited 2")},
	}
	if got := countFailureRepeat(history); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}

func TestCountFailureRepeat_EmptySignatureNeverMatches(t *testing.T) {
	history := []buildFailureRecord{
		{Status: "failed"},
		{Status: "failed"},
	}
	if got := countFailureRepeat(history); got != 1 {
		t.Fatalf("count = %d, want 1 (empty signatures must not merge)", got)
	}
}

func TestCountFailureRepeat_NonFailedCurrentReportsZero(t *testing.T) {
	history := []buildFailureRecord{{Status: "success"}}
	if got := countFailureRepeat(history); got != 0 {
		t.Fatalf("count = %d, want 0 for a non-failed current build", got)
	}
}

// TestCountFailureRepeat_RealNpmLogTimestampsStillCount uses the three literal
// error_message values persisted for tarotreaderhimu@gmail.com's app
// best-marriage-astrologer-in-guwahati on 2026-08-21. They differ only in the
// npm debug-log timestamp, so a raw string comparison reports no repeat at all.
func TestCountFailureRepeat_RealNpmLogTimestampsStillCount(t *testing.T) {
	reason := "dockerfile_build_failed"
	messages := []string{
		"dockerfile_build_failed: [build 5/6] RUN npm install: npm error A complete log of this run can be found in: /root/.npm/_logs/2026-08-21T14_09_09_964Z-debug-0.log",
		"dockerfile_build_failed: [build 5/6] RUN npm install: npm error A complete log of this run can be found in: /root/.npm/_logs/2026-08-21T14_03_12_528Z-debug-0.log",
		"dockerfile_build_failed: [build 5/6] RUN npm install: npm error A complete log of this run can be found in: /root/.npm/_logs/2026-08-21T13_59_54_133Z-debug-0.log",
	}
	history := make([]buildFailureRecord, 0, len(messages))
	for i := range messages {
		history = append(history, buildFailureRecord{Status: "failed", FailReason: &reason, ErrorMessage: &messages[i]})
	}
	if got := countFailureRepeat(history); got != 3 {
		t.Errorf("count = %d, want 3 for three real npm failures differing only by log timestamp", got)
	}
}

// TestFailureSignature_DifferentCausesStayDifferent guards the normalizer
// against erasing so much of the message that unrelated failures collapse into
// one streak.
func TestFailureSignature_DifferentCausesStayDifferent(t *testing.T) {
	reason := "dockerfile_build_failed"
	npm := "dockerfile_build_failed: [build 5/6] RUN npm install: npm error missing script"
	gomod := "dockerfile_build_failed: [build 3/6] RUN go build: package foo is not in std"
	if failureSignature(&reason, &npm) == failureSignature(&reason, &gomod) {
		t.Error("two different failure details must not share a signature")
	}
}
