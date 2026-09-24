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
	if New(langfuse.New("", "", "", true), nil) != nil {
		t.Fatal("unconfigured client must yield nil guard")
	}
}

type memStore struct {
	r     Reading
	ok    bool
	saves int
}

func (m *memStore) Load(context.Context, string) (Reading, bool, error) { return m.r, m.ok, nil }

func (m *memStore) Save(_ context.Context, _ string, r Reading) error {
	m.r, m.ok = r, true
	m.saves++
	return nil
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestFreshStoredReadingLiftsHoldWithoutReading(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	fc := &fakeCounter{err: errors.New("must not be called")}
	st := &memStore{r: Reading{Day: 100, Month: 20000, ReadAt: now.Add(-time.Hour)}, ok: true}
	g := &Guard{counter: fc, store: st, dayLimit: 1500, monthLimit: 45000, poll: 3 * time.Hour, now: fixedClock(now)}
	next := g.restore(context.Background())
	if g.Exceeded() {
		t.Fatal("a fresh stored reading under budget must lift the hold")
	}
	if fc.calls != 0 {
		t.Fatalf("restart spent %d metrics requests, want 0", fc.calls)
	}
	if want := now.Add(2 * time.Hour); !next.Equal(want) {
		t.Fatalf("next read at %v, want %v (stored read + poll)", next, want)
	}
}

func TestStoredReadingOverBudgetMutesAtOnce(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	st := &memStore{r: Reading{Day: 1600, Month: 20000, ReadAt: now.Add(-time.Hour)}, ok: true}
	g := &Guard{counter: &fakeCounter{}, store: st, dayLimit: 1500, monthLimit: 45000, poll: 3 * time.Hour, now: fixedClock(now)}
	g.restore(context.Background())
	if !g.Exceeded() {
		t.Fatal("a stored reading over the day budget must mute after a restart")
	}
}

func TestStaleStoredReadingKeepsHoldAndReadsNow(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	st := &memStore{r: Reading{Day: 1, Month: 1, ReadAt: now.Add(-31 * time.Hour)}, ok: true}
	g := &Guard{counter: &fakeCounter{}, store: st, dayLimit: 1500, monthLimit: 45000, poll: 3 * time.Hour, now: fixedClock(now)}
	next := g.restore(context.Background())
	if !g.Exceeded() {
		t.Fatal("a reading older than maxSavedAge must not lift the hold")
	}
	if !next.Equal(now) {
		t.Fatalf("next read at %v, want now", next)
	}
}

func TestYesterdaysDayUnitsDoNotMuteToday(t *testing.T) {
	now := time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC)
	st := &memStore{r: Reading{Day: 2000, Month: 20000, ReadAt: time.Date(2026, 9, 23, 23, 0, 0, 0, time.UTC)}, ok: true}
	g := &Guard{counter: &fakeCounter{}, store: st, dayLimit: 1500, monthLimit: 45000, poll: 3 * time.Hour, now: fixedClock(now)}
	g.restore(context.Background())
	if g.Exceeded() {
		t.Fatal("yesterday's day units must not mute a new day")
	}
}

func TestLastMonthsUnitsDoNotMuteNewMonth(t *testing.T) {
	now := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	st := &memStore{r: Reading{Day: 100, Month: 46000, ReadAt: time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)}, ok: true}
	g := &Guard{counter: &fakeCounter{}, store: st, dayLimit: 1500, monthLimit: 45000, poll: 3 * time.Hour, now: fixedClock(now)}
	g.restore(context.Background())
	if g.Exceeded() {
		t.Fatal("last month's units must not mute a new month")
	}
}

func TestRefreshStoresTheReading(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	st := &memStore{}
	g := &Guard{counter: &fakeCounter{perView: map[string]int64{langfuse.ViewObservations: 70, "roots": 10}}, store: st, dayLimit: 1500, monthLimit: 45000, poll: 3 * time.Hour, now: fixedClock(now)}
	if !g.Refresh(context.Background()) {
		t.Fatal("clean read must succeed")
	}
	if st.saves != 1 || st.r.Day != 80 || st.r.Month != 80 || !st.r.ReadAt.Equal(now) {
		t.Fatalf("stored %+v after %d saves", st.r, st.saves)
	}
}

func TestRateLimitWaitsForTheWindowToReopen(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	reset := now.Add(20 * time.Hour)
	fc := &fakeCounter{err: &langfuse.RateLimitedError{Path: "/api/public/v2/metrics", ResetAt: reset}}
	g := &Guard{counter: fc, dayLimit: 1500, monthLimit: 45000, poll: 3 * time.Hour, now: fixedClock(now)}
	if g.Refresh(context.Background()) {
		t.Fatal("a 429 must fail the read")
	}
	if fc.calls != 1 {
		t.Fatalf("a 429 read went on for %d requests, want 1", fc.calls)
	}
	if got, want := g.retryWait(), 20*time.Hour+resetSlack; got != want {
		t.Fatalf("retry wait %v, want %v", got, want)
	}
	fc.err = errors.New("dial tcp: connection refused")
	g.Refresh(context.Background())
	if got := g.retryWait(); got != retryPoll {
		t.Fatalf("a non-429 failure must retry after %v, got %v", retryPoll, got)
	}
}

func TestFutureStoredReadingIsNotTrusted(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	st := &memStore{r: Reading{Day: 1, Month: 1, ReadAt: now.Add(2 * time.Hour)}, ok: true}
	g := &Guard{counter: &fakeCounter{}, store: st, dayLimit: 1500, monthLimit: 45000, poll: 3 * time.Hour, now: fixedClock(now)}
	if next := g.restore(context.Background()); !next.Equal(now) || !g.Exceeded() {
		t.Fatalf("a reading from the future must be ignored: next %v, exceeded %v", next, g.Exceeded())
	}
}

func TestDailyRequestBudget(t *testing.T) {
	fc := &fakeCounter{perView: map[string]int64{}}
	g := &Guard{counter: fc, dayLimit: 1500, monthLimit: 45000, now: time.Now}
	g.Refresh(context.Background())
	perRead := int64(fc.calls)
	reads := int64(24 * time.Hour / defaultPoll)
	if reads*perRead > 80 {
		t.Fatalf("default poll spends %d of Langfuse's 100 daily metrics requests", reads*perRead)
	}
}
