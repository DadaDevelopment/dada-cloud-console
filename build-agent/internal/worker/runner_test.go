package worker

import "testing"

func TestInjectToken(t *testing.T) {
	got := injectToken("https://github.com/acme/app.git", "x-access-token", "tok123")
	want := "https://x-access-token:tok123@github.com/acme/app.git"
	if got != want {
		t.Errorf("injectToken = %q, want %q", got, want)
	}
	// non-https passthrough
	if got := injectToken("git@github.com:acme/app.git", "u", "t"); got != "git@github.com:acme/app.git" {
		t.Errorf("ssh url should be untouched, got %q", got)
	}
}

func TestShortSHA(t *testing.T) {
	if got := shortSHA("deadbeefcafebabe"); got != "deadbeef" {
		t.Errorf("shortSHA = %q", got)
	}
	if got := shortSHA("abc"); got != "abc" {
		t.Errorf("shortSHA short = %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("shellQuote = %q", got)
	}
}
