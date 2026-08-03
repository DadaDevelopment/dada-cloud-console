package api

import (
	"testing"
	"time"
)

// TestNextMeterDelay_AlignsToWallClock pins the property the 2026-08-03 gap
// needed: whatever minute the process happens to ask at, the next run lands just
// after the top of the next hour, never at "start time + interval".
func TestNextMeterDelay_AlignsToWallClock(t *testing.T) {
	hour := time.Hour
	cases := []struct {
		now  string
		want string
	}{
		{"2026-08-03T12:36:23Z", "2026-08-03T13:00:30Z"},
		{"2026-08-03T12:00:00Z", "2026-08-03T13:00:30Z"},
		{"2026-08-03T12:00:30Z", "2026-08-03T13:00:30Z"},
		{"2026-08-03T12:00:31Z", "2026-08-03T13:00:30Z"},
		{"2026-08-03T12:59:59Z", "2026-08-03T13:00:30Z"},
		{"2026-08-03T23:48:30Z", "2026-08-04T00:00:30Z"},
	}
	for _, tc := range cases {
		now, err := time.Parse(time.RFC3339, tc.now)
		if err != nil {
			t.Fatalf("parse now: %v", err)
		}
		want, err := time.Parse(time.RFC3339, tc.want)
		if err != nil {
			t.Fatalf("parse want: %v", err)
		}
		if got := now.Add(NextMeterDelay(now, hour)); !got.Equal(want) {
			t.Errorf("from %s: next run %s, want %s", tc.now, got.Format(time.RFC3339), tc.want)
		}
	}
}

// TestNextMeterDelay_RestartDoesNotShiftPhase is the regression proper. Two
// replicas that started 12 minutes apart must converge on the same run times;
// under the old ticker they kept their start phase forever, which is how prod
// ended up with rows at :36 from one pod and :48 from the other and nothing at
// all in the hours between the restarts.
func TestNextMeterDelay_RestartDoesNotShiftPhase(t *testing.T) {
	a, _ := time.Parse(time.RFC3339, "2026-08-03T12:36:23Z")
	b, _ := time.Parse(time.RFC3339, "2026-08-03T12:48:30Z")
	if got, want := a.Add(NextMeterDelay(a, time.Hour)), b.Add(NextMeterDelay(b, time.Hour)); !got.Equal(want) {
		t.Fatalf("replicas disagree: %s vs %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// TestNextMeterDelay_SubHourInterval keeps the offset from swallowing intervals
// shorter than it. A five-minute meter must still tick five-minutely, and a
// one-second interval must not be shifted into the next boundary by a 30s lead.
func TestNextMeterDelay_SubHourInterval(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-08-03T12:36:23Z")

	if got, want := now.Add(NextMeterDelay(now, 5*time.Minute)), "2026-08-03T12:40:30Z"; got.Format(time.RFC3339) != want {
		t.Errorf("5m interval: next run %s, want %s", got.Format(time.RFC3339), want)
	}

	d := NextMeterDelay(now, time.Second)
	if d <= 0 || d > time.Second {
		t.Errorf("1s interval: delay %v, want (0, 1s]", d)
	}
}

// TestNextMeterDelay_NonPositiveInterval guards the config path: a misread
// BILLING_METER_INTERVAL_SECS must not spin the loop at zero delay.
func TestNextMeterDelay_NonPositiveInterval(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-08-03T12:36:23Z")
	for _, interval := range []time.Duration{0, -time.Minute} {
		d := NextMeterDelay(now, interval)
		if d <= 0 || d > time.Hour {
			t.Errorf("interval %v: delay %v, want (0, 1h]", interval, d)
		}
	}
}
