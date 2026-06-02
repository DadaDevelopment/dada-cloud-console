package server

import "testing"

func TestResolveEditFile(t *testing.T) {
	cases := []struct {
		file     string
		wantPath string
		wantYAML bool
		wantOK   bool
	}{
		{"", "clusters/beget-prod/projects/a/environments/p/apps/x/values.yaml", true, true},
		{"values.yaml", "clusters/beget-prod/projects/a/environments/p/apps/x/values.yaml", true, true},
		{"compose.yaml", "clusters/beget-prod/projects/a/environments/p/apps/x/compose.yaml", true, true},
		{".env", "clusters/beget-prod/projects/a/environments/p/apps/x/.env", false, true},
		{"secrets.yaml", "", false, false},
	}
	for _, c := range cases {
		ef, ok := resolveEditFile(c.file, "a", "p", "x")
		if ok != c.wantOK {
			t.Errorf("file=%q ok=%v want %v", c.file, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if ef.path != c.wantPath {
			t.Errorf("file=%q path=%q want %q", c.file, ef.path, c.wantPath)
		}
		if ef.isYAML != c.wantYAML {
			t.Errorf("file=%q isYAML=%v want %v", c.file, ef.isYAML, c.wantYAML)
		}
	}
}
