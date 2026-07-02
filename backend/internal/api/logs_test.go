package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/logsearch"
)

func TestMergeLogResults(t *testing.T) {
	user := &logsearch.SearchResult{
		Total: 2,
		Entries: []logsearch.LogEntry{
			{Timestamp: "2026-06-04T00:00:03Z", Message: "vm-new"},
			{Timestamp: "2026-06-04T00:00:01Z", Message: "vm-old"},
		},
	}
	infra := &logsearch.SearchResult{
		Total: 1,
		Entries: []logsearch.LogEntry{
			{Timestamp: "2026-06-04T00:00:02Z", Message: "pod-mid"},
		},
	}

	got := mergeLogResults(user, infra, 200)
	if got.Total != 3 || len(got.Entries) != 3 {
		t.Fatalf("total=%d entries=%d, want 3/3", got.Total, len(got.Entries))
	}
	// Newest-first interleaving across the two streams.
	for i, want := range []string{"vm-new", "pod-mid", "vm-old"} {
		if got.Entries[i].Message != want {
			t.Errorf("entry[%d] = %q, want %q", i, got.Entries[i].Message, want)
		}
	}
	// Inputs are not mutated (merge copies before sorting).
	if user.Entries[0].Message != "vm-new" || len(user.Entries) != 2 {
		t.Errorf("user result mutated: %+v", user.Entries)
	}

	capped := mergeLogResults(user, infra, 2)
	if len(capped.Entries) != 2 || capped.Total != 3 {
		t.Errorf("cap: entries=%d total=%d, want 2/3", len(capped.Entries), capped.Total)
	}
}
