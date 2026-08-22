package kagent

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxPromptBytes caps a system prompt. The prompt travels as a ConfigMap key,
// and a ConfigMap is capped at 1 MiB for ALL of its keys together -- hitting
// that ceiling fails the apply, not the save, so the refusal would arrive from
// Argo minutes later with no connection to the edit that caused it.
const MaxPromptBytes = 128 * 1024

// MaxNameLength keeps room under the 63-character DNS label limit for the
// suffixes the composition appends: the prompt ConfigMap is "<name>-prompt"
// and the Deployment kagent creates adds a pod-template hash.
const MaxNameLength = 45

// ValidateName reports why name cannot be an agent name, or nil.
//
// The name becomes a Kubernetes object name, a Service hostname and a label
// value, so it is a DNS-1123 label and nothing else. Validating it here rather
// than at apply time is the difference between a field error in the form and a
// claim that sits Unready in the cluster with a message nobody is watching.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("agent name is required")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("agent name must be at most %d characters", MaxNameLength)
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i != 0 && i != len(name)-1:
		default:
			return fmt.Errorf("agent name may contain only lowercase letters, digits and dashes, and must start and end with a letter or digit")
		}
	}
	return nil
}

// ValidatePrompt reports why prompt cannot be a system prompt, or nil.
//
// Emptiness is the check that matters: an agent with no prompt still starts,
// still answers, and answers as whatever the base model happens to be. That
// looks like a working agent and is the hardest kind of broken to notice.
func ValidatePrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("system prompt is required: an agent with an empty prompt starts and answers as the bare model")
	}
	if len(prompt) > MaxPromptBytes {
		return fmt.Errorf("system prompt must be at most %d bytes, got %d", MaxPromptBytes, len(prompt))
	}
	if !utf8.ValidString(prompt) {
		return fmt.Errorf("system prompt must be valid UTF-8")
	}
	return nil
}

// ValidateHeader reports why header cannot be an allowedHeaders entry, or nil.
//
// Header names are matched case-insensitively by the runtime but are written
// lowercase everywhere in the live agents, and a header that does not parse is
// silently not replayed -- the agent then serves every caller as nobody, which
// is a data-leak shape rather than an outage shape.
func ValidateHeader(header string) error {
	if header == "" {
		return fmt.Errorf("header name is required")
	}
	if header != strings.ToLower(header) {
		return fmt.Errorf("header %q must be lowercase", header)
	}
	for _, r := range header {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("header %q may contain only letters, digits, dashes and underscores", header)
		}
	}
	return nil
}
