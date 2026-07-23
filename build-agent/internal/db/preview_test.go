package db

import (
	"strings"
	"testing"
)

// TestPreviewDatabaseName mirrors gitops-agent's
// TestPreviewDatabaseName (gitops-agent/internal/renderer/preview_database_test.go)
// with the identical input/output pairs - the two implementations must never
// disagree about what a preview's database is called.
func TestPreviewDatabaseName(t *testing.T) {
	cases := []struct {
		database string
		pr       int
		want     string
	}{
		{"odds-research", 7, "odds-research-pr7"},
		{"Odds_Research", 12, "odds-research-pr12"},
		{strings.Repeat("a", 80), 3, strings.Repeat("a", 63-len("-pr3")) + "-pr3"},
	}
	for _, c := range cases {
		got := previewDatabaseName(c.database, c.pr)
		if got != c.want {
			t.Errorf("previewDatabaseName(%q, %d) = %q, want %q", c.database, c.pr, got, c.want)
		}
		if len(got) > 63 {
			t.Errorf("previewDatabaseName(%q, %d) = %q, exceeds 63 bytes", c.database, c.pr, got)
		}
	}
}

// TestRewriteDatabaseNames covers the DATABASE_URL rewrite in isolation
// (decrypt/encrypt is exercised separately by the crypto package): a matching
// "/<old>" path segment is rewritten to "/<new>" whether or not a query
// string follows, a value naming some other database is left untouched, and
// an empty rewrites map is a no-op.
func TestRewriteDatabaseNames(t *testing.T) {
	rewrites := map[string]string{"odds-research": "odds-research-pr7"}

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{
			"with query string",
			"postgresql://svc-fonbet-db:s3cr3t@postgresql.databases.svc.cluster.local:5432/odds-research?sslmode=disable",
			"postgresql://svc-fonbet-db:s3cr3t@postgresql.databases.svc.cluster.local:5432/odds-research-pr7?sslmode=disable",
		},
		{
			"no query string",
			"postgresql://svc-fonbet-db:s3cr3t@postgresql.databases.svc.cluster.local:5432/odds-research",
			"postgresql://svc-fonbet-db:s3cr3t@postgresql.databases.svc.cluster.local:5432/odds-research-pr7",
		},
		{
			"unrelated database untouched",
			"postgresql://svc-other:pw@postgresql.databases.svc.cluster.local:5432/unrelated-db",
			"postgresql://svc-other:pw@postgresql.databases.svc.cluster.local:5432/unrelated-db",
		},
		{
			"non-url value untouched",
			"parent-normal",
			"parent-normal",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rewriteDatabaseNames(c.value, rewrites)
			if got != c.want {
				t.Errorf("rewriteDatabaseNames(%q) = %q, want %q", c.value, got, c.want)
			}
		})
	}

	if got := rewriteDatabaseNames("unchanged", map[string]string{}); got != "unchanged" {
		t.Errorf("rewriteDatabaseNames with empty rewrites = %q, want unchanged", got)
	}
}
