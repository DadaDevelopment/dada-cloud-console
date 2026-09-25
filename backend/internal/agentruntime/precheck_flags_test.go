package agentruntime

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The check's switches default to today's behaviour, an unknown mode keeps
// the safe default, a check without a model refuses to start, the check
// model falls back from AGENT_PRECHECK_LLM_* to the scoring judge's, and a
// hands-off code the runtime does not know refuses to start.
func TestRuntimeFlags_PrecheckDefaultsAndStartupCheck(t *testing.T) {
	f := runtimeFlagsFromEnv()
	require.Equal(t, precheckOff, f.Precheck)
	require.Nil(t, f.HandsOffCodes)
	require.NoError(t, ValidateFlagsFromEnv())

	t.Setenv("AGENT_RUNTIME_PRECHECK", "sometimes")
	require.Equal(t, precheckOff, runtimeFlagsFromEnv().Precheck, "an unknown mode keeps the safe default")

	t.Setenv("AGENT_RUNTIME_PRECHECK", "BLOCK")
	require.ErrorContains(t, ValidateFlagsFromEnv(), "AGENT_PRECHECK_LLM_", "a check without its model does not start")
	t.Setenv("AGENT_JUDGE_LLM_URL", "http://judge")
	t.Setenv("AGENT_JUDGE_LLM_KEY", "judge-key")
	t.Setenv("AGENT_JUDGE_LLM_MODEL", "judge-model")
	require.NoError(t, ValidateFlagsFromEnv())
	require.Equal(t, "judge-model", precheckLLMFromEnv().Model, "falls back to the scoring judge's model")

	t.Setenv("AGENT_PRECHECK_LLM_URL", "http://check")
	t.Setenv("AGENT_PRECHECK_LLM_KEY", "check-key")
	require.Equal(t, "judge-key", precheckLLMFromEnv().APIKey, "an incomplete own triple is not used")
	t.Setenv("AGENT_PRECHECK_LLM_MODEL", "check-model")
	own := precheckLLMFromEnv()
	require.Equal(t, "http://check", own.BaseURL)
	require.Equal(t, "check-key", own.APIKey)
	require.Equal(t, "check-model", own.Model)

	t.Setenv("AGENT_RUNTIME_HANDS_OFF_CODES", " e_legal_tax , ")
	require.Equal(t, map[string]bool{"E_LEGAL_TAX": true}, runtimeFlagsFromEnv().HandsOffCodes)
	require.NoError(t, ValidateFlagsFromEnv())
	t.Setenv("AGENT_RUNTIME_HANDS_OFF_CODES", "E_LEGAL_TAX,E_NOPE")
	require.ErrorContains(t, ValidateFlagsFromEnv(), "E_NOPE")
}
