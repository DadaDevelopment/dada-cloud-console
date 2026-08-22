package kagent

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	valid := []string{"reels-poc", "a", "agent-2", "digest-poc"}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
	invalid := map[string]string{
		"":                      "empty",
		"Reels":                 "uppercase becomes an invalid label value",
		"reels_poc":             "underscore is not a DNS label character",
		"-reels":                "leading dash",
		"reels-":                "trailing dash",
		strings.Repeat("a", 46): "too long once -prompt and a pod hash are appended",
		"reels poc":             "space",
		"reels.poc":             "a dot would split the Service hostname",
	}
	for name, why := range invalid {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error: %s", name, why)
		}
	}
}

// TestValidatePrompt_RefusesAnEmptyPrompt: an agent with no prompt still starts
// and still answers -- as the bare model. That is the hardest kind of broken to
// notice, so it is refused at the form and never reaches git.
func TestValidatePrompt_RefusesAnEmptyPrompt(t *testing.T) {
	for _, prompt := range []string{"", "   ", "\n\t "} {
		if err := ValidatePrompt(prompt); err == nil {
			t.Errorf("ValidatePrompt(%q) = nil, want an error", prompt)
		}
	}
	if err := ValidatePrompt("You are a helpful agent."); err != nil {
		t.Errorf("ValidatePrompt(real prompt) = %v, want nil", err)
	}
}

// TestValidatePrompt_RefusesAPromptTooBigForItsConfigMap: over the cap the
// refusal arrives from Argo minutes later, attached to a sync failure rather
// than to the edit that caused it.
func TestValidatePrompt_RefusesAPromptTooBigForItsConfigMap(t *testing.T) {
	if err := ValidatePrompt(strings.Repeat("x", MaxPromptBytes+1)); err == nil {
		t.Fatal("an oversized prompt must be refused before it is committed")
	}
	if err := ValidatePrompt(strings.Repeat("x", MaxPromptBytes)); err != nil {
		t.Fatalf("a prompt exactly at the cap must pass: %v", err)
	}
}

func TestValidateHeader(t *testing.T) {
	if err := ValidateHeader("x-dada-user"); err != nil {
		t.Errorf("ValidateHeader(x-dada-user) = %v, want nil", err)
	}
	for _, bad := range []string{"", "X-Dada-User", "x dada", "x:dada"} {
		if err := ValidateHeader(bad); err == nil {
			t.Errorf("ValidateHeader(%q) = nil, want an error", bad)
		}
	}
}
