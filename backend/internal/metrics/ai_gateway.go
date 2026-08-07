package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// AI Gateway upstream health.
//
// On 2026-08-04 the shared OpenRouter account ran out of credit and the gateway
// answered 402 to 66 of 66 calls over three hours. Every or-* alias was down at
// once, half of console chat and every user's background memory fold got
// nothing, and the only trace was a line in a pod log nobody was reading. The
// gateway had no way to say "a provider stopped paying out" that reached an
// alert, so the outage was found by hand, three hours in.
//
// This is that signal. The gateway posts each upstream failure to
// /internal/ai/failure/record and the counter below is what an alert rule can
// see. It is a counter rather than a DB-derived gauge -- unlike operations or
// builds, an upstream refusal leaves no row anywhere, and writing failures into
// agent_token_usage would put non-billable events in the billing ledger.
//
// Counter, not gauge, also because the question is "is this still happening",
// not "how many are outstanding": increase() over a window survives a pod
// restart and sums cleanly across the console's two replicas, which each see
// whichever failures the service load-balancer sent them.
//
// Labels are bounded on purpose. model_group and provider come from config.yaml
// (about twenty and seven values), status is an HTTP code. No project label:
// per-tenant truth belongs in the ledger, and a tenant-labelled series would
// grow without limit -- the same rule the route label in http.go follows.
var aiUpstreamFailures = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "dada_ai_upstream_failures_total",
	Help: "Upstream provider failures observed by the AI gateway, by model group, provider and HTTP status. status=\"402\" means a provider stopped paying out (exhausted balance); status=\"429\" means quota. A sustained stream on one group is that group's callers getting nothing.",
}, []string{"model_group", "provider", "status"})

// aiFallbacks counts how often a caller was served by a group other than the
// one it asked for. A fallback is a rescue, so it is not an alert on its own --
// but it is the difference between "the chain caught it" and "the chain was
// never declared", and without it a provider outage that the fallbacks fully
// absorb stays invisible until its bill arrives.
var aiFallbacks = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "dada_ai_fallbacks_total",
	Help: "Requests served by a fallback group instead of the one the caller named, by requested and served group. Non-zero means an upstream is degraded even though callers are still getting answers.",
}, []string{"requested", "served"})

// RecordAIUpstreamFailure counts one upstream refusal.
//
// An unknown status arrives as 0 and is labelled "unknown" rather than "0": a
// numeric-looking label that no provider ever returns invites an alert
// expression that silently matches nothing.
func RecordAIUpstreamFailure(modelGroup, provider string, status int) {
	code := "unknown"
	if status > 0 {
		code = strconv.Itoa(status)
	}
	aiUpstreamFailures.WithLabelValues(
		labelOrUnknown(modelGroup), labelOrUnknown(provider), code).Inc()
}

// RecordAIFallback counts one request that a fallback group answered.
func RecordAIFallback(requested, served string) {
	aiFallbacks.WithLabelValues(labelOrUnknown(requested), labelOrUnknown(served)).Inc()
}

func labelOrUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}
