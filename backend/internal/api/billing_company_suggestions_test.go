package api

import "testing"

func TestOnlyDigits(t *testing.T) {
	t.Parallel()
	if got, want := onlyDigits(" 7807-402 712 "), "7807402712"; got != want {
		t.Fatalf("onlyDigits() = %q, want %q", got, want)
	}
}
