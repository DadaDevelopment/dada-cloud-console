# ADR-015: AI Gateway Runtime and Data-Plane Architecture

## Status

Proposed — 2026-07-17

Implementation is gated **only** by the remaining operational validations listed under
[Remaining Risks](#remaining-risks) (G4 cancellation propagation, G9 introspection write
amplification, G10 subscription-pooling product question, real-Gemini redaction confirm,
LiteLLM upgrade-compat). It is **not** gated by any unresolved core extension-point
assumption: the load-bearing hook behaviour was empirically proven against the pinned
runtime (see [G3 Empirical Evidence](#g3-empirical-evidence)). G4, G9 and G10 are
explicitly **non-blocking** for ADR approval — they can change cancellation/operational
hardening but cannot change the selected data-plane placement or runtime composition.

Related: [ADR-008: KServe](008-kserve.md), [ADR-012: Telemetry Gateway](ADR-012-telemetry-gateway.md), [product-gtm-vision](../product/product-gtm-vision.md), [placement diagram](../architecture/ai-gateway-placement.svg)

---

## Context

The platform needs an LLM gateway: an OpenAI-compatible inference surface with
project-scoped bring-your-own-key (BYOK) credentials, streaming, token-accurate usage
billing, and provider translation across many upstreams. A full inventory established that
the platform already owns auth, tenancy, RBAC, quotas, billing, secrets, dashboard and
GitOps provisioning; the missing half is exclusively the LLM-provider data plane
(provider registry, adapters, OpenAI/Anthropic translation, routing, streaming, token
metering). None of that exists today.

Four seams were validated against real code, the live cluster, and an empirical spike
before this decision. The findings invalidated the intuitive "put it behind the existing
API gateway" design:

- **Spring Cloud Gateway (`cloud/gateway`) cannot be the AI policy seam.** It is reactive
  WebFlux/Netty [code `pom.xml:60-63`] but authenticates **Keycloak JWT only** — `sk-dada`
  / API-key / introspection paths are absent [code `SecurityConfiguration.java:120-124`;
  rg rc=1]. It performs **no** tenant resolution, **no** request-body mutation, and has
  **no** credential-store access (all NOT FOUND [code]). Adding per-request BYOK injection,
  quota reservation, credential decryption and usage correlation as Spring filters would
  build an AI Gateway Runtime inside the process that fronts every platform API — the
  "hidden AI runtime" anti-goal — and it still could not settle usage (post-provider).
  Its live deploy is unfit for streaming: 1 replica, no HPA, 30s grace, no preStop [live].
- **The Gin console backend is not an inference data plane.** The existing proxy fully
  buffers request and response and hardcodes a 60s client timeout [code
  `inference.go:23,155,191,227`]; the deployment is 1 replica / 512Mi / `Recreate`
  [code `values.yaml:76,181`; `backend-deployment.yaml:11`]. No streaming primitive exists
  anywhere in the backend.
- **The public ingress defaults are hostile to LLM traffic.** `ingress-nginx-pub` has an
  empty ConfigMap [live] → binary defaults: `proxy-body-size 1m` (media → 413) and
  `proxy-read-timeout 60s` (long completions / SSE → 504). `proxy-buffering` defaults off
  (good for SSE). The existing `codex-lb` ingress already carries the correct override set
  (buffering off, timeouts 3600, body 50m, http1.1) [live] — a ready template.
- **`codex-lb` is not a gateway.** It is a single-upstream ChatGPT/Codex account-pool proxy
  (Python/FastAPI) whose only upstream is `chatgpt.com/backend-api`
  [code `settings.py:132`], with no outbound provider abstraction, no `tenant`/`project`
  concept, and its own `sk-clb`/static-key auth [code `dependencies.py:29`, grep tenant=0].
  Production-grade code but intentionally dormant (scaled to 0 for cluster memory headroom,
  argo commit `219f1b4`, PVCs retained) [code/live]. Its genuine, non-replicable value is
  **pooling ChatGPT subscriptions** into an API — a capability LiteLLM cannot provide.

The `sk-dada` key contract was read end-to-end: SHA-256-hashed keys owned by user-service
[code `ApiKeyUsecase.java:58,128-135`], wire format `sk-dada-<43-char base64url>`
[code `ApiKeyUsecase.java:33-34,49-51`], introspected at `POST /v1/apikeys/introspect` with
the key in the JSON **body** under `permitAll` in-cluster trust
[code `ApiKeyController.java:104`; `DevConfig.java:69`], returning
`valid`/`principal_id`/`project_id`/`org_id`/`scopes`
[code `ApiKeyIntrospectResponseDto.java:15-22`]. Scopes are a free-form `VARCHAR(1024)`
column with no vocabulary validation [code `api_keys.sql:11`; `ApiKeyUsecase.java:119-126`].
The standard header extractor already accepts `Authorization: Bearer <key>`
[code `telemetry/keys.go:52-53`].

Finally, the decisive extension-point question — can LiteLLM plugin hooks implement
project-scoped dynamic credential injection and metadata propagation across the MVP
endpoints — was answered empirically, not from documentation, against the exact pinned
runtime (see [G3 Empirical Evidence](#g3-empirical-evidence)).

---

## Decision

**The AI Gateway Runtime will be implemented as a pinned LiteLLM distribution extended with
first-party platform plugins, deployed as an isolated, highly-available data-plane service
behind a dedicated ingress.**

LiteLLM is not "merely a container" and the plugins are not incidental configuration: the
plugins are the platform policy layer and, together with LiteLLM's provider-translation
engine, they constitute the runtime. The runtime consists of:

- **LiteLLM 1.92.0, pinned** — provider adapters, OpenAI/Anthropic translation, routing,
  streaming, model cost map.
- **`custom_auth`** — validates the inbound `sk-dada` key via user-service introspection and
  resolves the project.
- **`async_pre_call_hook`** — model-alias authorization, project-scoped BYOK injection,
  quota reservation, and platform request-id minting.
- **success and failure accounting callbacks** — settle reservations and emit usage into the
  platform ledger.
- **platform-owned request IDs, quota reservations and usage-ledger integration** — the
  first-party accounting surface described in [Usage Ledger](#usage-ledger).

The existing **Spring Cloud Gateway is not in the AI inference path**. The existing
**`codex-lb` is not the platform AI Gateway**; it may remain only as an optional upstream for
ChatGPT-subscription pooling if that product requirement remains active (see
[G10](#remaining-risks)).

### Decisions to record

1. **Dedicated ingress + isolated HA LiteLLM runtime.** LiteLLM runs as its own
   deployment (≥2 replicas, HPA, `RollingUpdate` + preStop drain + raised grace) behind a
   dedicated ingress that overrides the hostile defaults (buffering off, timeouts raised,
   body size raised) using the proven `codex-lb` annotation set [live]. It is a distinct
   failure domain from the console and from the Spring gateway.
2. **Existing `sk-dada-*` keys extended with `ai:*` scopes.** No new key type. The
   free-form `scopes` column requires no user-service or schema change
   [code `api_keys.sql:11`]; AI keys are minted through the existing admin
   `POST /v1/apikeys` with the target `project_id`/`org_id` and `ai:*` scopes.
3. **OpenAI SDK compatibility through `Authorization: Bearer sk-dada-*`.** The OpenAI SDK
   sends this shape natively; no custom client code is required. Server-side, `custom_auth`
   strips `Bearer ` and introspects with the key in the request **body** — the key MUST NOT
   be forwarded as a Bearer header to user-service (its JWT filter would reject a non-JWT)
   [code `ApiKeyIntrospectRequestDto.java:17`; `DevConfig.java:75-76`].
4. **User-service introspection cached for at most 60 seconds.** Introspection is a DB
   lookup plus a `last_used_at` write on every call [code `ApiKeyUsecase.java:104-106`];
   the runtime MUST cache results locally with TTL ≤ 60s, fail-closed on a miss during an
   outage window it cannot confirm, and serve-stale only on a proven outage — mirroring the
   existing consumer pattern [code `introspect.go:79,102-116`]. TTL bounds the
   revocation-propagation window (revocation is soft-delete `revoked_at`, no push
   invalidation [code `ApiKeyDbAdapter.java:57-68`]).
5. **Project-scoped BYOK injected in `async_pre_call_hook`.** The hook selects the
   project's provider credential and sets `api_key` + `api_base` on the request before the
   provider client is constructed. Proven in G3.
6. **Provider secrets remain encrypted in the platform backend and never enter argo-infra
   git.** The backend owns decryption (`GitopsEncryptionKey`, env_vars AES-GCM
   [code `envvars.go:211`]) and exposes a decrypted credential over an internal channel to
   the runtime. Keys are never rendered into the GitOps repo, whose secret channel is
   plaintext `stringData` [code `renderer.go:386-389`].
7. **Platform Postgres usage ledger is the source of truth.** See
   [Usage Ledger](#usage-ledger).
8. **The LiteLLM callback payload is the primary source of actual provider usage** that
   feeds the ledger. It is not itself the source of truth.
9. **Redis is used only for hot reservations, rate limits and counters.** It is fail-open;
   the durable truth is Postgres.
10. **MVP endpoints:** chat completions (streaming and non-streaming); embeddings; audio
    transcription; and — optionally — image generation, included because G3 proved the same
    hook path works there.
11. **AI Studio / KServe remains a sibling product** [ADR-008]. Custom-model serving
    ("host your own model") and the AI Gateway ("proxy to LLM providers") are two products
    under one AI category, sharing auth/quota/billing/dashboard, not sharing serving
    internals.
12. **LiteLLM's management UI, teams, virtual-key database and budgets are not adopted.**
    The platform already owns keys, tenancy, quotas, billing and dashboard; adopting
    LiteLLM's parallel versions would run a second, worse copy of the platform.

---

## Architectural Escape Criteria (Validity Envelope)

This decision is scoped. The failure modes that matter for the future are not Spring Gateway,
ingress, or field names — they are product scenarios that outgrow a single-invocation proxy.
This section is the normative boundary: the condition under which the decision holds, and the
triggers to extract a first-party orchestration runtime. It is forward-looking; the thresholds
are HYPOTHESIS about future scenarios, not claims about current behaviour.

**Invariant.** LiteLLM plugins remain *the runtime* while a request maps to exactly **one
platform-selected provider invocation** and plugin logic is limited to authentication,
authorization, credential injection, reservation and accounting events. Outside that envelope
LiteLLM is **demoted from "the runtime" to "the adapter/translation layer"**, and the platform
**MUST** introduce a first-party orchestration runtime in front of it.

Rule of thumb: if a policy can be expressed as "pick a credential and add metadata", a plugin
fits. If a policy **creates new provider attempts or changes the request lifecycle**, it needs
its own orchestrator.

**Extract the first-party orchestrator at the FIRST of these triggers:**

1. One user request creates **multiple provider attempts** — cross-provider fallback, hedged
   or speculative execution, judge/critic pass, moderation side-call, STT→LLM→TTS, OCR→vision,
   embed→N indices.
2. A **managed-key / shared-balance / prepaid / markup** product appears — billing becomes a
   financial transaction system (transactional fund authorization, provider-invoice
   reconciliation), not post-hoc accounting.
3. A **durable async job lifecycle** appears — video generation, batch, fine-tuning, provider
   Files API, assistants/threads, webhooks, scheduled/queued GPU work.
4. **Provider-native persistent resources** appear — Threads, cached content, Files, vector
   stores, assistants — requiring a platform↔provider↔account↔resource-id↔expiry mapping.
5. **Routing policy becomes a product engine** — region/residency/price/latency/capability/
   SLA/AB/canary selection — not an alias→provider table.
6. **Hooks begin to own retries, workflow, or stream transformation** — server-side tools,
   agent loops — i.e. plugins make product decisions instead of adapting platform context.

**Where it breaks first (likely order):** (1) managed-key billing + shared pools → (2)
cross-provider routing/fallback → (3) async jobs/files/video → (4) `async_pre_call_hook`
growing into a policy monolith → (5) multi-region + enterprise credentials → (6) attempt-level
accounting.

**Threshold per axis:**

| Axis | MVP is fine for | Extract first-party runtime when |
|---|---|---|
| Request shape | one request → one provider call | one request → multiple provider attempts |
| Money | BYOK, user pays provider, post-hoc accounting | managed key / shared pool / prepaid / markup |
| Job lifecycle | sync request/response | durable async (video/batch/fine-tune/Files/assistants) |
| Provider resources | stateless `messages[]` | provider-native persistent entities |
| Routing | alias → provider table | routing is a policy engine |
| Plugin scope | auth / authz / inject / reserve / account | plugin makes product decisions |
| Auth | opaque introspection + ≤60s cache | high cache-miss / multi-region / instant revocation |
| Region | single region beside backend/DB | second active region or data-residency requirement |
| Streaming | SSE passthrough | stream is a state machine (moderation/tool interception/resume/fan-out) |
| Observability | final-state callback | attempt-level cost/timings, SLA, enterprise disputes |
| Credential | `api_key` + `api_base` | IAM roles / STS / workload identity / OAuth / mTLS / Vault |
| Config | near-static, one runtime | frequent routing/config change or multi-replica dynamic push |

**Guardrails that keep the escape cheap (already in this ADR):**

- The ledger carries **`provider_attempt_id`** → attempt-level accounting and cross-provider
  fallback need a new attempt loop above LiteLLM, not a schema rewrite.
- Credential resolution returns a **reference** and the backend owns the secret (Decision 6) →
  a credential-type system (Bedrock IAM/STS, Azure/GCP workload identity, OAuth, mTLS, Vault)
  can be added without changing the plugin contract; plugins select a credential reference,
  they need not receive the raw secret.
- Config **SHOULD** carry policy/model/credential revision ids stamped onto each request →
  control-plane/data-plane divergence and "which config served this request" stay auditable.
- Auth via opaque introspection is the MVP; the documented escape is exchanging `sk-dada` for
  a short-lived, locally-verifiable signed token when cache-miss rate, multi-region, or
  instant-revocation requirements make per-request introspection a bottleneck or SPOF.

These guardrails are why the eventual migration is a **demotion of LiteLLM to an adapter
layer**, not a rewrite. The product framing that keeps this ADR valid: *an OpenAI-compatible
BYOK gateway for synchronous inference with simple one-request-to-one-provider routing*. It
begins to obstruct when the product becomes *a universal AI execution platform that itself
orchestrates models, tools, retries, jobs, shared money and provider-native resources*.

---

## G3 Empirical Evidence

The extension-point assumptions were validated by running a real LiteLLM proxy with a
custom plugin against two mock OpenAI-compatible upstreams. Every result below is backed by
a recorded artifact in the scratch directory `g3-spike/` (mock request logs, plugin event
log, proxy debug log). Secrets are not copied here; the artifacts record only redacted
credential labels.

- **Tested version:** LiteLLM **1.92.0** (pinned; installed clean; `openai` 2.45.0,
  python 3.10). `litellm.__version__` is absent in 1.92.0 — use
  `importlib.metadata.version("litellm")`.
- **chat completions, non-streaming:** PASS — `call_type=acompletion`.
- **chat completions, streaming:** PASS — SSE streamed; identical hook behaviour to
  non-streaming.
- **embeddings:** PASS — `call_type=aembedding`.
- **multipart audio transcription:** PASS — `call_type=transcription`; the uploaded file is
  not re-buffered by the hook (mock received the full multipart body).
- **image generation:** PASS — `call_type=image_generation`; included because it shares the
  same hook.
- **Hook mutation occurs before provider client construction.** The model-list default
  `api_base` was an intentionally dead port; every request still reached the live mock,
  proving the hook overrode `api_base` and `api_key` before the client was built. The
  static credential/base are not present in `data` at hook time (they live on the router
  deployment, applied after the hook).
- **Project metadata and usage reach the accounting callback.** `async_log_success_event`
  received `platform_request_id`, `project_id`, and a `usage` object
  (e.g. `{prompt 11, completion 7, total 18}`) per request.
- **Zero credential crossover under concurrent two-project testing.** Under 66-way
  concurrency with two projects on the same `api_base` differing only by credential, every
  success event's credential matched its project (projectA→CRED_A, projectB→CRED_B), zero
  crossover. The LiteLLM client cache key includes `sha256(api_key)` + `api_base`
  [code `llms/openai/common_utils.py:get_openai_client_cache_key`], so no client is shared
  across credentials; credential rotation produced no stale-client serving.
- **`litellm_call_id` is stable across internal LiteLLM retries** (one id spans retry
  attempts and both the success and failure callbacks).
- **OpenAI client retries create new logical requests.** The OpenAI SDK's own
  `max_retries` (default 2) mints new logical requests and new ids — the client MUST set
  `max_retries=0`, or billing MUST dedupe on the platform id (see hardening).

---

## Mandatory Hardening

These are normative requirements for any implementation of this ADR.

- Every request **MUST** receive a platform-selected `api_key` and `api_base`. The runtime
  **SHALL NOT** rely on static model-list credentials or user-supplied credentials as a
  functional default.
- Static model credentials **MUST** be invalid or otherwise unusable, so that a skipped or
  failed hook fails closed rather than silently using a shared default. (G3 disproved the
  safety of static/user-supplied defaults: a user body `api_key` overrode the static
  credential and bled into later credential-less requests via the pooled deployment client.)
- User-supplied credential and base-url fields (`api_key`, `api_base`, `extra_body`)
  **MUST** be removed in the hook before the platform injects its own values.
- Production message logging **MUST** be disabled (`turn_off_message_logging: true`).
- DEBUG logging **MUST** be disabled in production.
- The inbound `sk-dada` bearer **MUST** never appear in logs or traces. (G3 observed the
  inbound bearer logged in plaintext under `--detailed_debug`; provider credentials
  themselves were redacted.)
- Billing idempotency **MUST** use `platform_request_id`, not only `litellm_call_id`.
- OpenAI client examples **MUST** set `max_retries=0`, or **MUST** document that client
  retries create new billable logical requests.
- The usage ledger **MUST** support request-start, reservation, success, failure,
  expiration and reconciliation events.
- Gemini remains **conditionally enabled** pending one real-provider confirmation that the
  provider credential travels as a header (`x-goog-api-key`) and is redacted from logs — G3
  observed header auth with no `?key=` URL leak in 1.92.0 (mock path), superseding the
  earlier documentation-based URL-leak concern, but this MUST be confirmed against the real
  `generativelanguage` endpoint before Gemini BYOK is enabled.

---

## Routing Limitation

Dynamic BYOK injection with a fixed `api_base` **pins a logical LiteLLM request to one
provider**. Therefore the MVP does not guarantee transparent cross-provider fallback for
project-scoped BYOK credentials. Three distinct behaviours must not be conflated:

1. **Internal retry within one selected provider** — LiteLLM retries the same
   deployment/credential; supported.
2. **Fallback among statically configured deployments** — LiteLLM Router fallback across
   `model_list` deployments; supported for statically configured credentials, but the
   platform-injected fixed `api_base` overrides this for BYOK, so BYOK requests do not fan
   out.
3. **Future platform-managed cross-provider fallback** — requires the platform policy layer
   to start a **new logical provider attempt with a separately resolved credential**
   (a new `provider_attempt_id` and a re-run of credential resolution). This is out of MVP
   scope and is the reason the accounting schema carries a `provider_attempt_id`.

This limitation is recorded in both the [Consequences](#consequences) and the
[MVP Boundary](#mvp-boundary).

---

## Usage Ledger

Source model:

- **Platform Postgres usage ledger = source of truth.**
- **LiteLLM success/failure callbacks = primary source of observed provider outcome and
  actual usage** (they feed the ledger; the callback transport is not itself the source of
  truth).
- **Pre-call runtime events = source of reservation and request-start state.**
- **Reconciliation events = source of later corrections** (unmapped-cost rows, missing
  callbacks, disputes).

Identifiers (the schema MUST NOT depend solely on `litellm_call_id`):

- **`platform_request_id`** — minted by the platform in `async_pre_call_hook`, threaded
  through LiteLLM `metadata`, round-tripped into the callback. Billing idempotency anchor.
- **`provider_attempt_id`** — platform-minted per provider attempt; the unit at which a
  future cross-provider fallback re-resolves a credential and accrues separate cost.
- **`litellm_call_id`** — LiteLLM's per-logical-request id; stable across LiteLLM internal
  retries (G3). Correlation, not the sole billing key.
- **`provider_request_id`** — the upstream provider's own response id (e.g. the OpenAI
  response `id`); retained for dispute resolution and provider-side trace correlation.

Ledger events: `request_start`, `reservation`, `success`, `failure`, `expiration`,
`reconciliation`. Redis holds only the hot reservation, rate-limit and counter state; it is
fail-open and never authoritative.

Reserve → settle → reconcile lifecycle: the pre-call hook reserves an estimated maximum cost
against the project budget in Redis and writes a `request_start` + `reservation` event; the
success/failure callback writes the actual usage and cost (dedupe on `platform_request_id`)
and settles the reservation; unsettled reservations expire; a periodic reconciliation flags
unmapped-cost rows (LiteLLM returns cost 0 for models absent from its price map) and
corrects the ledger.

---

## Alternatives Considered and Rejected

1. **Spring Cloud Gateway as the AI policy runtime.** Rejected: it cannot authenticate
   `sk-dada`, resolve tenant, reach the credential store, or mutate request bodies today
   (all NOT FOUND [code]); implementing them there builds a hidden AI runtime inside the
   all-platform edge, degrades failure isolation, and still cannot settle usage. Its live
   deploy (1 replica / 30s grace / no preStop [live]) is unfit for streaming.
2. **Existing Gin backend as an inference proxy.** Rejected: fully buffered, 60s hardcoded
   timeout, 1-replica / 512Mi / `Recreate`, no streaming primitive [code/live]. Wrong
   shape and wrong failure domain.
3. **`codex-lb` as the platform gateway.** Rejected: single-upstream ChatGPT-account proxy
   with no outbound provider abstraction and no tenant model [code `settings.py:132`,
   grep tenant=0]. Retained only as an optional subscription-pooling upstream (G10).
4. **Reimplementing provider adapters in first-party code.** Rejected: the provider
   translation/adapter surface is exactly the high-maintenance grunt-work LiteLLM maintains
   well; rebuilding it maximizes new code for no differentiation.
5. **Adopting the full LiteLLM management plane** (virtual-key DB, teams, budgets, admin
   UI). Rejected: duplicates auth/tenancy/quota/billing/dashboard the platform already owns
   and integrates better.
6. **A separate first-party proxy in front of LiteLLM for the MVP.** Deferred, not
   rejected outright: G3 proved LiteLLM hooks are sufficient to enforce auth, project-scoped
   BYOK, metadata propagation and accounting for the MVP endpoint set, so a separate proxy
   is unnecessary now. It remains an escape hatch if a future endpoint class cannot enforce
   equivalent policy through hooks.

---

## Remaining Risks

- **G4 — client-disconnect cancellation propagation** (does the runtime abort the upstream
  provider call when the client disconnects?). FastAPI cancellation is proven in `codex-lb`
  [code `proxy.py:746`]; LiteLLM is the same stack, high confidence, unconfirmed here.
  **Non-blocking**: can change cancellation hardening only, not placement or composition.
- **G9 — user-service `last_used_at` write amplification.** Introspection writes on every
  call [code `ApiKeyUsecase.java:106`]; at AI QPS this stresses the `api_key` table. The
  ≤60s cache mitigates but every miss still writes. Decision (aggressive cache vs a
  no-touch introspect variant) is **non-blocking**.
- **G10 — whether `codex-lb` subscription pooling remains a product requirement.** Decides
  full retirement vs keep-as-optional-upstream. Product question, **non-blocking**.
- **Real Gemini credential-redaction confirmation** — one real-provider call to confirm
  header auth + log redaction before enabling Gemini BYOK.
- **LiteLLM upgrade compatibility of the plugin hooks** — the architecture depends on
  empirically verified hook behaviour that a version bump could change (see
  [Upgrade Policy](#upgrade-policy)).

G4, G9 and G10 are classified **non-blocking for ADR approval**.

---

## Upgrade Policy

Because this architecture depends on empirically verified hook behaviour rather than on a
stable public contract:

- LiteLLM is pinned to **1.92.0** initially. Production **MUST NOT** run a floating
  `latest` image.
- Any LiteLLM upgrade **MUST** replay the G3 compatibility suite (hook coverage per
  endpoint, credential precedence, cross-tenant isolation, retry/fallback accounting,
  secret-exposure) before promotion.
- The G3 suite **SHALL** become a repository-owned contract test, not a one-off spike.

---

## Consequences

- New first-party surface is small and well-bounded: a pinned LiteLLM deployment + a
  dedicated ingress + three plugins (`custom_auth`, `async_pre_call_hook`,
  success/failure callbacks) + backend internal endpoints (credential decrypt, quota
  reserve/settle, ledger ingest, all reusing existing subsystems). The provider-translation
  surface is 100% LiteLLM, embedded unchanged.
- The AI failure domain is isolated: LLM load, provider faults, and streaming pressure hit
  the LiteLLM runtime, not the console or the Spring gateway.
- Provider secrets never leave the backend except over an internal channel and never enter
  GitOps git.
- The routing limitation applies: BYOK requests are pinned to one provider; transparent
  cross-provider fallback for BYOK is not delivered in the MVP and requires a future
  platform-managed attempt loop keyed on `provider_attempt_id`.
- Billing correctness depends on the ledger, not on LiteLLM's own spend DB: per-attempt
  failure cost is not emitted by LiteLLM (one failure callback per logical request), so the
  first-party accounting layer owns per-attempt accounting if/when it is needed.
- The Spring gateway, user-service key store, and KServe AI Studio are untouched.
- This decision has a **bounded validity envelope** — see
  [Architectural Escape Criteria](#architectural-escape-criteria-validity-envelope). The
  ledger's `provider_attempt_id`, backend-owned credential references, and revisioned config
  are the guardrails that keep the eventual orchestrator extraction a *demotion of LiteLLM to
  an adapter*, not a rewrite.

---

## MVP Boundary

**In scope:** dedicated HA LiteLLM runtime behind a dedicated ingress; `/chat/completions`
(streaming + non-streaming), `/embeddings`, `/audio/transcriptions`, optional
`/images/generations`; `sk-dada` inbound auth via cached introspection with `ai:*` scopes;
project-scoped BYOK injected per request; reserve → settle → reconcile ledger; provider
registry + model registry CRUD in the console control plane; usage dashboard (read).

Providers at MVP: Anthropic, Groq, OpenRouter, SambaNova (all chat/stream/tools/vision/usage
native); Gemini conditionally, pending the real-provider redaction confirm; AssemblyAI only
as a non-normalized passthrough lane.

**Out of scope / deferred:** transparent cross-provider fallback for BYOK (see
[Routing Limitation](#routing-limitation)); the `/responses` normalized API (bridge
inconsistent across providers); Files API + provider file bindings and async job
orchestration (batch, video generation, video understanding); text-to-speech;
dada-managed shared-key pools with markup (BYOK first = zero key liability); a separate
first-party proxy in front of LiteLLM (escape hatch only); adoption of any LiteLLM
management-plane feature.
