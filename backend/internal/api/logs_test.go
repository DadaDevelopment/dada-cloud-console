package api

import (
	"context"
	"errors"
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

// failingClusterSearch stands in for the infra Elasticsearch client when the
// query cannot run at all.
type failingClusterSearch struct{ err error }

func (f failingClusterSearch) Search(context.Context, logsearch.SearchOpts) (*logsearch.SearchResult, error) {
	return nil, f.err
}

// TestClusterAppLogsSaysSoWhenTheStreamWasNotSearched pins the difference
// between an app that logged nothing and a search that never ran. For a k8s app
// the cluster stream is the only one carrying its lines, so a swallowed failure
// here reaches the caller as {"entries":[],"total":0} — a broken query read as
// silence.
func TestClusterAppLogsSaysSoWhenTheStreamWasNotSearched(t *testing.T) {
	ctx := context.Background()
	opts := logsearch.SearchOpts{KubeApp: "lead-gen"}

	cases := []struct {
		name       string
		namespaces []string
		nsErr      error
		searcher   clusterLogSearcher
		wantNote   bool
	}{
		{
			name:     "namespaces could not be resolved",
			nsErr:    errors.New("connection refused"),
			searcher: failingClusterSearch{err: errors.New("never reached")},
			wantNote: true,
		},
		{
			name:       "the search itself failed",
			namespaces: []string{"leadgen-prod"},
			searcher:   failingClusterSearch{err: errors.New("elasticsearch search: status 503")},
			wantNote:   true,
		},
		{
			name:     "a VM-only app has no cluster namespaces",
			searcher: failingClusterSearch{err: errors.New("never reached")},
			wantNote: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, note := clusterAppLogs(ctx, tc.searcher, tc.namespaces, tc.nsErr, opts)
			if res != nil {
				t.Errorf("result = %+v, want nil — nothing was searched", res)
			}
			if tc.wantNote && note == "" {
				t.Error("note is empty — the caller cannot tell a failed search from a quiet app")
			}
			if !tc.wantNote && note != "" {
				t.Errorf("note = %q, want none — a VM-only app is not a degradation", note)
			}
		})
	}
}
