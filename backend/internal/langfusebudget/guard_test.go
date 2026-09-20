package langfusebudget

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/langfuse"
)

type fakeCounter struct {
	perView map[string]int64
	err     error
	calls   int
}

func (f *fakeCounter) Count(_ context.Context, view string, _, _ time.Time, filters ...langfuse.MetricFilter) (int64, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	if len(filters) > 0 {
		return f.perView["roots"], nil
	}
	return f.perView[view], nil
}

func TestRefreshSumsUnitsAndFlipsVerdict(t *testing.T) {
	fc := &fakeCounter{perView: map[string]int64{langfuse.ViewObservations: 700, "roots": 100, langfuse.ViewScoresNumeric: 600, langfuse.ViewScoresBoolean: 50, langfuse.ViewScoresCategorical: 0}}
	g := &Guard{counter: fc, dayLimit: 1500, monthLimit: 45000, now: time.Now}
	g.Refresh(context.Background())
	if g.Exceeded() {
		t.Fatalf("1450 units must stay under 1500")
	}
	fc.perView[langfuse.ViewScoresNumeric] = 700
	g.Refresh(context.Background())
	if !g.Exceeded() {
		t.Fatalf("1550 units must exceed 1500")
	}
}

func TestRefreshKeepsVerdictWhenLangfuseFails(t *testing.T) {
	fc := &fakeCounter{perView: map[string]int64{langfuse.ViewObservations: 2000}}
	g := &Guard{counter: fc, dayLimit: 1500, monthLimit: 45000, now: time.Now}
	g.Refresh(context.Background())
	if !g.Exceeded() {
		t.Fatalf("expected exceeded")
	}
	fc.err = errors.New("429")
	g.Refresh(context.Background())
	if !g.Exceeded() {
		t.Fatalf("a failed read must not lift the verdict")
	}
}

func TestUnreadGuardHoldsBackUntilFirstRead(t *testing.T) {
	fc := &fakeCounter{perView: map[string]int64{langfuse.ViewObservations: 10}, err: errors.New("429")}
	g := &Guard{counter: fc, dayLimit: 1500, monthLimit: 45000, now: time.Now}
	if !g.Exceeded() {
		t.Fatal("a guard that never read the usage must hold back")
	}
	if g.Refresh(context.Background()) {
		t.Fatal("a 429 read must report failure")
	}
	if !g.Exceeded() {
		t.Fatal("a failed first read must keep holding back")
	}
	fc.err = nil
	if !g.Refresh(context.Background()) {
		t.Fatal("a clean read must report success")
	}
	if g.Exceeded() {
		t.Fatal("10 units under 1500 must lift the hold")
	}
}

func TestNilGuardNeverExceeds(t *testing.T) {
	var g *Guard
	if g.Exceeded() {
		t.Fatal("nil guard must be a no-op")
	}
	if New(langfuse.New("", "", "", true)) != nil {
		t.Fatal("unconfigured client must yield nil guard")
	}
}
