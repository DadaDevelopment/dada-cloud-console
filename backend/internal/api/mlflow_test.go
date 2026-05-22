package api

import "testing"

// startsWith is the prefix-isolation primitive every MLflow registry call
// runs through (D13). A regression here would let projects see — and
// deploy — each other's models. Pin the matrix.
func TestStartsWith(t *testing.T) {
	cases := []struct {
		s, prefix string
		want      bool
	}{
		{"s3://bucket/foo/iris/v1", "s3://bucket/foo/", true},
		// Trailing slash is the project's responsibility — without it the
		// helper happily matches "foobar/" against prefix "foo".
		{"s3://bucket/foobar/", "s3://bucket/foo", true},
		{"s3://bucket/foobar/", "s3://bucket/foo/", false},
		{"s3://bucket/other/iris/v1", "s3://bucket/foo/", false},
		// Empty prefix matches everything — backend treats this as "no
		// prefix configured" and short-circuits before calling startsWith,
		// but pin the behavior so the helper itself stays predictable.
		{"anything", "", true},
		{"", "x", false},
		{"", "", true},
		// Source shorter than prefix can never match.
		{"short", "longerprefix", false},
	}
	for _, c := range cases {
		if got := startsWith(c.s, c.prefix); got != c.want {
			t.Errorf("startsWith(%q, %q) = %v, want %v", c.s, c.prefix, got, c.want)
		}
	}
}
