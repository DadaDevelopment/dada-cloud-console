package api

import (
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
)

func TestDueGraceStage(t *testing.T) {
	graceUntil := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return graceUntil.Add(-d) }
	ptr := func(tm time.Time) *time.Time { return &tm }

	cases := []struct {
		name       string
		notifiedAt *time.Time
		now        time.Time
		wantDays   int
		wantDue    bool
	}{
		{"far out, silent", nil, at(60 * 24 * time.Hour), 0, false},
		{"30d window opens", nil, at(29 * 24 * time.Hour), 30, true},
		{"30d already sent", ptr(at(29 * 24 * time.Hour)), at(20 * 24 * time.Hour), 0, false},
		{"7d after 30d sent", ptr(at(29 * 24 * time.Hour)), at(6 * 24 * time.Hour), 7, true},
		{"7d already sent", ptr(at(6 * 24 * time.Hour)), at(5 * 24 * time.Hour), 0, false},
		{"final day", ptr(at(6 * 24 * time.Hour)), at(12 * time.Hour), 1, true},
		{"final already sent", ptr(at(12 * time.Hour)), at(2 * time.Hour), 0, false},
		{"late backfill gets one notice", nil, at(2 * 24 * time.Hour), 7, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := graceAccount{OrgID: "org", GraceUntil: graceUntil, NotifiedAt: tc.notifiedAt}
			stage, ok := dueGraceStage(a, tc.now)
			if ok != tc.wantDue {
				t.Fatalf("due=%v, want %v", ok, tc.wantDue)
			}
			if !ok {
				return
			}
			if days := int(stage / (24 * time.Hour)); days != tc.wantDays {
				t.Fatalf("stage=%dd, want %dd", days, tc.wantDays)
			}
		})
	}
}

func TestGraceDaysLeft(t *testing.T) {
	graceUntil := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		remaining time.Duration
		want      int
	}{
		{30 * 24 * time.Hour, 30},
		{6*24*time.Hour + 5*time.Hour, 7},
		{25 * time.Hour, 2},
		{time.Hour, 1},
		{0, 1},
	}
	for _, tc := range cases {
		if got := graceDaysLeft(graceUntil, graceUntil.Add(-tc.remaining)); got != tc.want {
			t.Fatalf("remaining=%s: days=%d, want %d", tc.remaining, got, tc.want)
		}
	}
}

func TestFreePlanOf(t *testing.T) {
	plans := []pricing.Plan{{Key: "startup"}, {Key: "free"}, {Key: "business"}}
	p, ok := freePlanOf(plans)
	if !ok || p.Key != "free" {
		t.Fatalf("got %q ok=%v, want free", p.Key, ok)
	}
	if _, ok := freePlanOf([]pricing.Plan{{Key: "startup"}}); ok {
		t.Fatal("catalog without a free plan must not resolve one")
	}
}
