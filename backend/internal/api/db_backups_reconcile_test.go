package api

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
)

type recordingDBBackupPresigner struct {
	enabled bool
	calls   []struct {
		prefix    string
		olderThan time.Duration
	}
}

func (p *recordingDBBackupPresigner) Enabled() bool { return p.enabled }

func (p *recordingDBBackupPresigner) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}

func (p *recordingDBBackupPresigner) PutObject(context.Context, string, io.Reader, int64, string) error {
	return nil
}

func (p *recordingDBBackupPresigner) DeleteOldObjects(_ context.Context, prefix string, olderThan time.Duration) (int, error) {
	p.calls = append(p.calls, struct {
		prefix    string
		olderThan time.Duration
	}{prefix, olderThan})
	return 0, nil
}

func TestSweepVolumeExports_Disabled_Skips(t *testing.T) {
	lastVolumeExportSweep = time.Time{}
	p := &recordingDBBackupPresigner{enabled: false}
	h := &Handler{dbBackupPresigner: p}

	h.sweepVolumeExports(context.Background())

	if len(p.calls) != 0 {
		t.Fatalf("expected no DeleteOldObjects call when presigner disabled, got %d", len(p.calls))
	}
}

func TestSweepVolumeExports_Enabled_CallsWithVolexportsPrefixAnd24h(t *testing.T) {
	lastVolumeExportSweep = time.Time{}
	p := &recordingDBBackupPresigner{enabled: true}
	h := &Handler{dbBackupPresigner: p}

	h.sweepVolumeExports(context.Background())

	if len(p.calls) != 1 {
		t.Fatalf("expected exactly 1 DeleteOldObjects call, got %d", len(p.calls))
	}
	if p.calls[0].prefix != "volexports/" {
		t.Errorf("prefix = %q, want %q", p.calls[0].prefix, "volexports/")
	}
	if p.calls[0].olderThan != 24*time.Hour {
		t.Errorf("olderThan = %v, want 24h", p.calls[0].olderThan)
	}
}

func TestSweepVolumeExports_Throttled_SecondImmediateCallSkips(t *testing.T) {
	lastVolumeExportSweep = time.Time{}
	p := &recordingDBBackupPresigner{enabled: true}
	h := &Handler{dbBackupPresigner: p}

	h.sweepVolumeExports(context.Background())
	h.sweepVolumeExports(context.Background())

	if len(p.calls) != 1 {
		t.Fatalf("expected throttled second call to be a no-op, got %d calls", len(p.calls))
	}
}

func TestBackupIntervalForFrequency(t *testing.T) {
	cases := []struct {
		freq string
		want time.Duration
	}{
		{"@hourly", time.Hour},
		{"@daily", 24 * time.Hour},
		{"@weekly", 7 * 24 * time.Hour},
		{"@monthly", 30 * 24 * time.Hour},
		{"@yearly", 365 * 24 * time.Hour},
		{"daily", 24 * time.Hour},
		{"weekly", 7 * 24 * time.Hour},
		{"WEEKLY", 7 * 24 * time.Hour},
		{"  @weekly  ", 7 * 24 * time.Hour},
		{"", 24 * time.Hour},
		{"nonsense", 24 * time.Hour},
	}
	for _, tc := range cases {
		if got := backupIntervalForFrequency(tc.freq); got != tc.want {
			t.Errorf("backupIntervalForFrequency(%q) = %v, want %v", tc.freq, got, tc.want)
		}
	}
}

func TestServiceDatabaseBackupFrequency(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		want    string
	}{
		{
			name:    "present",
			summary: `{"spec":{"backup":{"enabled":true,"frequency":"weekly"}}}`,
			want:    "weekly",
		},
		{
			name:    "absent backup block",
			summary: `{"spec":{"database":"recog"}}`,
			want:    "",
		},
		{
			name:    "absent spec",
			summary: `{"kind":"ServiceDatabaseV2"}`,
			want:    "",
		},
		{
			name:    "malformed json",
			summary: `not json`,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serviceDatabaseBackupFrequency([]byte(tc.summary)); got != tc.want {
				t.Errorf("serviceDatabaseBackupFrequency(%s) = %q, want %q", tc.summary, got, tc.want)
			}
		})
	}
}

func namedCandidate(name string, lastBackupAt *time.Time) scheduledBackupCandidate {
	return scheduledBackupCandidate{
		projectID:    uuid.New(),
		envID:        uuid.New(),
		name:         name,
		database:     name,
		frequency:    "@daily",
		lastBackupAt: lastBackupAt,
	}
}

func daysAgo(now time.Time, days int) *time.Time {
	t := now.AddDate(0, 0, -days)
	return &t
}

func TestDueScheduledBackups_NoFreeSlots_StartsNothing(t *testing.T) {
	now := time.Now()
	candidates := []scheduledBackupCandidate{
		namedCandidate("a", nil),
		namedCandidate("b", nil),
		namedCandidate("c", daysAgo(now, 30)),
	}

	for _, freeSlots := range []int{0, -1, -5} {
		got := dueScheduledBackups(candidates, freeSlots, now)
		if len(got) != 0 {
			t.Errorf("freeSlots=%d: expected 0 started, got %d (%v)", freeSlots, len(got), got)
		}
	}
}

func TestDueScheduledBackups_StartsExactlyFreeSlots_LongestWaitingFirst(t *testing.T) {
	now := time.Now()
	candidates := []scheduledBackupCandidate{
		namedCandidate("stale", daysAgo(now, 2)),
		namedCandidate("fresh", daysAgo(now, 0)),
		namedCandidate("old", daysAgo(now, 30)),
		namedCandidate("never", nil),
	}

	got := dueScheduledBackups(candidates, 1, now)
	if len(got) != 1 || got[0].name != "never" {
		t.Fatalf("freeSlots=1: got %v, want exactly [never]", names(got))
	}

	got = dueScheduledBackups(candidates, 2, now)
	if len(names(got)) != 2 || names(got)[0] != "never" || names(got)[1] != "old" {
		t.Fatalf("freeSlots=2: got %v, want [never old]", names(got))
	}

	got = dueScheduledBackups(candidates, 10, now)
	if len(got) != 3 {
		t.Fatalf("freeSlots=10: expected all 3 due candidates (fresh excluded), got %v", names(got))
	}
	for _, c := range got {
		if c.name == "fresh" {
			t.Errorf("freeSlots=10: fresh is within its @daily interval and must not be started")
		}
	}
}

func TestDueScheduledBackups_NullsFirst_ThenByName(t *testing.T) {
	now := time.Now()
	candidates := []scheduledBackupCandidate{
		namedCandidate("zeta", daysAgo(now, 30)),
		namedCandidate("bravo", nil),
		namedCandidate("alpha", nil),
	}

	got := dueScheduledBackups(candidates, 10, now)
	want := []string{"alpha", "bravo", "zeta"}
	if got := names(got); !equalNames(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func names(cs []scheduledBackupCandidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.name
	}
	return out
}

func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFailStuckBackups_OnlyPastTimeout(t *testing.T) {
	pool := testVolumeExportPool(t)
	ctx := context.Background()
	h := &Handler{pool: pool}

	projectID, envID, _ := seedVolumeExportApp(t, pool, "")
	stuckID, freshID := uuid.New(), uuid.New()
	insert := func(id uuid.UUID, age time.Duration) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO db_backups (id, project_id, environment_id, resource_name, database_name,
			     dump_path, status, kind, created_at)
			 VALUES ($1, $2, $3, 'stuck-test', 'stuck-test', 'dumps/stuck-test.dump', 'Running', $4, NOW() - $5::interval)`,
			id, projectID, envID, models.DBBackupKindScheduled, age.String()); err != nil {
			t.Fatalf("seed backup: %v", err)
		}
	}
	insert(stuckID, stuckBackupTimeout+time.Hour)
	insert(freshID, time.Minute)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM db_backups WHERE project_id = $1`, projectID)
	})

	h.failStuckBackups(ctx)

	status := func(id uuid.UUID) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, `SELECT status FROM db_backups WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatalf("read status: %v", err)
		}
		return s
	}
	if got := status(stuckID); got != "Failed" {
		t.Fatalf("stuck backup status = %q, want Failed", got)
	}
	if got := status(freshID); got != "Running" {
		t.Fatalf("fresh backup status = %q, want Running", got)
	}
}
