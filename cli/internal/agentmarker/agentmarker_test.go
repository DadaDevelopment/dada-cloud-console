package agentmarker

import "testing"

func TestDetectPrefersClaudecodeOverTermProgram(t *testing.T) {
	env := map[string]string{
		"TERM_PROGRAM": "vscode",
		"CLAUDECODE":   "1",
	}
	got := Detect(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if got != "CLAUDECODE" {
		t.Errorf("got %q, want CLAUDECODE", got)
	}
}

func TestDetectFallsBackToTermProgram(t *testing.T) {
	env := map[string]string{"TERM_PROGRAM": "WarpTerminal"}
	got := Detect(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if got != "TERM_PROGRAM" {
		t.Errorf("got %q, want TERM_PROGRAM", got)
	}
}

func TestDetectReturnsEmptyWhenNothingSet(t *testing.T) {
	got := Detect(func(k string) (string, bool) { return "", false })
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDetectIgnoresEmptyValue(t *testing.T) {
	env := map[string]string{"CLAUDECODE": "", "CURSOR_AGENT": "1"}
	got := Detect(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if got != "CURSOR_AGENT" {
		t.Errorf("got %q, want CURSOR_AGENT (CLAUDECODE set but empty should not count)", got)
	}
}
