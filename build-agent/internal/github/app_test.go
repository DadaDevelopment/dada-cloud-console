package github

import "testing"

func TestFirstLine(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"single line", "fix: broken thing", "fix: broken thing"},
		{"multi line body", "fix: broken thing\n\nLonger explanation here.", "fix: broken thing"},
		{"trims whitespace", "  fix: broken thing  \nbody", "fix: broken thing"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstLine(tc.input); got != tc.want {
				t.Errorf("firstLine(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestTruncateCommitMessage(t *testing.T) {
	long := ""
	for i := 0; i < 250; i++ {
		long += "a"
	}
	got := truncate(firstLine(long), 200)
	if len(got) != 200 {
		t.Fatalf("truncate len = %d, want 200", len(got))
	}

	short := "short subject"
	if got := truncate(firstLine(short), 200); got != short {
		t.Errorf("truncate(%q) = %q, want unchanged", short, got)
	}
}
