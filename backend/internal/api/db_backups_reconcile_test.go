package api

import (
	"context"
	"io"
	"testing"
	"time"
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
