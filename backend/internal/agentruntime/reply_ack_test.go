package agentruntime

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func ackPending(texts ...string) []Message {
	out := make([]Message, 0, len(texts))
	for _, t := range texts {
		out = append(out, Message{Role: "user", Content: t})
	}
	return out
}

func TestAckConfirmationBatch(t *testing.T) {
	require.True(t, ackConfirmationBatch(ackPending("готово")))
	require.True(t, ackConfirmationBatch(ackPending("Сделал.")))
	require.True(t, ackConfirmationBatch(ackPending("отправил", "готово")))
	require.True(t, ackConfirmationBatch(ackPending("зарегался")))
	require.True(t, ackConfirmationBatch(ackPending("ок, сделаю")))

	require.False(t, ackConfirmationBatch(nil))
	require.False(t, ackConfirmationBatch(ackPending("готово, а что дальше?")))
	require.False(t, ackConfirmationBatch(ackPending("готово", "и ещё вопрос про комиссию")))
	require.False(t, ackConfirmationBatch(ackPending("пополнил")), "money in flight is never just a receipt")
	require.False(t, ackConfirmationBatch(ackPending("отправил 1000 на депозит")))

	withMedia := ackPending("готово")
	withMedia[0].Attachments = []any{map[string]any{"kind": "photo"}}
	require.False(t, ackConfirmationBatch(withMedia))
}

func TestAckLimitReason_OffByDefault(t *testing.T) {
	long := strings.Repeat("я", 200)
	require.Empty(t, ackLimitReason(long, ackPending("готово"), RuntimeState{}, 0))
	require.Empty(t, ackLimitReason(long, ackPending("готово"), RuntimeState{}, -5))
}

func TestAckLimitReason_FiresOnlyOnALongReplyToABareConfirmation(t *testing.T) {
	long := strings.Repeat("я", 200)
	require.NotEmpty(t, ackLimitReason(long, ackPending("готово"), RuntimeState{}, 30))
	require.Empty(t, ackLimitReason("Принял", ackPending("готово"), RuntimeState{}, 30))
	require.Empty(t, ackLimitReason(long, ackPending("а какая комиссия?"), RuntimeState{}, 30))

	open := RuntimeState{OpenLoops: map[string]OpenLoop{"amount": {Question: "сколько?", Status: "open", SourceMessageID: uuid.New()}}}
	require.Empty(t, ackLimitReason(long, ackPending("готово"), open, 30), "an open loop means the turn has real work to do")

	resolved := RuntimeState{OpenLoops: map[string]OpenLoop{"amount": {Question: "сколько?", Status: "resolved", SourceMessageID: uuid.New()}}}
	require.NotEmpty(t, ackLimitReason(long, ackPending("готово"), resolved, 30))
}

func TestAckLimitFlag_DefaultIsZero(t *testing.T) {
	require.Zero(t, runtimeFlagsFromEnv().AckLimit)
	t.Setenv("AGENT_RUNTIME_ACK_LIMIT", "30")
	require.Equal(t, 30, runtimeFlagsFromEnv().AckLimit)
}

func TestAckRepairHint_NamesTheLimitAndAsksForNoQuestion(t *testing.T) {
	hint := ackRepairHint(30)
	require.Contains(t, hint, "30")
	require.Contains(t, hint, "без нового вопроса")
}

// courtesyOnly is the neighbouring rule and must keep its own boundary: the
// ten courtesy phrases stay silences, and none of them is an action receipt.
func TestAckLimit_DoesNotOverlapCourtesyOnly(t *testing.T) {
	history := []Message{{Role: "assistant", Content: "Всё верно, счёт открыт."}}
	require.True(t, courtesyOnly(ackPending("спасибо"), history, RuntimeState{}))
	require.False(t, ackConfirmationBatch(ackPending("спасибо")))
	require.False(t, courtesyOnly(ackPending("готово"), history, RuntimeState{}))
}
