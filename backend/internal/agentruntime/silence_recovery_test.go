package agentruntime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Plan 4.3, the retry knobs. Unset must reproduce the numbers that used to be
// compiled in, because the whole ladder (3s, 6s, 12s) is pinned by
// a2a_failed_task_test.
func TestA2ARetryKnobs_DefaultToTheCompiledInNumbers(t *testing.T) {
	c, ok := NewA2AClient().(*httpA2AClient)
	require.True(t, ok)
	require.Equal(t, failedTaskRetryPause, c.retryPause)
	require.Equal(t, failedTaskRetries, c.retries)

	t.Setenv("AGENT_RUNTIME_TASK_RETRY_PAUSE_MS", "9000")
	t.Setenv("AGENT_RUNTIME_TASK_RETRIES", "5")
	c, ok = NewA2AClient().(*httpA2AClient)
	require.True(t, ok)
	require.Equal(t, 9*time.Second, c.retryPause)
	require.Equal(t, 5, c.retries)

	t.Setenv("AGENT_RUNTIME_TASK_RETRY_PAUSE_MS", "не число")
	t.Setenv("AGENT_RUNTIME_TASK_RETRIES", "-1")
	c, ok = NewA2AClient().(*httpA2AClient)
	require.True(t, ok)
	require.Equal(t, failedTaskRetryPause, c.retryPause)
	require.Equal(t, failedTaskRetries, c.retries)
}

func TestRateLimitedTaskIsRecognised(t *testing.T) {
	for _, text := range []string{"429 Too Many Requests", "provider rate limit hit", "Overloaded", "status 429"} {
		require.Truef(t, reRateLimited.MatchString(text), "%q must read as a rate limit", text)
	}
	for _, text := range []string{"connection refused", "tool server unavailable", "4290 tokens"} {
		require.Falsef(t, reRateLimited.MatchString(text), "%q must not read as a rate limit", text)
	}
}

func TestSilenceRecoveryFlag_DefaultsOff(t *testing.T) {
	require.False(t, runtimeFlagsFromEnv().SilenceRecovery)
	t.Setenv("AGENT_RUNTIME_SILENCE_RECOVERY", "1")
	require.True(t, runtimeFlagsFromEnv().SilenceRecovery)
}

// isSilenceReply is what the new branch keys off, so pin what counts as
// "the turn produced nothing".
func TestIsSilenceReply(t *testing.T) {
	require.True(t, isSilenceReply(""))
	require.True(t, isSilenceReply("   "))
	require.True(t, isSilenceReply("SKIP"))
	require.True(t, isSilenceReply("skip."))
	require.False(t, isSilenceReply("Принял"))
}
