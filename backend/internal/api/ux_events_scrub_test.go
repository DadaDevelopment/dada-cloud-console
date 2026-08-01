package api

import "testing"

// TestScrubUXTargetDropsIdentifiers pins the ingest guard. The label comes off
// the rendered page, so anything the page shows can reach the column: the case
// that produced this is a real prod row where one app row's text carried a
// customer's address inside a registry path.
func TestScrubUXTargetDropsIdentifiers(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"div:fonbet-value Ready nexus.dada-tuda.ru/artemmendeleev-gmail-com", "div:fonbet-value Ready"},
		{"a:artem@example.com", "a:"},
		{"button:915d00c2-1fe2-4527-ab94-f2bd07d2e10b", "button:"},
		{"span:build 1234567", "span:build"},
		{"button:Открыть меню", "button:Открыть меню"},
		{"button:Deploy app", "button:Deploy app"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := scrubUXTarget(tc.in); got != tc.want {
			t.Errorf("scrubUXTarget(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestScrubUXTargetLeavesCleanLabelsAlone guards against the scrub eating the
// ordinary case: most clicks are on plainly named controls and must stay
// byte-identical, or every existing path query silently changes shape.
func TestScrubUXTargetLeavesCleanLabelsAlone(t *testing.T) {
	for _, in := range []string{"button:Создать", "a:Базы данных", "select:select:profile", "textarea:textarea"} {
		if got := scrubUXTarget(in); got != in {
			t.Errorf("clean label rewritten: %q -> %q", in, got)
		}
	}
}
