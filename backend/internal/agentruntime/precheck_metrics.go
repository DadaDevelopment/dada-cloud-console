package agentruntime

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// precheckOutcomes counts checked turns, follow-ups and hand-off lines by
// agent, mode (log, block, idle_block, client_line) and outcome. Under log
// the outcome is shadow_outcome, what block would have done, so the same
// series tells how often block would hand off before block is switched on;
// handoff_by names the criterion or signal of a hand-off, a closed set from
// the agent's spec. The share of dialogues with a hand-off (the 15 % gate) is
// a per-conversation number and is read from the "agentruntime: precheck"
// log line, which carries the conversation id.
var precheckOutcomes = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "dada_agent_precheck_total",
	Help: "Pre-delivery checks of agent replies by agent, mode and outcome; under mode=log the outcome is what block would have done.",
}, []string{"agent", "mode", "outcome", "handoff_by"})

// precheckSeconds is the judge time of one checked turn, all its Checks
// together and the agent's rewrite excluded: the 30 s p95 gate of the
// rollout reads it.
var precheckSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "dada_agent_precheck_check_seconds",
	Help:    "Judge time of one checked turn: all its Checks together, the agent's rewrite excluded.",
	Buckets: []float64{0.5, 1, 2, 3, 5, 8, 12, 20, 30, 45},
}, []string{"agent", "mode"})

// observePrecheck counts one precheck record (see precheckTurn.record).
func observePrecheck(agent string, rec map[string]any) {
	mode, _ := rec["mode"].(string)
	outcome, _ := rec["outcome"].(string)
	by, _ := rec["handoff_by"].(string)
	if shadow, _ := rec["shadow_outcome"].(string); shadow != "" {
		outcome = shadow
		by, _ = rec["shadow_handoff_by"].(string)
	}
	precheckOutcomes.WithLabelValues(agent, mode, outcome, by).Inc()
	if ms, ok := rec["check_ms"].(int64); ok {
		precheckSeconds.WithLabelValues(agent, mode).Observe(float64(ms) / 1000)
	}
}
