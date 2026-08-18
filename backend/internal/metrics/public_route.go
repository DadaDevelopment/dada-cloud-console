package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var publicRouteProbes = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "dada_public_route_probes_total",
	Help: "Customer-visible public-route probes by outcome. edge_unavailable identifies DNS, TLS or load-balancer transport failures; bad_gateway is an HTTP 5xx returned by the route.",
}, []string{"outcome"})

// RecordPublicRouteProbe creates the operator signal for the exact path a
// visitor takes. The bounded outcome label deliberately contains no hostname
// or tenant identity.
func RecordPublicRouteProbe(healthy bool, reason string) {
	outcome := "ok"
	if !healthy {
		outcome = reason
		if outcome == "" {
			outcome = "failed"
		}
	}
	publicRouteProbes.WithLabelValues(outcome).Inc()
}
