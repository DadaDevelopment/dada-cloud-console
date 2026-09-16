package agentruntime

import (
	"context"
	"errors"
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

// Review L2: a recovered turn keeps the shape the runtime gave it.
func TestOutboundTexts_PrefersThePartsOverTheGluedTurn(t *testing.T) {
	require.Equal(t, []string{"раз", "два"}, outboundTexts(MessageResponse{Text: "раз два", Messages: []string{"раз", "два"}}))
	require.Equal(t, []string{"один ответ"}, outboundTexts(MessageResponse{Text: "один ответ"}))
	require.Equal(t, []string{""}, outboundTexts(MessageResponse{}))
}

// The same through the real recovery path: the first turn fails, the retry
// succeeds with a cut-up reply, and every part is delivered in order.
func TestPGTurnRecovery_DeliversEveryPartOfARecoveredSeries(t *testing.T) {
	t.Setenv("AGENT_RUNTIME_SPLIT_REPLY", "1")
	f := newRecoveryFixture(t, func(calls int, _ AgentRunRequest) (string, error) {
		if calls == 1 {
			return "", errors.New("a2a task did not complete: failed: Error code: 429")
		}
		return "Принял.\n---\nСчёт открыли?", nil
	})

	_, err := f.srv.runtime.ProcessMessage(context.Background(), f.request("1", "готово"))
	require.Error(t, err)

	require.Eventually(t, func() bool { return len(f.delivered()) == 2 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"Принял.", "Счёт открыли?"}, f.delivered())
	require.Equal(t, []string{"Принял. Счёт открыли?"}, f.assistantMessages(t), "the transcript keeps one row per turn")
}
