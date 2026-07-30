# Box time-to-ready budget — what it means and how it is enforced

Product mandate: **a box is ready in seconds.** This is the one claim marketing
cannot compensate for — a box that is not ready in seconds is not a body for an
agent, and no amount of positioning fixes that. So it is enforced the same way the
`<300ms` API rule is (`latency-budget.md`): a const budget in code, a histogram, a
separate breach counter, and an alert.

## The definition, precisely

**T0** — the instant the spawn request is admitted server-side: the first statement
after auth, quota and the spend-cap precheck. Not when the user typed the command
(unmeasurable), and not after the pool pick — that would hide queueing, which is
exactly what breaks under load.

**T1** — the instant the orchestrator receives the **exit status** of the canary
executed inside the box (`box.CanaryCommand`).

The rejected alternatives, each of which hides a real failure:

| Cheaper stop point | What it would let through |
|---|---|
| "the API returned" | measures our JSON encoder, not a body for the agent |
| "SSH accepted TCP" | a listener that accepts and then hangs on key exchange or PAM — half of real "fast boot, slow ready" failures live there |
| "first byte of output" | a box that prints a banner and stalls |

The canary also probes the toolchain (node, python, go, git, docker), not just the
channel. **The warm image is the product**: a box that boots in two seconds and
then makes the agent run `apt install` has not delivered anything. In a genuinely
warm image these probes cost tens of milliseconds, so including them is free — and
it makes it structurally impossible to ship a fast-booting box with a cold image.

**Measurement integrity:** every phase timestamp is taken by the orchestrator
observing the box. A guest-reported time is never trusted — the clock inside a
machine that just booted is exactly the clock that is wrong.
`box.CanaryResult.GuestClaimedAt` exists so it can be logged and compared, never
so it can be measured with, and `TestSpawnIgnoresGuestReportedTime` pins that.

## Phases

Measured separately so a regression names its own culprit instead of hiding behind
a fast total. Order is fixed (`box.CriticalPath`); completing out of order or twice
is an error, because a total that does not equal the sum of its parts is unreadable
on a dashboard.

| Phase | From → to |
|---|---|
| `admit` | request accepted → placement decided |
| `pool_pop` | placement → warm instance claimed (≈0 on a hit; a whole cold boot lands here on a miss) |
| `boot` | claimed → tenant identity bound |
| `net` | bound → out of quarantine into tenant egress |
| `auth` | reachable → exec channel accepts the customer's key (key injection is the classic hidden second) |
| `canary` | channel up → canary exit status received |

## Targets

| Path | Metric | Target |
|---|---|---|
| Warm hit | `dada_box_ready_duration_seconds` p50 | ≤ 3.0 s |
| Warm hit | p95 | ≤ 8.0 s (`metrics.alerts.boxReadyP95Seconds`) |
| Warm hit | budget breach threshold | 10 s (`metrics.BoxReadyBudget`, mirrored in `metrics.alerts.boxReadyBudgetSeconds`) |
| Warm hit | any single spawn | ≤ 15 s — hard failure, asserted by the nightly rehearsal |
| Pool miss | p95 | ≤ 45 s |
| Pool miss rate | `dada_box_pool_misses_total / spawns` | ≤ 2 % |

The number quoted publicly is **client-perceived p50 on the warm path**
(`dada_box_client_ready_duration_seconds`), published alongside p95, and it comes
from production rather than from a spreadsheet.

## The three layers that keep us honest

### Layer A — per PR, blocking, hermetic

Seconds cannot be measured in a CI pod without a hypervisor. The **shape** of the
critical path can be, and that is where regressions in this class actually come
from: someone adds a serial step — another provisioning call, a synchronous DNS
wait, a second database round trip before ready — and the p95 slides a week later
in production.

`backend/internal/box/readypath_golden_test.go` pins the ordered step list against
`backend/tests/golden/box/ready-path.txt`. Adding a step fails the PR with a
one-line diff. If the change is intended:

```
cd backend && go test ./internal/box -run TestReadyPathGolden -update-golden
```

and justify the new step in the PR. **Work that can happen after the box is handed
to the customer does not belong on this list.**

The same package also pins what "ready" means
(`TestReadinessRequiresTheWarmToolchain`), that phases are disjoint and sum to the
total, that an inconsistent timeline fails the spawn instead of publishing a wrong
number, and that pool claims are exactly-once under concurrency.

`backend/internal/metrics/box_surface_test.go` pins the metric surface against
`backend/tests/golden/box/metrics.txt`, and `TestAlertedMetricsAreDeclared` proves
every `dada_*` series referenced by an alert rule is a metric that actually exists —
closing the hole where renaming a metric silently kills the alert watching it. It
is a static scan rather than a registry lookup on purpose: an unused `CounterVec`
has no children, so a registry-based check would pass while the metric was
misnamed.

### Layer B — nightly, live

`scripts/box-rehearse.sh` (not yet written — see `tasks/box-backlog.md` phase 1)
runs 30 real spawns against staging and asserts p50 ≤ 3.0 s, max ≤ 15 s, pool
misses ≤ 2 %, and each phase's p50 inside its own sub-budget so "boot got slower"
cannot hide behind a fast total.

It deliberately does **not** gate on p95: 30 samples cannot resolve a p95 honestly.
p95 is the tracked SLO from production histograms; a small-N gate uses p50 and max,
which is statistically defensible.

### Layer C — production truth

A synthetic canary runs in-cluster every 5 minutes from a dedicated org and feeds
`dada_box_client_ready_duration_seconds`.

## Alerts

Group `dada-cloud-console.box` in
`helm/dada-cloud-console/templates/prometheusrule.yaml`.

| Alert | Fires on | Severity |
|---|---|---|
| `BoxReadyBudgetBreached` | `rate(dada_box_ready_budget_breaches_total[10m]) > 0`, by phase | warning |
| `BoxReadySLORegressed` | p95 over `boxReadyP95Seconds` for 15m | warning |
| `BoxPoolExhausted` | `dada_box_pool_available == 0` for 5m | warning |
| `BoxCrystallizeStateLoss` | any increase in `dada_box_crystallize_state_loss_total` | **critical** |
| `BoxMeterStale` | newest metered minute older than `boxMeterStaleSeconds` | warning |
| `BoxFailedRecent` | `dada_box_failed_recent > 0` for 5m | warning |

`BoxReadyBudgetBreached` carries the **dominant phase** of the breaching spawn as a
label, so the alert that fires already names the culprit — check
`dada_box_phase_duration_seconds` for that phase, then `dada_box_pool_available`:
the usual cause is an exhausted warm pool turning every spawn into a cold start.

`BoxCrystallizeStateLoss` is the only critical one. The promise is that the same
object continues living; one loss teaches the customer not to trust the graduation
path, which severs the monetization ladder at its second step. Treat it as
customer-visible data loss: find the operation, read its `validation_result`
equivalence report, and contact the customer before they find out themselves.

## Which knobs live where

| Value | Owner | Consumer |
|---|---|---|
| Breach threshold | `metrics.BoxReadyBudget` in `backend/internal/metrics/box.go` | the breach counter |
| Breach threshold, for alert text | `metrics.alerts.boxReadyBudgetSeconds` in `values.yaml` | annotation text only |
| p95 SLO | `metrics.alerts.boxReadyP95Seconds` | `BoxReadySLORegressed` |
| Meter staleness | `metrics.alerts.boxMeterStaleSeconds` | `BoxMeterStale` |

The Go const is authoritative for enforcement; the chart values only render into
alert text and expressions. **Keep them in step** — a mismatch means the alert
describes a budget the backend is not enforcing.

## Related

- `docs/plans/2026-07-29-box-test-and-measurement.md` — the full test inventory
- `docs/plans/2026-07-29-box-runtime-architecture.md` — the phase-by-phase budget
  and where the time actually goes
- `tasks/box-backlog.md` — remaining phase-1 work (rehearsal script, nightly job,
  funnel instrumentation)
- `latency-budget.md` — the API latency rule this pattern is copied from
