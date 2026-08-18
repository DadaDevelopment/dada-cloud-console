package api

import (
	"context"
	"log"
	"math"
	"time"

	"github.com/gin-gonic/gin"
)

// agentTokenBill is the priced summary of an org's agent-token consumption over
// a time window: raw token count, the provider USD cost frozen in the ledger,
// and the ruble revenue billed to the user (cost-plus, owner ask B).
type agentTokenBill struct {
	TotalTokens int64   `json:"tokens"`
	CostUSD     float64 `json:"costUSD"`
	BilledUSD   float64
	RevenueRUB  float64 `json:"amount"`
}

// agentTokenBillForOrg sums the agent_token_usage ledger for one org over
// [from, to) and prices it into rubles at read time using the configured FX
// rate and markup. Read-time pricing keeps the stored ledger a pure
// provider-cost record so re-pricing history needs no migration.
//
// The window is half-open: from inclusive, to exclusive. org_id is matched
// verbatim (the ledger stores the same string the chat/hub handlers write).
func (h *Handler) agentTokenBillForOrg(ctx context.Context, orgID string, from, to time.Time) (agentTokenBill, error) {
	var bill agentTokenBill
	err := h.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(cost_usd), 0)::float8, COALESCE(SUM(total_tokens), 0),
		        COALESCE(SUM(CASE WHEN source = $4 THEN billed_usd ELSE cost_usd * $5 END), 0)::float8
		   FROM agent_token_usage
		  WHERE org_id = $1 AND created_at >= $2 AND created_at < $3`,
		orgID, from, to, agentTokenSourceGateway, h.cfg.PricingMarkup,
	).Scan(&bill.CostUSD, &bill.TotalTokens, &bill.BilledUSD)
	if err != nil {
		return agentTokenBill{}, err
	}
	bill.RevenueRUB = round2(bill.BilledUSD * h.cfg.AgentTokenUSDToRUB)
	return bill, nil
}

// agentTokenBillAll sums the agent_token_usage ledger across every org over
// [from, to) and prices it into rubles. Platform-wide variant of
// agentTokenBillForOrg for the god-admin economics view.
func (h *Handler) agentTokenBillAll(ctx context.Context, from, to time.Time) (agentTokenBill, error) {
	var bill agentTokenBill
	err := h.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(cost_usd), 0)::float8, COALESCE(SUM(total_tokens), 0),
		        COALESCE(SUM(CASE WHEN source = $3 THEN billed_usd ELSE cost_usd * $4 END), 0)::float8
		   FROM agent_token_usage
		  WHERE created_at >= $1 AND created_at < $2`,
		from, to, agentTokenSourceGateway, h.cfg.PricingMarkup,
	).Scan(&bill.CostUSD, &bill.TotalTokens, &bill.BilledUSD)
	if err != nil {
		return agentTokenBill{}, err
	}
	bill.RevenueRUB = round2(bill.BilledUSD * h.cfg.AgentTokenUSDToRUB)
	return bill, nil
}

// round4 trims a USD figure to cent-of-a-cent precision for JSON display.
func round4(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

// adminAgentTokenEconomics is the platform-wide agent-token economics block for
// the god-admin costs view: actual ledger revenue, provider cost, and margin
// over the last `days` days (owner ask B: agent runs on the dashboard). Unlike
// the infra cost tree these are exact ledger actuals, not OpenCost-scaled
// estimates, so they are kept in their own block and never folded into the
// hardware-reconciled totals. Fail-soft: a ledger read error (e.g. before the
// migration lands) yields available=false rather than failing the whole view.
func (h *Handler) adminAgentTokenEconomics(ctx context.Context, days int) gin.H {
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -days)
	bill, err := h.agentTokenBillAll(ctx, from, to)
	if err != nil {
		log.Printf("admin costs: agent-token economics unavailable: %v", err)
		return gin.H{"available": false}
	}
	costRUB := bill.CostUSD * h.cfg.AgentTokenUSDToRUB
	return gin.H{
		"available":   true,
		"window_days": days,
		"tokens":      bill.TotalTokens,
		"cost_usd":    round4(bill.CostUSD),
		"cost_rub":    round2(costRUB),
		"revenue_rub": round2(bill.RevenueRUB),
		"margin_rub":  round2(bill.RevenueRUB - costRUB),
		"usd_rub":     h.cfg.AgentTokenUSDToRUB,
		"markup":      h.cfg.PricingMarkup,
	}
}

// currentBillingMonthUTC returns the half-open [start, nextMonth) UTC bounds of
// the calendar month containing now, matching the invoice preview period.
func currentBillingMonthUTC(now time.Time) (from, to time.Time) {
	now = now.UTC()
	from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to = from.AddDate(0, 1, 0)
	return from, to
}
