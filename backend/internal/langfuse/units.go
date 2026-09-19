package langfuse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

const metricsPath = "/api/public/v2/metrics"

// Views of the v2 metrics API that count toward the plan's monthly units. A
// Langfuse unit is one trace, one observation or one score; a trace in v4 is
// its root observation, so the observations view counted twice (all rows,
// then root rows only) plus the three score views is the whole bill.
const (
	ViewObservations      = "observations"
	ViewScoresNumeric     = "scores-numeric"
	ViewScoresCategorical = "scores-categorical"
	ViewScoresBoolean     = "scores-boolean"
)

// MetricFilter is one where-clause of a v2 metrics query.
type MetricFilter struct {
	Column   string `json:"column"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
	Type     string `json:"type"`
}

// RootObservationsOnly narrows the observations view to trace roots.
var RootObservationsOnly = MetricFilter{Column: "isRootObservation", Operator: "=", Value: true, Type: "boolean"}

type metricsQuery struct {
	View          string         `json:"view"`
	Dimensions    []any          `json:"dimensions"`
	Metrics       []metricsPair  `json:"metrics"`
	Filters       []MetricFilter `json:"filters"`
	FromTimestamp string         `json:"fromTimestamp"`
	ToTimestamp   string         `json:"toTimestamp"`
}

type metricsPair struct {
	Measure     string `json:"measure"`
	Aggregation string `json:"aggregation"`
}

// Count returns how many rows of the view fall inside [from, to).
func (c *Client) Count(ctx context.Context, view string, from, to time.Time, filters ...MetricFilter) (int64, error) {
	if filters == nil {
		filters = []MetricFilter{}
	}
	q := metricsQuery{View: view, Dimensions: []any{}, Metrics: []metricsPair{{Measure: "count", Aggregation: "count"}},
		Filters: filters, FromTimestamp: from.UTC().Format(time.RFC3339), ToTimestamp: to.UTC().Format(time.RFC3339)}
	raw, err := json.Marshal(q)
	if err != nil {
		return 0, fmt.Errorf("langfuse metrics: encode: %w", err)
	}
	var out struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := c.getJSON(ctx, metricsPath, url.Values{"query": {string(raw)}}, &out); err != nil {
		return 0, err
	}
	if len(out.Data) == 0 {
		return 0, nil
	}
	var v any
	if err := json.Unmarshal(out.Data[0]["count_count"], &v); err != nil {
		return 0, fmt.Errorf("langfuse metrics %s: decode count: %w", view, err)
	}
	switch n := v.(type) {
	case float64:
		return int64(n), nil
	case string:
		return strconv.ParseInt(n, 10, 64)
	}
	return 0, fmt.Errorf("langfuse metrics %s: count_count is %T", view, v)
}
