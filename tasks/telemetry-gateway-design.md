# Telemetry Gateway — design + impl plan

Decision record: [ADR-012](../docs/adr/ADR-012-telemetry-gateway.md). Product: [PRD-monitoring](../docs/prd/PRD-monitoring.md).
Path **A**: Prometheus + ES now, thick gateway (decode OTLP + inject labels), Mimir/Loki later.

## Plane split

| Plane | Service | Owns |
|---|---|---|
| Write | **telemetry-gateway** (new) | all device ingest: OTLP + bespoke JSON, auth by `dmon_` key, rate limit, decode, label inject, forward to Prom/ES |
| Control | console backend | resource CRUD, key issue (shown once), RBAC, alerts/channels, Grafana provisioning |
| Read | console backend | health, metrics/logs read-back, grafana-link/embed |

Shared: Postgres `monitoring_apps`, Prometheus, Elasticsearch, Grafana. Gateway gets a **read-only** DB role (key verify + tenant resolve only).

## Gateway service shape

New module (own go.mod or workspace member): `gateway/` (separate deploy/image). Reuse via shared packages, not by importing the console handler tree:
- Lift `prometheus/remotewrite.go`, `logsearch/write.go`, the key-hash verify, the rate limiter, and `sanitizeMetricName` into a shared `internal/telemetry` package both services import. Avoids fork/drift.

```
gateway/
  cmd/gateway/main.go         # config, DB pool (RO), Prom/ES clients, HTTP server
  internal/ingest/auth.go     # dmon_ key -> tenant (prefix index -> argon2 verify), scope gate
  internal/ingest/otlp.go     # OTLP decode (metrics+logs) -> shared write models
  internal/ingest/json.go     # bespoke JSON (moved from console)
  internal/ingest/handler.go  # /v1/metrics /v1/logs /v1/... + limiter + cardinality cap
```

### Key resolution by key (not appId in path)

Today auth loads by `:appId` from path. Gateway has no appId in path → resolve from the key:

1. Parse `dmon_` from `X-API-Key` or `Authorization: Bearer`.
2. `prefix = key[:13]` (`dmon_` + 8 base64url chars ≈ 48 bits) → `SELECT ... FROM monitoring_apps WHERE api_key_prefix = $1`.
3. For each candidate (normally one), `verifyMonitoringKeyHash(key, hash)` (constant-time argon2id) → first match wins.
4. Load tenant: `org_id (owner), project_id, environment, name, scopes`.
5. Scope gate: `metrics:write` for /v1/metrics, `logs:write` for /v1/logs.

Migration: **index on `api_key_prefix`** (`CREATE INDEX ... ON monitoring_apps (api_key_prefix)`). Prefix is the narrow; argon2 is the decider — collisions safe.

## OTLP → our model mapping

OTLP decode via `go.opentelemetry.io/proto/otlp/{metrics,logs,common}/v1`. Content-Type drives codec: `application/x-protobuf` (proto.Unmarshal) or `application/json` (protojson).

### Metrics → `prometheus.WriteSeries`
Walk `ResourceMetrics → ScopeMetrics → Metric → data points`.

| OTLP | → series |
|---|---|
| metric name | `__name__` = `sanitizeMetricName(name)` |
| Gauge / Sum datapoint value | series value |
| datapoint `time_unix_nano` | `TimestampMS` |
| Histogram (explicit buckets) | expand to `<name>_bucket{le=...}`, `<name>_sum`, `<name>_count` |
| Exponential histogram | **deferred** — drop + log (v1) |
| resource/datapoint attributes | labels, **capped** by `maxLabels` (cardinality guard) |
| `service.name` attr | `source` label only |
| — (authoritative) | `org_id`, `project_id`, `environment`, `monitoring_app` from DB row |

### Logs → `logsearch.AppLog`
Walk `ResourceLogs → ScopeLogs → LogRecord`.

| OTLP | → AppLog |
|---|---|
| `body` (string/kvlist) | `Message` |
| `severity_text` / `severity_number` | `Level` (upper) |
| `time_unix_nano` | `Timestamp` |
| `service.name` | `Source` |
| attributes | indexed fields |
| — (authoritative) | `OrgID`, `ProjectID`, `Environment`, `MonitoringApp` from DB row |

Guards unchanged: per-app token bucket (`ingestLimiter`), metric-count cap (413), message length (413), source length (400).

## Endpoints (gateway)

```
POST /v1/metrics                 OTLP   scope metrics:write   (appId from key)
POST /v1/logs                    OTLP   scope logs:write
POST /api/v1/metrics             JSON   scope metrics:write   (bespoke, back-compat)
POST /api/v1/logs                JSON   scope logs:write
GET  /healthz /readyz
```

## Embedded Grafana (read plane, console)

- Provisioning already done: per-project folder + per-app dashboard (`grafana/client.go`).
- Embed `/d-solo` panels or full dashboard iframe in `monitoring/[appId]/page.tsx`.
- Auth (pick at impl): Grafana `auth.proxy` with console-injected `X-WEBAUTH-USER` mapped to a per-project Grafana org/team + folder permission; OR per-org service-account token + signed embed. `allow_embedding=true`, lock `X-Frame-Options`/CSP to console origin.
- Keep native SVG sparkline (`MetricsPanel`) only for the health-card glance; rich view = Grafana.

## Onboarding UX (console) — Langfuse-style, the activation path

Replace the empty `monitoring/` white state with a guided flow. Reference: Langfuse "Time to log your first trace" (numbered steps + live waiting badge).

**Resource detail / first-run, 3 numbered steps:**
1. **Create monitoring resource** — name → POST create (already exists). If list empty, this is step 0 front-and-center.
2. **API key** — issued on create, shown once, big copy button + "store now, not shown again" (backend already returns plaintext once). Add "Manage keys" affordance.
3. **Connect your device** — tabbed code snippet, **Node.js (OpenTelemetry) first**, then Python / OTel collector / curl. Endpoint + key **prefilled**. Copy button.

**Live state badge** top of card: `● Waiting for first telemetry` (amber) → polls `GET .../health` last-seen → flips to `● Receiving` (green) on first datapoint. Mirrors Langfuse "Waiting for first trace".

### Node.js snippet (prefilled, OTLP/HTTP)
```js
import { NodeSDK } from '@opentelemetry/sdk-node';
import { OTLPMetricExporter } from '@opentelemetry/exporter-metrics-otlp-http';
import { PeriodicExportingMetricReader } from '@opentelemetry/sdk-metrics';

const sdk = new NodeSDK({
  metricReader: new PeriodicExportingMetricReader({
    exporter: new OTLPMetricExporter({
      url: 'https://ingest.dada-tuda.ru/v1/metrics',   // prefilled
      headers: { 'X-API-Key': 'dmon_xxx' },            // prefilled, shown once
    }),
  }),
});
sdk.start();
```
Or zero-code via env: `OTEL_EXPORTER_OTLP_ENDPOINT=https://ingest.dada-tuda.ru` + `OTEL_EXPORTER_OTLP_HEADERS=X-API-Key=dmon_xxx`.

## Task checklist

- [ ] shared `internal/telemetry`: lift remotewrite, eswrite, keyhash verify, limiter, sanitize (no behavior change)
- [ ] migration: index on `monitoring_apps.api_key_prefix`
- [ ] gateway scaffold: cmd/main, RO DB pool, Prom/ES clients, config, healthz
- [ ] ingest/auth: key-prefix lookup → argon2 verify → tenant + scope gate
- [ ] ingest/otlp: metrics (gauge/sum/histogram) + logs decode, proto + json codecs, `go.opentelemetry.io/proto/otlp` dep
- [ ] ingest/json: move bespoke handlers from console
- [ ] ingest/handler: routes, limiter, cardinality caps, authoritative label inject
- [ ] tests: OTLP proto+json round-trip; histogram expansion; cross-tenant isolation; scope reject; rate/cardinality limits
- [ ] helm/deploy: gateway image + public ingest ingress (`ingest.dada-tuda.ru`), RO DB secret
- [ ] console: remove bespoke ingest routes (moved) OR keep dual during cutover
- [ ] console UI: guided onboarding (3 steps + live "Waiting for first telemetry" badge + Node snippet)
- [ ] console UI: embedded Grafana panel in app detail; auth wiring
- [ ] validate against ADR-012 plan (real OTel SDK push end-to-end)

## Open / defer
- Exponential histograms (v2).
- OTLP traces (separate ADR, needs trace store).
- Embed auth method (auth-proxy vs service-account) — decide at impl.
- Mimir/Loki migration (thin-gateway rewrite) when tenant scale demands.
