// Package metrics holds build-agent Prometheus collectors (plan §4).
// Labels are restricted to project/app — NEVER secrets or commit content.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// BuildTotal counts finished builds by terminal result.
	BuildTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "build_total",
		Help: "Total builds by terminal result.",
	}, []string{"result"})

	// BuildDuration observes per-phase build duration.
	BuildDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "build_duration_seconds",
		Help: "Build duration per phase.",
	}, []string{"phase"})

	// BuildQueueDepth is the number of queued builds.
	BuildQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "build_queue_depth",
		Help: "Number of queued builds.",
	})

	// BuildsInflight is the number of builds currently running.
	BuildsInflight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "builds_inflight",
		Help: "Number of in-flight builds.",
	})

	// BuildCacheHitTotal counts registry-cache hits.
	BuildCacheHitTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "build_cache_hit_total",
		Help: "Total registry-cache hits.",
	})

	// BuildSupersededTotal counts builds canceled by a newer commit.
	BuildSupersededTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "build_superseded_total",
		Help: "Total builds superseded by a newer commit.",
	})

	// BuildRetryTotal counts infra-failure retries.
	BuildRetryTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "build_retry_total",
		Help: "Total infra-failure build retries.",
	})
)

// Handler returns the /metrics HTTP handler.
func Handler() http.Handler { return promhttp.Handler() }
