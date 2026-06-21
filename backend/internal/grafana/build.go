package grafana

import "fmt"

// ThresholdRule is the console-level description of a single-metric threshold
// alert, translated into the Grafana provisioning payload by BuildThresholdRule.
type ThresholdRule struct {
	UID          string            // stable, deterministic
	Title        string            // human title
	FolderUID    string            // project folder
	RuleGroup    string            // grouping (we use the monitoring_app id)
	Expr         string            // PromQL returning the metric to compare
	Condition    string            // >, <, >=, <=
	Threshold    float64           //
	For          string            // pending duration, e.g. "5m"
	ContactPoint string            // contact point NAME for notification routing
	Labels       map[string]string // org_id/project_id/monitoring_app + custom
}

// grafanaEvaluator maps a console condition to a Grafana threshold evaluator type.
func grafanaEvaluator(cond string) string {
	switch cond {
	case "<", "<=":
		return "lt"
	default: // >, >=
		return "gt"
	}
}

// BuildThresholdRule produces a Grafana 10 provisioning alert-rule payload: a
// Prometheus query (refId A) reduced to its last value (refId B) and compared to
// the threshold (refId C, the alerting condition). Notification routing is set
// via notification_settings.receiver (the contact point name).
func BuildThresholdRule(promDSUID string, r ThresholdRule) map[string]any {
	queryA := map[string]any{
		"refId":         "A",
		"datasourceUid": promDSUID,
		"relativeTimeRange": map[string]any{
			"from": 600,
			"to":   0,
		},
		"model": map[string]any{
			"refId":         "A",
			"expr":          r.Expr,
			"instant":       true,
			"datasource":    map[string]any{"type": "prometheus", "uid": promDSUID},
			"intervalMs":    1000,
			"maxDataPoints": 43200,
		},
	}
	reduceB := map[string]any{
		"refId":         "B",
		"datasourceUid": "__expr__",
		"model": map[string]any{
			"refId":      "B",
			"type":       "reduce",
			"reducer":    "last",
			"expression": "A",
			"datasource": map[string]any{"type": "__expr__", "uid": "__expr__"},
		},
	}
	conditionC := map[string]any{
		"refId":         "C",
		"datasourceUid": "__expr__",
		"model": map[string]any{
			"refId":      "C",
			"type":       "threshold",
			"expression": "B",
			"datasource": map[string]any{"type": "__expr__", "uid": "__expr__"},
			"conditions": []any{
				map[string]any{
					"type": "query",
					"evaluator": map[string]any{
						"type":   grafanaEvaluator(r.Condition),
						"params": []any{r.Threshold},
					},
				},
			},
		},
	}

	forDur := r.For
	if forDur == "" {
		forDur = "5m"
	}

	rule := map[string]any{
		"uid":          r.UID,
		"title":        r.Title,
		"folderUID":    r.FolderUID,
		"ruleGroup":    r.RuleGroup,
		"condition":    "C",
		"for":          forDur,
		"noDataState":  "NoData",
		"execErrState": "Error",
		"orgID":        1,
		"data":         []any{queryA, reduceB, conditionC},
		"labels":       r.Labels,
		"annotations": map[string]any{
			"summary": fmt.Sprintf("%s %s %g", r.Expr, r.Condition, r.Threshold),
		},
	}
	if r.ContactPoint != "" {
		rule["notification_settings"] = map[string]any{"receiver": r.ContactPoint}
	}
	return rule
}

// MetricPanel is one timeseries panel definition for the per-resource dashboard.
type MetricPanel struct {
	Title string
	Expr  string
	Unit  string
}

// BuildDashboard produces a minimal Grafana dashboard model (one timeseries
// panel per metric) for a monitoring resource. uid must be stable per resource.
func BuildDashboard(uid, title, promDSUID string, panels []MetricPanel) map[string]any {
	gridW := 12
	out := make([]any, 0, len(panels))
	for i, p := range panels {
		out = append(out, map[string]any{
			"id":         i + 1,
			"type":       "timeseries",
			"title":      p.Title,
			"datasource": map[string]any{"type": "prometheus", "uid": promDSUID},
			"gridPos":    map[string]any{"h": 8, "w": gridW, "x": (i % 2) * gridW, "y": (i / 2) * 8},
			"fieldConfig": map[string]any{
				"defaults": map[string]any{"unit": p.Unit},
			},
			"targets": []any{
				map[string]any{
					"refId":      "A",
					"expr":       p.Expr,
					"datasource": map[string]any{"type": "prometheus", "uid": promDSUID},
				},
			},
		})
	}
	return map[string]any{
		"uid":           uid,
		"title":         title,
		"schemaVersion": 39,
		"tags":          []any{"dada-monitoring"},
		"timezone":      "browser",
		"time":          map[string]any{"from": "now-6h", "to": "now"},
		"panels":        out,
	}
}
