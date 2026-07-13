package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func sampleCount(t *testing.T, method, route, status string) uint64 {
	t.Helper()
	h, ok := httpRequestDuration.WithLabelValues(method, route, status).(prometheus.Histogram)
	if !ok {
		t.Fatal("expected observer to be a prometheus.Histogram")
	}
	var m dto.Metric
	if err := h.(prometheus.Metric).Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

func TestHTTPMiddlewareRecordsMatchedRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(HTTPMiddleware())
	r.GET("/projects/:id", func(c *gin.Context) { c.Status(http.StatusOK) })

	before := sampleCount(t, "GET", "/projects/:id", "200")
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/projects/42", nil))

	if got := sampleCount(t, "GET", "/projects/:id", "200") - before; got != 1 {
		t.Fatalf("expected 1 observation for matched route template, got %v", got)
	}
}

func TestHTTPMiddlewareCollapsesUnmatched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(HTTPMiddleware())

	before := sampleCount(t, "GET", "unmatched", "404")
	for _, p := range []string{"/nope/1", "/nope/2", "/other/random"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}

	if got := sampleCount(t, "GET", "unmatched", "404") - before; got != 3 {
		t.Fatalf("expected 3 unmatched requests collapsed into one series, got %v", got)
	}
}
