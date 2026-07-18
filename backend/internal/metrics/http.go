package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
)

var httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name: "http_server_request_duration_seconds",
	Help: "Latency of HTTP requests handled by the API, labelled by method, matched route template and response status. Query per-endpoint percentiles with histogram_quantile over rate() of the _bucket series.",
	Buckets: []float64{
		0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
	},
}, []string{"method", "route", "status"})

// slowRequests counts responses that breached the platform latency budget, by
// method and matched route. It is the durable enforcement signal behind the
// "<300ms" rule: alert on rate() > 0 to catch a regressed endpoint the moment it
// ships, without waiting for a user to report it. Cardinality is bounded because
// the route label is the Gin template, not the raw path.
var slowRequests = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "dada_http_slow_requests_total",
	Help: "HTTP requests whose handler exceeded the platform latency budget (slowRequestBudget). Labelled by method and matched route. rate() > 0 means an endpoint is over budget.",
}, []string{"method", "route"})

// slowRequestBudget is the platform-wide per-request latency ceiling. Every
// backend endpoint is expected to answer inside it; a breach is logged at WARN
// and counted in dada_http_slow_requests_total so it surfaces on a dashboard
// instead of only in a user complaint. Simple read endpoints target far lower
// (~50ms), but a single hard ceiling keeps the mechanism unambiguous.
const slowRequestBudget = 300 * time.Millisecond

// HTTPMiddleware records request latency into httpRequestDuration for every
// request. The route label uses Gin's matched route template (c.FullPath), not
// the raw URL, to keep cardinality bounded; unmatched requests (404) collapse to
// a single "unmatched" series instead of exploding one series per random path.
//
// It also enforces the latency budget: any request slower than slowRequestBudget
// is logged at WARN and counted in dada_http_slow_requests_total so a regressed
// endpoint pages the platform team rather than a user.
func HTTPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(start)
		status := c.Writer.Status()
		httpRequestDuration.WithLabelValues(
			c.Request.Method,
			route,
			strconv.Itoa(status),
		).Observe(elapsed.Seconds())

		if elapsed > slowRequestBudget {
			slowRequests.WithLabelValues(c.Request.Method, route).Inc()
			log.Warn().
				Str("method", c.Request.Method).
				Str("route", route).
				Int("status", status).
				Dur("duration", elapsed).
				Float64("budget_ms", float64(slowRequestBudget.Milliseconds())).
				Msg("slow request: exceeded latency budget")
		}
	}
}
