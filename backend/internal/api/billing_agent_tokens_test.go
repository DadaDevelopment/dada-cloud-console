package api

import (
	"testing"
	"time"
)

func TestCurrentBillingMonthUTC(t *testing.T) {
	now := time.Date(2026, 7, 25, 13, 44, 9, 0, time.UTC)
	from, to := currentBillingMonthUTC(now)

	wantFrom := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if !from.Equal(wantFrom) {
		t.Fatalf("from = %s, want %s", from, wantFrom)
	}
	if !to.Equal(wantTo) {
		t.Fatalf("to = %s, want %s", to, wantTo)
	}
	if !now.Before(to) || now.Before(from) {
		t.Fatalf("now %s must fall inside [%s, %s)", now, from, to)
	}
}

func TestCurrentBillingMonthUTC_DecemberRollsToNextYear(t *testing.T) {
	now := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	from, to := currentBillingMonthUTC(now)
	if from.Year() != 2026 || from.Month() != time.December {
		t.Fatalf("from = %s, want 2026-12-01", from)
	}
	if to.Year() != 2027 || to.Month() != time.January {
		t.Fatalf("to = %s, want 2027-01-01", to)
	}
}

func TestRound4(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0.123456, 0.1235},
		{0.00004, 0.0000},
		{1.99999, 2.0},
		{0, 0},
	}
	for _, tc := range cases {
		if got := round4(tc.in); got != tc.want {
			t.Fatalf("round4(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
