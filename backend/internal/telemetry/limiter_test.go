package telemetry

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewIngestLimiterDefault(t *testing.T) {
	if l := NewIngestLimiter(0); l.perMin != 120 {
		t.Errorf("default perMin = %d, want 120", l.perMin)
	}
	if l := NewIngestLimiter(-5); l.perMin != 120 {
		t.Errorf("negative perMin = %d, want 120", l.perMin)
	}
	if l := NewIngestLimiter(60); l.perMin != 60 {
		t.Errorf("perMin = %d, want 60", l.perMin)
	}
}

// Allow drains the burst then blocks, and is isolated per app id.
func TestIngestLimiterAllow(t *testing.T) {
	l := NewIngestLimiter(60)
	app := uuid.New()
	for i := 0; i < 60; i++ {
		if !l.Allow(app) {
			t.Fatalf("request %d unexpectedly blocked within burst", i)
		}
	}
	if l.Allow(app) {
		t.Fatal("request past burst should be blocked")
	}
	if !l.Allow(uuid.New()) {
		t.Fatal("a different app must have its own bucket")
	}
}
