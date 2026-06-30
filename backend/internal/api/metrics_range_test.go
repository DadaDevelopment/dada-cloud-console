package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func rangeCtx(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?"+query, nil)
	return c
}

func TestParseRangePresetsUnchanged(t *testing.T) {
	cases := []struct {
		q    string
		dur  time.Duration
		step time.Duration
	}{
		{"range=15m", 15 * time.Minute, 60 * time.Second},
		{"range=", time.Hour, 60 * time.Second},
		{"range=1h", time.Hour, 60 * time.Second},
		{"range=6h", 6 * time.Hour, 5 * time.Minute},
		{"range=24h", 24 * time.Hour, 15 * time.Minute},
	}
	for _, tc := range cases {
		start, end, step := parseRange(rangeCtx(tc.q))
		if got := end.Sub(start).Round(time.Second); got != tc.dur {
			t.Errorf("%s: dur=%v want %v", tc.q, got, tc.dur)
		}
		if step != tc.step {
			t.Errorf("%s: step=%v want %v", tc.q, step, tc.step)
		}
	}
}

func TestParseRangeFlexible(t *testing.T) {
	cases := []struct {
		q    string
		dur  time.Duration
		step time.Duration
	}{
		{"range=30m", 30 * time.Minute, 60 * time.Second},
		{"range=2h", 2 * time.Hour, 60 * time.Second},
		{"range=7d", 7 * 24 * time.Hour, time.Hour},
		{"range=4w", 28 * 24 * time.Hour, time.Hour},
		{"range=60d", 60 * 24 * time.Hour, 6 * time.Hour},
		{"range=bogus", time.Hour, 60 * time.Second}, // falls back to default
	}
	for _, tc := range cases {
		start, end, step := parseRange(rangeCtx(tc.q))
		if got := end.Sub(start).Round(time.Second); got != tc.dur {
			t.Errorf("%s: dur=%v want %v", tc.q, got, tc.dur)
		}
		if step != tc.step {
			t.Errorf("%s: step=%v want %v", tc.q, step, tc.step)
		}
	}
}

func TestParseRangeAbsoluteFromTo(t *testing.T) {
	from := time.Now().Add(-3 * time.Hour).Unix()
	to := time.Now().Unix()
	start, end, step := parseRange(rangeCtx("from=" + itoa(from) + "&to=" + itoa(to)))
	if start.Unix() != from || end.Unix() != to {
		t.Fatalf("absolute window not honored: start=%d end=%d", start.Unix(), end.Unix())
	}
	if step != 60*time.Second {
		t.Errorf("3h window step=%v want 60s", step)
	}

	// to <= from is invalid → falls back to default 1h relative window.
	s2, e2, _ := parseRange(rangeCtx("from=" + itoa(to) + "&to=" + itoa(from)))
	if got := e2.Sub(s2).Round(time.Second); got != time.Hour {
		t.Errorf("invalid from/to: dur=%v want 1h fallback", got)
	}
}

func itoa(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func TestValidAgg(t *testing.T) {
	for _, ok := range []string{"", "avg", "sum", "min", "max", "count", "p50", "p90", "p95", "p99"} {
		if !validAgg(ok) {
			t.Errorf("validAgg(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"median", "p95)", "sum(", "stddev", "topk"} {
		if validAgg(bad) {
			t.Errorf("validAgg(%q) = true, want false", bad)
		}
	}
}

func TestAggExpr(t *testing.T) {
	const inner = `rate(reqs_total{}[60s])`
	cases := []struct {
		agg, groupBy string
		counter      bool
		want         string
	}{
		{"", "", true, "sum(" + inner + ")"},
		{"", "", false, "avg(" + inner + ")"},
		{"", "code", true, "sum by (code) (" + inner + ")"},
		{"", "code", false, "avg by (code) (" + inner + ")"},
		{"max", "", false, "max(" + inner + ")"},
		{"min", "code", false, "min by (code) (" + inner + ")"},
		{"p95", "", false, "quantile(0.95, " + inner + ")"},
		{"p99", "code", false, "quantile by (code) (0.99, " + inner + ")"},
		{"p50", "", false, "quantile(0.5, " + inner + ")"},
	}
	for _, tc := range cases {
		got := aggExpr(tc.agg, tc.groupBy, inner, tc.counter)
		if got != tc.want {
			t.Errorf("aggExpr(%q,%q,counter=%v) = %q, want %q", tc.agg, tc.groupBy, tc.counter, got, tc.want)
		}
	}
}
