package renderer

import (
	"strings"
	"testing"
)

// Compose v2 interpolates env_file values, so any $ in a secret must be doubled
// to $$ or the value is silently truncated (proven on the findata edge endpoint).
// Locks the escaping so a managed DB password / DATABASE_URL survives intact.
func TestRenderEnvFile_EscapesDollar(t *testing.T) {
	out := RenderEnvFile(map[string]string{
		"POSTGRES_PASSWORD": "ab$cd12$xy",
		"DATABASE_URL":      "postgres://u:p$w@db:5432/app",
		"PLAIN":             "no-dollar",
	})
	for _, want := range []string{
		"POSTGRES_PASSWORD=ab$$cd12$$xy",
		"DATABASE_URL=postgres://u:p$$w@db:5432/app",
		"PLAIN=no-dollar",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("env file missing %q\n---\n%s", want, out)
		}
	}
	// A leading-$ secret must not collapse to empty (the dangerous case).
	out2 := RenderEnvFile(map[string]string{"TOKEN": "$ecret"})
	if !strings.Contains(out2, "TOKEN=$$ecret") {
		t.Errorf("leading-$ secret not escaped:\n%s", out2)
	}
}
