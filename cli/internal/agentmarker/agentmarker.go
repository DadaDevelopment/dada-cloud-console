// Package agentmarker detects whether ddc is running inside a known coding
// agent's terminal session, so the CLI can tell the console's audit trail
// "this deploy was not typed by a human at a keyboard right now".
package agentmarker

import "os"

// candidateVars are checked in order; the first one present and non-empty
// wins. The set favors vendor-specific, hard-to-spoof-by-accident variables
// (CLAUDECODE, CURSOR_*, CODEX_*, ...) over TERM_PROGRAM, which is also set
// for a human typing in a plain VS Code terminal and is included last, as a
// weak fallback signal only, per the task's explicit request to consider it.
var candidateVars = []string{
	"CLAUDECODE",
	"CURSOR_TRACE_ID",
	"CURSOR_AGENT",
	"CODEX_SANDBOX",
	"GITHUB_COPILOT_CLI",
	"WINDSURF_SESSION_ID",
	"REPLIT_DEV_DOMAIN",
	"AIDER_MODEL",
	"TERM_PROGRAM",
}

// Detect returns the name of the first agent-session environment variable
// found set and non-empty, or "" if none of candidateVars are present. The
// header carries the variable NAME, not its value, so no session content
// leaks into request headers.
func Detect(lookup func(string) (string, bool)) string {
	for _, name := range candidateVars {
		if v, ok := lookup(name); ok && v != "" {
			return name
		}
	}
	return ""
}

// DetectFromEnv is Detect wired to the real process environment.
func DetectFromEnv() string {
	return Detect(os.LookupEnv)
}
