package agentruntime

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFunnelOrderReason(t *testing.T) {
	empty := RuntimeState{}
	withTarget := RuntimeState{ReportedFacts: map[string]ReportedFact{targetFactKey: {Value: "50 тысяч в месяц"}}}
	withAmount := RuntimeState{ReportedFacts: map[string]ReportedFact{amountFactKey: {Value: "500"}}}

	require.NotEmpty(t, funnelOrderReason("Рад, что всё устраивает. С какого депозита готовы начать?", empty))
	require.NotEmpty(t, funnelOrderReason("Понял. На сколько вам комфортно пополнить?", empty))
	require.NotEmpty(t, funnelOrderReason("Вход от 300 USD. Сколько свободных сейчас есть на старт?", empty))
	require.NotEmpty(t, funnelOrderReason("Ок. Готовы зайти на 300?", empty))

	require.Empty(t, funnelOrderReason("Подскажи, а какая цель по результатам с трейдинга в месяц? Ну или в год?", empty))
	require.Empty(t, funnelOrderReason("Депозит остаётся вашими деньгами на вашем счёте. Подходит вам такой формат?", empty))
	require.Empty(t, funnelOrderReason("Депозит остаётся вашими деньгами на вашем счёте у брокера.", empty))
	require.Empty(t, funnelOrderReason("Тогда вопрос, на какой депозит вы готовы начать торговать, исходя из целей по доходу?", withTarget))
	require.Empty(t, funnelOrderReason("С 500 стартуем?", withAmount))
}
