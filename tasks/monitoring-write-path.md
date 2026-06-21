# Monitoring write path + resource (PRD-monitoring / ADR-011)

Scope: WRITE path only. Read path exists. Sibling chip = alerts/dashboards/health/UI.
Depends on IAM-consumer chip (scope middleware + claims) — NOT in repo, so add minimal scope support ourselves.

## Decisions (caveman)
- No prometheus/prompb dep (huge tree). Hand-roll remote-write protobuf via `google.golang.org/protobuf/encoding/protowire`. Add only `github.com/golang/snappy`.
- Rate limit via `golang.org/x/time/rate`, per-app in-memory token bucket.
- No user-service IAM client exists → issue key locally (crypto/rand + argon2id, plaintext once). Seam noted for future IAM delegation.
- Ingest auth = scope from claims (RequireScope). Org/project labels from DB (authoritative), not client body. Path projectId must match app row (isolation).
- Logs write to `dada-app-logs-<date>`; read pattern `dada-app-logs-*` catches them. Mirror fields so existing LogsViewer works (app=monitoring_app, vm_name=source).

## Tasks
- [ ] migration 016_monitoring.sql — monitoring_apps
- [ ] auth: Scopes/OrgID on Claims; scope.go (RequireScope/HasScope)
- [ ] prometheus/remotewrite.go — WriteClient
- [ ] logsearch/write.go — WriteClient
- [ ] config: remote-write + app-log-index + rate/cardinality knobs
- [ ] handler.go: wire clients + limiter
- [ ] api/monitoring.go: Create/List + IngestMetrics/IngestLogs + keygen + guards
- [ ] router.go: routes (ingest behind RequireScope)
- [ ] go.mod: snappy + x/time
- [ ] tests: remote-write round-trip; handler scope/isolation/cardinality
- [ ] build + go test green
- [ ] push

## Review
(filled at end)
