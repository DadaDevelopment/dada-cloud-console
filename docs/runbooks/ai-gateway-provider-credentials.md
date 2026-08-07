# AI gateway — provider credentials, free tiers and fallbacks

**Audience:** operator answering "whose key is this burning, and what happens
when it stops paying out".
**Code:** [ADR-015](../adr/ADR-015-ai-gateway-runtime-data-plane.md), alias
catalog in `backend/internal/api/ai_catalog.go`, routing in the `ai-gateway`
repo's `config.yaml`.

Written after 2026-08-04, when the shared OpenRouter account hit zero, the
gateway answered 402 to 66 of 66 calls over three hours, every `or-*` alias
went down at once, and nothing raised a signal. Half of console chat and every
user's background memory fold got nothing. The failure was found by reading pod
logs by hand.

All numbers below were measured on prod on 2026-08-07 unless a source is cited.

---

## 1. Where the keys are

**There are no provider keys in Kubernetes.** `deploy/ai-gateway` carries no
`envFrom` and four env vars (`PYTHONUNBUFFERED`, `PYTHONPATH`,
`UVICORN_LOG_CONFIG`, `SERVICE_NAME`). Every provider key is resolved per
request from the console database, decrypted by
`POST /internal/ai/credential/get`, and bound to the deployment the router
picked — never to the alias, because a tier alias spans several providers and
which one serves is the router's decision.

The consequence worth internalising: **a project can only reach a provider it
holds a key for.** There is no platform-wide fallback key.

`ai_provider_credentials`, 12 rows:

| Owner | `project_id` | Providers |
|---|---|---|
| **platform (the only truly platform-owned key)** | `NULL` | `nvidia_nim` |
| project `platform` | `640ed82d-7be0-48e3-ab88-1e792a95cef2` | openai, anthropic, groq, openrouter, sambanova |
| project `internal` | `10000000-0000-0000-0000-000000000001` | openai, anthropic, groq, openrouter, sambanova |
| project `fin-core` | `75487ba7-5d91-4027-a20a-d8d0daa698ab` | openai |

`project_id IS NULL` is the platform-key marker (migration 079). Only
`nvidia_nim` is one. Everything else that looks platform-ish is a credential
owned by a *project named* `platform`, and it is spent like any tenant's.

### Whose balance console chat spends

`AGENT_CHAT_GATEWAY_KEY` in secret `dada-cloud-console-backend` is
`sk-dada-hc_…` — not a console-minted `sk-dada-ai-` / `sk-dada-id-` key, so the
gateway introspects it against user-service. It resolves to project
**`platform`** (`640ed82d`), confirmed in the gateway log: the `gpt-4o` and
`gpt-4o-mini` groups are called with that `project_id` and no other.

So console chat burns project `platform`'s **openai** key, and — before the
A/B split was moved off OpenRouter on 2026-08-04 — project `platform`'s
**openrouter** key. That is the balance that went to zero.

`ai_gateway_keys` has ever held three rows, all self-service test keys; none of
them is console chat's.

### Checking a balance or a limit

| Provider | Balance / limit check | Notes |
|---|---|---|
| openrouter | `GET https://openrouter.ai/api/v1/key` → `limit_remaining`, `usage` ([docs](https://openrouter.ai/docs/api-reference/limits)) | The only provider here with a real balance endpoint. A negative balance 402s **even on `:free` models** — which is exactly what happened. |
| openai | No public balance API; dashboard only | Exhaustion surfaces as `429 insufficient_quota`, not 402. |
| anthropic | No public balance API; dashboard only | Same shape. |
| groq | No balance — free plan is rate-limited, not metered. Per-response headers `x-ratelimit-remaining-requests` / `-tokens` | See §2. |
| sambanova | No balance endpoint; free tier persists indefinitely | See §2. |
| nvidia_nim | Credit ceiling removed in 2026; free tier is a 40 RPM rate limit | See §2. |

**The check that actually matters is now a metric, not a curl.** See §4.

---

## 2. Free tiers and native tool calls

Native tool calls are mandatory for anything console chat routes to — the
assistant's whole surface is tools. Measured by sending the same prompt with
and without a `tools` array through the live gateway from `agent-sandbox`.

One measurement trap worth recording: with `max_tokens=32` every reasoning
model returned `200` with empty content and no tool call, which reads exactly
like "this provider cannot do tool calls". It is truncation. Re-measured at
`max_tokens=600`, the same groups emit tool calls. Do not repeat the smaller
budget.

| Provider | Free tier | Limit | Models on the gateway | Native tool calls | Verdict |
|---|---|---|---|---|---|
| **nvidia_nim** | yes, no card | 40 RPM ([source](https://decodethefuture.org/en/nvidia-nim-api-pricing-limits-guide/)) | nemotron-3 nano/super/ultra, gpt-oss-20b, mistral-nemotron, deepseek-v4-pro, glm-5.2, 2 vision models | **yes** (measured on `fast`, `medium`, `smart`, `vision`) | **default and fallback** — the only platform-owned provider |
| **groq** | yes | 30 RPM; gpt-oss-20b 1K RPD / 200K TPD, llama-3.3-70b 1K RPD / 100K TPD, compound-mini 250 RPD ([source](https://console.groq.com/docs/rate-limits)) | gpt-oss-20b, llama-3.3-70b-versatile, compound-mini | gpt-oss-20b yes; **llama-3.3-70b 400s on any request carrying tools** | fallback only, and never for `groq-llama` |
| **sambanova** | yes, persists past the credits | 10–30 RPM by model size ([source](https://sambanova.ai/blog/sambanova-cloud-developer-tier-is-live)) | Meta-Llama-3.3-70B-Instruct | yes | fallback only |
| **openrouter** | `:free` variants only | 20 RPM / 50 RPD, 1000 RPD after $10 purchased | gpt-4.1-mini, gpt-4o-mini, gpt-4.1-mini:online | yes (paid routes) | **neither, while the balance is zero** — a negative balance 402s the free variants too |
| **openai** | none | paid | gpt-4o, gpt-4o-mini, text-embedding-3-small | yes | default, on a paid key |
| **anthropic** | none | paid | claude-sonnet-5, claude-haiku-4-5 | yes | default, on a paid key |

**The standing risk this table exposes:** every fallback chain terminates in
`nvidia_nim`, and `nvidia_nim` is 40 RPM. It is the platform's only owned
credential, so it is both the rescue and the single point of failure. A
provider outage during a traffic peak will convert into 429s from the rescuer.
Second nvidia-independent tier = the next piece of work, not something this
runbook closes.

### Measured latency, which the chain does not rescue

`medium` answered in 15s and also timed out past 120s in the same session
(`nvidia_nim/nvidia/nemotron-3-super-120b-a12b`,
`nvidia_nim/mistralai/mistral-nemotron`). A fallback restores the answer, not
the deadline: a caller with a 120s timeout that falls into `medium` can still
get nothing. Chains that must stay fast should prefer `fast`.

---

## 3. Fallbacks

`router_settings.fallbacks` in the gateway's `config.yaml`. Every chat-serving
group has one, and every chain lands on a provider that does native tool calls.

Three groups deliberately have none, and the reason is in each case that a
substitute would answer a different question:

- `vision` — nvidia_nim is the only provider here whose models read the image.
- `search` — no fallback is possible because the group is not a capability, it
  is one vendor's bundled product: `groq/groq/compound-mini` runs the search
  inside the model and, by its own catalog line, *rejects requests carrying
  tools*. Nothing substitutes for that, which is exactly the argument against
  keeping it. Web search is an ordinary tool call; modelling it as a model
  group is what created a single-provider dependency (measured: `401 no
  credential for project/provider groq` for any project without a groq key),
  and `or-gpt-41-mini-online → search` inherits it. The gateway has no
  search-shaped hole to fill — the console has no web-search tool at all, and
  no caller in this repo asks for the `search` alias. See §5.
- `text-embedding-3-small` — the free embedders serve 1024 dims against
  OpenAI's 1536. A fallback would hand back vectors that do not fit the
  caller's index. Closing this needs a dimension-matched free group, not a
  fallback line.

Known cost of the chains that do exist: a genuine client error now burns the
chain before surfacing, and a context-window overflow falls back into
smaller-window groups (measured: generic fallbacks fire on
`ContextWindowExceededError` even with `context_window_fallbacks` set), so the
error a caller finally sees may name the last group tried rather than the one
they asked for.

### Live proof

From `agent-sandbox` (`7a387969-…`), which holds **no** provider credentials of
its own. Baseline before the chains existed:

```
gpt-4o-mini -> 401 litellm.AuthenticationError: no credential for
               project/provider openai.
               No fallback model group found for original
               model_group=gpt-4o-mini
```

After (gateway image `master-1.0.0-24`):

```
{"model": "or-gpt-41-mini", "tools": true, "status": 200,
 "served": "nvidia_nim/openai/gpt-oss-20b", "tool_calls": true}
{"model": "gpt-4o-mini",    "tools": true, "status": 200,
 "served": "nvidia_nim/openai/gpt-oss-20b", "tool_calls": true}
```

and the degradation is stated out loud in the gateway log:

```
{"log": "ai_gateway", "kind": "fallback", "requested": "or-gpt-41-mini",
 "served_group": "fast", "model": "nvidia_nim/nvidia/nemotron-3-nano-30b-a3b",
 "project_id": "7a387969-e082-415c-8b61-1f53f7e18295"}
```

`or-gpt-41-mini` is the alias that served 50% of console chat on 2026-08-04 and
returned nothing. A project with no OpenRouter key at all now gets a 200 with
working tool calls.

---

## 4. The signal

An exhausted balance used to raise nothing. It now raises a Prometheus alert.

The gateway posts every upstream refusal and every fallback to the console's
`POST /internal/ai/failure/record`, which increments:

- `dada_ai_upstream_failures_total{model_group,provider,status}`
- `dada_ai_fallbacks_total{requested,served}`

Rules live in `helm/dada-cloud-console/templates/prometheusrule.yaml`, group
`dada-cloud-console.ai`:

- **AIModelGroupRefused** — `status=~"402|429"` sustained above
  `aiUpstreamFailureRate` (default 0.2/min, from OpenRouter's measured ~0.4/min
  on 2026-08-04) for 10m, `keep_firing_for: 30m`. `for:` alone gates only the
  rising edge, so a refusal stream that dips for one scrape would otherwise
  resolve and re-fire as a brand-new alert.
- **AIServedByFallback** — `rate(dada_ai_fallbacks_total[15m]) > 0` for 15m,
  `keep_firing_for: 1h`, severity info. This is the one that catches an outage
  the chains fully absorb: callers are fine, and an upstream is still down.

End-to-end check (both replicas, because the gateway hits the service and
either can answer):

```bash
kubectl -n argocd-prod exec deploy/dada-cloud-console-backend -- \
  wget -qO- http://127.0.0.1:8080/metrics | grep '^dada_ai_'
```

Measured after driving refusals and fallbacks through the live gateway (two
replicas, sampled separately — `sum()` over them is the real count):

```
dada_ai_fallbacks_total{requested="or-gpt-41-mini",served="fast"} 2
dada_ai_fallbacks_total{requested="or-gpt-41-mini",served="fast"} 4
dada_ai_upstream_failures_total{model_group="groq-gpt-oss",provider="groq",status="429"} 1
dada_ai_upstream_failures_total{model_group="or-gpt-41-mini",provider="openrouter",status="401"} 1
dada_ai_upstream_failures_total{model_group="or-gpt-41-mini",provider="openrouter",status="401"} 2
dada_ai_upstream_failures_total{model_group="search",provider="groq",status="401"} 2
```

The `groq` 429 in that sample is not from the probe — it is live traffic
hitting groq's free-tier rate limit, which the gateway log had been recording
27 times per window with nobody reading it. That is the first time a free-tier
limit on this platform has been visible as a number an alert can query.

The provider label needed a fix to get there. `metadata["provider"]` is only
written once the deployment hook resolves a credential, so every failure raised
*by* that hook arrived as `provider="unknown"` — blank in precisely the case
the metric exists for. Resolved from the deployment target instead
(ai-gateway `f0301f7`).

Labels are bounded against the console's own alias catalog; a name the console
does not know counts under `other` rather than being dropped, so a drift
between the two repos shows up as a visible bucket instead of as silence.

---

## 5. Still open

- **One rescuer.** Every chain ends at nvidia_nim, 40 RPM, one key. Needs a
  second free tier that is not nvidia and does native tool calls.
- **Web search is modelled as a model group instead of as a tool.** That is the
  whole reason it is provider-bound. `search` resolves to groq's compound
  bundle, which cannot accept tools, so it can never take part in the
  tool-calling conversation the assistant actually runs; meanwhile no code in
  this repo calls the alias, and the assistant's toolset
  (`backend/internal/agentchat/toolset.go`) contains no web-search tool.
  A search tool behind the existing `LoadToolTool` catalog would work on every
  tool-calling group already on the gateway — `fast`, `medium`, `smart`,
  `gpt-4o*`, `or-*` — which retires the dependency rather than documenting it.
  Until then the console has no web search, and `search` /
  `or-gpt-41-mini-online` are dead weight for every project without a groq key.
- **Embeddings have no fallback** and cannot get one until a 1536-dim free
  embedder exists on the gateway.
- **`AGENT_CHAT_MEMORY_MODEL` is still unset**, so background memory folding
  inherits `AGENT_CHAT_MODEL`. That coupling is what took folding down for
  *all* users on 2026-08-04 while the A/B split only took down half of chat.
  The chains cover it now; the coupling is still there.
- **OpenRouter's balance is the owner's call.** Until it is topped up no `or-*`
  alias reaches OpenRouter — they are answered by the fallback chain instead.
