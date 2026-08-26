package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSupportIntakeTitleTakesFirstLine(t *testing.T) {
	got := supportIntakeTitle("Payments fail on checkout\nMore details below.")
	if got != "Payments fail on checkout" {
		t.Fatalf("got %q", got)
	}
}

func TestSupportIntakeTitleBoundsLength(t *testing.T) {
	long := strings.Repeat("x", supportIntakeTitleMaxLen+50)
	got := supportIntakeTitle(long)
	if len(got) != supportIntakeTitleMaxLen {
		t.Fatalf("len=%d want %d", len(got), supportIntakeTitleMaxLen)
	}
}

func TestSupportIntakeTitleFallsBackWhenBlank(t *testing.T) {
	if got := supportIntakeTitle("   \n rest"); got != "Support ticket" {
		t.Fatalf("got %q want fallback", got)
	}
}

func TestDispatchSupportIntakeNoopsWhenUnconfigured(t *testing.T) {
	h := &Handler{}
	// Must not panic or block: supportTask is nil, so this is a pure no-op.
	h.dispatchSupportIntake(uuid.New(), "some report", "user@example.com", "web")
}
