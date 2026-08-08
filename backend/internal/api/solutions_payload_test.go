package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/solutions"
)

// TestSolutionPayloadCarriesIcon pins the catalog icon. The console draws a
// catalog entry as a logo chip, and it derives nothing itself: if this field
// goes missing the chips silently fall back to a generic glyph and every
// ready-made project starts looking the same.
func TestSolutionPayloadCarriesIcon(t *testing.T) {
	if len(solutions.V1) == 0 {
		t.Fatal("catalog is empty")
	}
	for _, s := range solutions.V1 {
		got, _ := solutionPayload(s)["icon"].(string)
		if want := s.Icon(); got != want {
			t.Fatalf("%s: icon = %q, want %q", s.Slug, got, want)
		}
		if got == "" {
			t.Fatalf("%s: no icon", s.Slug)
		}
	}
}
