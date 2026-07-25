package pricing

import (
	"math"
	"testing"
)

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestAgentTokenCostUSD(t *testing.T) {
	cases := []struct {
		name       string
		model      string
		prompt     int64
		completion int64
		want       float64
	}{
		{"sonnet full million each", "claude-sonnet-5", 1_000_000, 1_000_000, 18.0},
		{"sonnet alias", "claude", 1_000_000, 0, 3.0},
		{"haiku output only", "claude-haiku-4-5", 0, 1_000_000, 4.0},
		{"haiku alias", "claude-haiku", 1_000_000, 0, 0.80},
		{"substring match haiku", "anthropic/claude-haiku-4-5-20260101", 1_000_000, 0, 0.80},
		{"unknown model falls back to most expensive", "gpt-mystery", 1_000_000, 0, 3.0},
		{"zero tokens zero cost", "claude", 0, 0, 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AgentTokenCostUSD(tc.model, tc.prompt, tc.completion)
			if !approxEqual(got, tc.want) {
				t.Fatalf("AgentTokenCostUSD(%q, %d, %d)=%v want %v", tc.model, tc.prompt, tc.completion, got, tc.want)
			}
		})
	}
}

func TestAgentTokenRevenueRUB(t *testing.T) {
	got := AgentTokenRevenueRUB(1.0, 80.0, 2.7)
	want := 216.0
	if !approxEqual(got, want) {
		t.Fatalf("AgentTokenRevenueRUB(1, 80, 2.7)=%v want %v", got, want)
	}
	if got := AgentTokenRevenueRUB(0, 80.0, 2.7); !approxEqual(got, 0) {
		t.Fatalf("AgentTokenRevenueRUB(0, ...)=%v want 0", got)
	}
}
