# API latency budget — the &lt;300ms rule &amp; how it is enforced

Owner mandate (2026-07-18): **every backend endpoint answers in under 300ms; simple
read endpoints under 50ms.** This is a hard product rule, not a nice-to-have — a slow
console reads as a broken console.

## The three layers that keep us honest

### 1. Measurement (always on)

Every request is timed by `internal/metrics.HTTPMiddleware` and recorded into the
Prometheus histogram `http_server_request_duration_seconds{method,route,status}` (the
`route` label is the matched Gin template, so cardinality stays bounded). Query live
per-endpoint p95:

```
histogram_quantile(0.95, sum by (route, le) (rate(http_server_request_duration_seconds_bucket[5m])))
```

Scrape it in-cluster: `kubectl -n argocd-prod exec deploy/console-backend -- wget -qO- http://localhost:8080/metrics`.

### 2. Enforcement signal (durable, M0)

The same middleware enforces the budget. Any request slower than the budget
(`slowRequestBudget = 300ms`, in `internal/metrics/http.go`):

- logs a `WARN` line: `slow request: exceeded latency budget` with `route`, `method`,
  `status`, `duration`;
- increments the counter `dada_http_slow_requests_total{method,route}`.

Alert on it — a nonzero rate means an endpoint regressed the moment it shipped:

```
sum by (route) (rate(dada_http_slow_requests_total[5m])) > 0
```

### 3. Cache-aside for slow upstreams

Analytics/observability endpoints fan out to slow upstreams (per-tenant Mimir range
queries, OpenSearch log search, OpenCost aggregation). They serve from the fail-open
Redis cache-aside layer (`internal/cache`) so one upstream round-trip backs a burst of
dashboard refreshes. A Redis outage degrades latency only, never correctness.

| Surface | Cache key prefix | TTL knob | Default |
|---------|------------------|----------|---------|
| Money/billing (cost, meter, consumption) | `cost:*` | `CACHE_COST_TTL_SECONDS` | 300s (warmed) |
| Metric panels + resource health (VM/app/resource) | `metrics:*`, `health:*` | `CACHE_METRICS_TTL_SECONDS` | 20s |
| Aggregated log search | `logs:*` | `CACHE_LOGS_TTL_SECONDS` | 10s |

The money path additionally uses a background warmer (`StartCostCacheWarmer`) so a user
never pays a cold OpenCost aggregation. Do **not** put a cold warm on the startup path —
a slow first warm crash-loops the pod past the liveness probe.

## When you add or change an endpoint

1. If it calls a slow upstream (Mimir/OpenSearch/OpenCost/external API) or fans out a
   loop of queries, wrap the work in `cache.Fetch` with a short TTL keyed by
   route + project/resource id + query args, and prefer concurrent fan-out over a
   serial loop.
2. Run the suite and watch `dada_http_slow_requests_total` after rollout. If your route
   shows up, it is over budget — fix it before calling the work done.
