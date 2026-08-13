package cliapp

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/cli/internal/apiclient"
)

func ptr(s string) *string { return &s }

// TestPlatformFailureDoesNotBlameTheUser covers the real run from 2026-08-13:
// the platform's Jenkins could not check out its own pipeline library, and the
// CLI told the owner only that the build "failed".
func TestPlatformFailureDoesNotBlameTheUser(t *testing.T) {
	got := explainBuildFailure(apiclient.Build{
		Status:       "failed",
		FailReason:   ptr("platform_error"),
		ErrorMessage: ptr("platform_error: ERROR: Maximum checkout retry attempts reached, aborting"),
	})
	if !strings.Contains(got, "на нашей стороне") {
		t.Fatalf("platform failure must say it is ours: %q", got)
	}
	if !strings.Contains(got, "Maximum checkout retry attempts reached") {
		t.Fatalf("the console line is the evidence and must survive: %q", got)
	}
	if strings.Contains(got, "platform_error: ") {
		t.Fatalf("the machine code is printed twice: %q", got)
	}
}

func TestExplainBuildFailureFallsBackToStatus(t *testing.T) {
	got := explainBuildFailure(apiclient.Build{Status: "canceled"})
	if !strings.Contains(got, "canceled") {
		t.Fatalf("unknown reason should still name the status: %q", got)
	}
}

func TestBuildConsoleURLPointsAtTheBuildPage(t *testing.T) {
	cfg := Config{APIBase: "https://console.dada-tuda.ru/api/v1"}
	got := buildConsoleURL(cfg, "p-1", "genagent", "b-9")
	want := "https://console.dada-tuda.ru/projects/p-1/apps/genagent/builds/b-9"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestDescribeDetectionDoesNotReadAsFailure guards the line the owner saw:
// "Фреймворк: не определён (порт 0)" looked like a broken deploy even though
// the pipeline detects again after unpacking.
func TestDescribeDetectionDoesNotReadAsFailure(t *testing.T) {
	got := describeDetection("", 0)
	if !strings.Contains(got, "сборщик") {
		t.Fatalf("empty detection must name who decides next: %q", got)
	}
	if strings.Contains(got, "порт 0") {
		t.Fatalf("port 0 is an internal value, not a message: %q", got)
	}
	if got := describeDetection("python", 0); !strings.Contains(got, "python") {
		t.Fatalf("known framework must be named: %q", got)
	}
	if got := describeDetection("nextjs", 3000); !strings.Contains(got, "3000") {
		t.Fatalf("known port must be shown: %q", got)
	}
}
