package api

import "testing"

// The cutoff column decides which rows leave. A table with both an insertion
// time and a mutation time must archive by the one that never moves: picking
// updated_at would delete rows whose "age" is a function of when they were last
// touched, which is not what the owner agreed to.
func TestPickCutoffColumn_PrefersInsertionTime(t *testing.T) {
	cols := []archiveColumn{
		{Name: "id", Type: "bigint", NotNull: true, Indexed: true, Position: 1},
		{Name: "updated_at", Type: "timestamp with time zone", NotNull: true, Indexed: true, Position: 2},
		{Name: "created_at", Type: "timestamp with time zone", NotNull: true, Indexed: true, Position: 3},
	}
	got, reason := pickCutoffColumn(cols)
	if reason != "" {
		t.Fatalf("unexpected rejection: %s", reason)
	}
	if got.Name != "created_at" {
		t.Errorf("picked %q, want created_at", got.Name)
	}
}

// Indexing outranks the name convention: an unindexed created_at turns the
// archive job's delete into a full scan of the table it is trying to shrink,
// while an indexed event_time keeps it a range scan.
func TestPickCutoffColumn_IndexOutranksName(t *testing.T) {
	cols := []archiveColumn{
		{Name: "created_at", Type: "timestamp with time zone", NotNull: true, Position: 1},
		{Name: "event_time", Type: "timestamp with time zone", NotNull: true, Indexed: true, Position: 2},
	}
	got, _ := pickCutoffColumn(cols)
	if got.Name != "event_time" {
		t.Errorf("picked %q, want the indexed event_time", got.Name)
	}
}

// A table with no wall-clock column cannot be archived by date, and must say so
// rather than fall back onto a synthetic id that has no defensible mapping onto
// a date the user picked.
func TestPickCutoffColumn_RejectsWithoutTimestamp(t *testing.T) {
	cols := []archiveColumn{
		{Name: "id", Type: "bigint", NotNull: true, Indexed: true, Position: 1},
		{Name: "payload", Type: "jsonb", Position: 2},
	}
	got, reason := pickCutoffColumn(cols)
	if reason == "" {
		t.Fatalf("expected a rejection, got column %q", got.Name)
	}
}

// The same table must always produce the same plan: a user reads the preview,
// then runs the job, and the two must agree on which column was meant.
func TestPickCutoffColumn_StableOnTies(t *testing.T) {
	cols := []archiveColumn{
		{Name: "seen_at", Type: "timestamp with time zone", NotNull: true, Indexed: true, Position: 4},
		{Name: "fetched_at", Type: "timestamp with time zone", NotNull: true, Indexed: true, Position: 2},
	}
	first, _ := pickCutoffColumn(cols)
	for i := 0; i < 20; i++ {
		got, _ := pickCutoffColumn(cols)
		if got.Name != first.Name {
			t.Fatalf("plan is not stable: %q then %q", first.Name, got.Name)
		}
	}
	if first.Name != "fetched_at" {
		t.Errorf("tie broke on %q, want the earlier column fetched_at", first.Name)
	}
}

// date columns qualify: a daily rollup table keyed by date is exactly the shape
// this feature is for.
func TestPickCutoffColumn_AcceptsDate(t *testing.T) {
	cols := []archiveColumn{{Name: "day", Type: "date", NotNull: true, Indexed: true, Position: 1}}
	got, reason := pickCutoffColumn(cols)
	if reason != "" || got.Name != "day" {
		t.Errorf("date column rejected: %q reason=%q", got.Name, reason)
	}
}

func TestEstimateArchiveBytes(t *testing.T) {
	cases := []struct {
		name                        string
		rows, totalRows, totalBytes int64
		want                        int64
	}{
		{"half the rows frees about half the bytes", 50, 100, 1000, 500},
		{"archiving everything frees everything", 100, 100, 1000, 1000},
		{"a cutoff past the newest row cannot free more than the table", 500, 100, 1000, 1000},
		{"nothing to archive frees nothing", 0, 100, 1000, 0},
		{"an empty table cannot be divided by", 10, 0, 1000, 0},
		{"a table with no size on record estimates nothing", 10, 100, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := estimateArchiveBytes(c.rows, c.totalRows, c.totalBytes); got != c.want {
				t.Errorf("estimateArchiveBytes(%d, %d, %d) = %d, want %d",
					c.rows, c.totalRows, c.totalBytes, got, c.want)
			}
		})
	}
}
