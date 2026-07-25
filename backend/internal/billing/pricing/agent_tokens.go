package pricing

import "strings"

type agentModelPrice struct {
	inputPerMTok  float64
	outputPerMTok float64
}

// agentModelPrices is the in-repo provider list-price table for the handful of
// models the ADR-015 console agent gateway routes to. Values are USD per
// 1,000,000 tokens and are the billing DEFAULT: they price the ledger's
// cost_usd column at write time. Tune them here or promote to a config/DB
// table later; they must never appear on customer-facing surfaces raw.
var agentModelPrices = map[string]agentModelPrice{
	"claude-sonnet-5":  {inputPerMTok: 3.0, outputPerMTok: 15.0},
	"claude-haiku-4-5": {inputPerMTok: 0.80, outputPerMTok: 4.0},
}

// agentFallbackPrice prices any unrecognised model at the most expensive known
// rate so an unknown model can never silently under-bill.
var agentFallbackPrice = agentModelPrice{inputPerMTok: 3.0, outputPerMTok: 15.0}

func agentPriceFor(model string) agentModelPrice {
	m := strings.ToLower(strings.TrimSpace(model))
	if p, ok := agentModelPrices[m]; ok {
		return p
	}
	switch m {
	case "claude":
		return agentModelPrices["claude-sonnet-5"]
	case "claude-haiku":
		return agentModelPrices["claude-haiku-4-5"]
	}
	if strings.Contains(m, "haiku") {
		return agentModelPrices["claude-haiku-4-5"]
	}
	if strings.Contains(m, "sonnet") {
		return agentModelPrices["claude-sonnet-5"]
	}
	return agentFallbackPrice
}

// AgentTokenCostUSD returns the provider cost in USD for one completed agent
// turn, given the resolved model id and its prompt/completion token counts.
// This is the raw provider cost, before any FX conversion or markup.
func AgentTokenCostUSD(model string, promptTokens, completionTokens int64) float64 {
	p := agentPriceFor(model)
	return (float64(promptTokens)/1e6)*p.inputPerMTok + (float64(completionTokens)/1e6)*p.outputPerMTok
}

// AgentTokenRevenueRUB converts a provider USD cost into the customer-facing
// RUB charge: costUSD x usdToRUB x markup. FX rate and markup are billing
// parameters applied at invoice time, deliberately not frozen into the ledger,
// so re-pricing history never requires a data migration.
func AgentTokenRevenueRUB(costUSD, usdToRUB, markup float64) float64 {
	return costUSD * usdToRUB * markup
}
