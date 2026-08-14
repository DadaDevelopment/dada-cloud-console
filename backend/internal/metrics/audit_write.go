package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Live incident 2026-08-14: a user with 359 pageviews, 689 clicks and a
// deployed, built app (nodejs-argo) held twelve audit_events rows, all
// SessionStart, zero CreateProject/CreateApp/TriggerBuild. writeAuditRow
// (backend/internal/api/audit.go) returned bare on any Postgres error that
// was not one of the three known-resolvable foreign key violations, so the
// insert failed with nothing anywhere to say so: not a log line, not a
// metric, not a row. Every funnel measurement built on audit_events was
// quietly undercounting write-actions with no way to know by how much.
//
// auditWriteFailures is the fix for the "nothing anywhere" half of that: a
// bounded counter an alert rule can watch, by action and failure reason, so
// the day the next constraint change breaks an insert path, the counter
// moves before someone has to notice a funnel number lost seven days of
// review to figure out why it did not match reality.
//
// Labels stay bounded: action is one of the closed auditAction* constants in
// backend/internal/api/audit.go, reason is one of a short closed set
// (fk_unresolved, other). No actor or resource label -- that is exactly the
// per-tenant detail that belongs in the audit row itself, not in a Prometheus
// series that must stay small forever.
var auditWriteFailures = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "dada_audit_write_failures_total",
	Help: "audit_events INSERT attempts that failed and were dropped, by action and reason. Any sustained rate here means the audit trail is losing rows that funnel/activation metrics rely on.",
}, []string{"action", "reason"})

// RecordAuditWriteFailure counts one dropped audit_events row.
func RecordAuditWriteFailure(action, reason string) {
	if action == "" {
		action = "unknown"
	}
	if reason == "" {
		reason = "other"
	}
	auditWriteFailures.WithLabelValues(action, reason).Inc()
}

// AuditWriteFailuresCollectorForTest hands back the single counter behind one
// (action, reason) label pair, so a test in another package can assert on the
// exact series RecordAuditWriteFailure writes to (via testutil.ToFloat64)
// without this package exposing the CounterVec itself for production use.
func AuditWriteFailuresCollectorForTest(action, reason string) prometheus.Counter {
	return auditWriteFailures.WithLabelValues(action, reason)
}
