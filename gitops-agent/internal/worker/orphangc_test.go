package worker

import (
	"testing"
	"time"
)

func TestGCDecide(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	mark := time.Hour
	purge := 24 * time.Hour
	old := now.Add(-2 * time.Hour)
	fresh := now.Add(-10 * time.Minute)
	longOrphan := now.Add(-48 * time.Hour)
	recentOrphan := now.Add(-2 * time.Hour)

	cases := []struct {
		name          string
		liveBacked    bool
		gitBacked     bool
		gitVerifiable bool
		phase         string
		lastSynced    time.Time
		orphanedAt    *time.Time
		want          gcAction
	}{
		{"live pod keeps app", true, false, true, "Ready", old, nil, gcNone},
		{"git-backed keeps app even with no pod", false, true, true, "Pending", old, nil, gcNone},
		{"git unverifiable + no pod = leave alone", false, false, false, "Unknown", old, nil, gcNone},
		{"dead but fresh = wait for mark grace", false, false, true, "Unknown", fresh, nil, gcNone},
		{"dead and stale = mark", false, false, true, "Unknown", old, nil, gcMark},
		{"orphaned but recently = wait for purge grace", false, false, true, "Orphaned", old, &recentOrphan, gcNone},
		{"orphaned long enough = purge", false, false, true, "Orphaned", old, &longOrphan, gcPurge},
		{"orphaned with nil stamp never purges", false, false, true, "Orphaned", old, nil, gcNone},
		{"orphaned app came back via pod = clear", true, false, true, "Orphaned", old, &longOrphan, gcClear},
		{"orphaned app came back via git = clear", false, true, true, "Orphaned", old, &longOrphan, gcClear},
		{"orphaned but git unverifiable + no pod = hold", false, false, false, "Orphaned", old, &longOrphan, gcNone},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gcDecide(c.liveBacked, c.gitBacked, c.gitVerifiable, c.phase,
				c.lastSynced, c.orphanedAt, now, mark, purge)
			if got != c.want {
				t.Fatalf("gcDecide = %v, want %v", got, c.want)
			}
		})
	}
}
