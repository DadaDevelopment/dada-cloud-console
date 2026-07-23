package renderer_test

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

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
		got := renderer.PreviewDatabaseName(c.database, c.pr)
		if got != c.want {
			t.Errorf("PreviewDatabaseName(%q, %d) = %q, want %q", c.database, c.pr, got, c.want)
		}
		if len(got) > 63 {
			t.Errorf("PreviewDatabaseName(%q, %d) = %q, exceeds 63 bytes", c.database, c.pr, got)
		}
	}
}

func TestPreviewDatabaseOwnerRole(t *testing.T) {
	if got, want := renderer.PreviewDatabaseOwnerRole("fonbet-db"), "svc-fonbet-db"; got != want {
		t.Errorf("PreviewDatabaseOwnerRole(%q) = %q, want %q", "fonbet-db", got, want)
	}
}

func TestRenderPreviewDatabase(t *testing.T) {
	out, err := renderer.RenderPreviewDatabase(renderer.PreviewDatabaseSpec{
		Name:        "odds-research-pr7",
		Owner:       "svc-fonbet-db",
		ProjectSlug: "artemmendeleev-gmail-com",
		EnvSlug:     "pr-7-fonbet-value",
		OperationID: "op-123",
	})
	if err != nil {
		t.Fatalf("RenderPreviewDatabase: %v", err)
	}
	for _, want := range []string{
		"apiVersion: postgresql.sql.crossplane.io/v1alpha1",
		"kind: Database",
		"name: odds-research-pr7",
		"deletionPolicy: Delete",
		"owner: svc-fonbet-db",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderPreviewDatabase output missing %q; got:\n%s", want, out)
		}
	}
}
