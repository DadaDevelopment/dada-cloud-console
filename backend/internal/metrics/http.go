package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name: "http_server_request_duration_seconds",
	Help: "Latency of HTTP requests handled by the API, labelled by method, matched route template and response status. Query per-endpoint percentiles with histogram_quantile over rate() of the _bucket series.",
	Buckets: []float64{
		0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
	},
}, []string{"method", "route", "status"})

// HTTPMiddleware records request latency into httpRequestDuration for every
// request. The route label uses Gin's matched route template (c.FullPath), not
// the raw URL, to keep cardinality bounded; unmatched requests (404) collapse to
// a single "unmatched" series instead of exploding one series per random path.
func HTTPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		httpRequestDuration.WithLabelValues(
			c.Request.Method,
			route,
			strconv.Itoa(c.Writer.Status()),
		).Observe(time.Since(start).Seconds())
	}
}
