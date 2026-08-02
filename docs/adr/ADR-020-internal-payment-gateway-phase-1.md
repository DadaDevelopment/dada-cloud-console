# ADR-020: Internal Payment Gateway, Phase 1

## Status

Accepted — 2026-08-02. Implemented in `35d3b27`.

Scope is deliberately narrow: **our own services only**. Owner decision, verbatim:
«гейтвей только для наших сервисов».

Related: [ADR-015: AI Gateway](ADR-015-ai-gateway-runtime-data-plane.md),
[product-gtm-vision](../product/product-gtm-vision.md)

---

## Context

Every internal service that wants to take money today has to become a YooKassa
integration of its own: its own shop id, its own secret key, its own
`CreatePayment` call, its own webhook receiver, its own test-to-live credential
swap. The console already carries all of that for plan checkout — client
(`backend/internal/billing/yookassa/client.go`), provider
(`.../provider.go`), a shop-wide webhook receiver
(`backend/internal/api/billing_payments.go:143`), and the credentials
(`YOOKASSA_SHOP_ID` / `YOOKASSA_SECRET_KEY`).

The concrete trigger is a Telegram VPN bot. Grounding found it currently charges
via Telegram Stars and has **no YooKassa code at all** — so there is nothing to
migrate; what there is, is a place where the second copy of the integration was
about to be born. It also sets the hard constraints: it runs under systemd on a
bare VPS outside the cluster, and it has **no inbound HTTP server**.

The production shop is still on a **test** key. The stated operational goal is
that going live is one secret swap in the cloud, not a tour of every service.

### What the platform already decided, and is not being reopened

The 2026-07-25 owner directive — «платформа не агрегатор, лицензия не нужна» —
governs **customer** applications. Customer apps connect their *own* YooKassa
merchant account via OAuth and receive credentials by env-inject
(`backend/internal/api/payments_connect.go`); money never crosses the platform's
books. That is phase 2 and this ADR does **not** change it. Aggregating customer
funds would require a payment-agent licence the platform deliberately does not
hold.

Phase 1 is a different animal: it moves *our own* money through *our own* shop.
No licensing question arises, because there is no third party.

## Decision

Add a thin internal seam — `POST /api/v1/pay/charges`, `GET
/api/v1/pay/charges/:id`, `GET /api/v1/pay/charges` — that lets a
key-authenticated internal service create a YooKassa payment and read its
status, without ever holding the shop credentials.

### Credentials: a new revocable per-service key, not an existing one

Prefix `sk-dada-pay-`, minted by a platform admin, stored as a plain SHA-256
hash, revocable by setting `revoked_at`, resolved **inside** the handler rather
than by router middleware.

Three existing credential families were considered and rejected:

- **`ai_gateway_keys` (migration 058)** — project-scoped, self-service, with
  scopes hardcoded to `"ai:chat ai:embeddings"`. Wrong tenancy grain for money:
  a project member could mint a key that charges the platform's shop.
- **The shared `InternalAuthToken`** — one static token, unrevocable
  individually, and its blast radius covers internal endpoints generally. Wrong
  shape for a credential that lives on an off-cluster VPS.
- **Console JWT** — the caller is a bot, not a session.

The chosen idiom is the deploy hook (`app_deploy_hooks`, migration 039): public
route, per-resource bearer, resolved in the handler. It already carries an
off-platform caller in production.

Auth failure taxonomy is copied from `backend/internal/gateway/server.go:150-178`
and is load-bearing:

- missing or unparseable key → **401** `invalid api key`
- unknown or revoked key → **401** `invalid api key` (same message; a probing
  caller learns nothing)
- Postgres failure while resolving the key → **503** `auth backend unavailable`

The 503 matters: an auth-path database outage must not present as a flood of
401s, which reads as "the bot's key is wrong" and sends whoever is on call to
rotate a credential that was never broken.

### Storage: new tables, not `payments`

`payments` (migration 050) is plan-shaped. `plan TEXT NOT NULL` feeds
`assignPlanTx` (`provider.go:199`), and every succeeded row is cross-checked by
`SweepPaymentPlanMismatch` (`billing_mismatch.go:41-49`). A VPN subscription
booked into that table would grant some org a plan it never bought and then page
someone about the discrepancy it just created.

Migration 083 adds `pay_service_keys` and `service_charges`.

### Idempotency: the caller's own ref, enforced by the database

`UNIQUE (service_key_id, external_ref)`. The caller supplies a ref it can
regenerate deterministically (`tg:12345:plan-30d`); a retried create returns the
**same** charge and its existing `confirmation_url` rather than a second
payment. This is not a nicety — a bot on a flaky VPS link retries, and double
billing a user is the failure that costs trust.

One crash window needs explicit handling. Between the `INSERT` and the YooKassa
call the row exists, so the UNIQUE guard will match it forever, but nothing is
behind it. Left alone, the caller would receive that dead row — pending, no
confirmation URL — on every subsequent retry, permanently unable to pay. So the
replay path *heals* it by re-running the create with the charge UUID as the
`Idempotence-Key`, which YooKassa deduplicates on its side.

### Delivery: polling, by construction

The caller has no inbound HTTP endpoint, so there is nowhere to deliver a
callback. `GET /api/v1/pay/charges/:id` reconciles a pending charge against
YooKassa on every read and flips the row if it has settled.

The shop-wide webhook is still wired in: when `ProcessWebhook` reports
`OutcomeUnknownPayment`, the id is offered to the service-charge processor
before being logged as unknown. Both paths take a `FOR UPDATE` row lock and
re-check the status after acquiring it, so a concurrent webhook and a concurrent
poll cannot both apply the same transition.

Webhook payloads are **never** trusted for content. YooKassa webhooks are
unsigned, so both paths re-fetch authoritatively via `GetPayment` and treat the
payload as nothing more than a hint that something changed.

### Amounts

The amount is caller-supplied, which would be unacceptable for plans (those stay
server-priced) and is acceptable here only because the caller is our own service
behind a revocable key. A per-charge ceiling of 100000.00 bounds the damage from
a leaked key or a unit-confusion bug.

## Consequences

- Test-to-live becomes a single secret swap on the console deployment. Nothing
  ships to the VPN bot, because the bot never had a credential.
- Every internal charge is visible in one table, auditable via
  `ServiceChargeCreated`, and attributable to a named service.
- A leaked service key can create charges against our shop up to the ceiling.
  Mitigation is revocation plus the `last_used_at` trail; there is no per-key
  rate limit yet.
- Polling costs one YooKassa `GetPayment` per read of a pending charge. At
  current volume this is negligible; if a caller ever polls hot, the reconcile
  needs a floor.
- No callback delivery. A service that *does* have an inbound endpoint will want
  one, and that means a durable worker (the `operations` +
  `FOR UPDATE SKIP LOCKED` pattern already in `box_operations_worker.go`), not
  a fire-and-forget HTTP call. Deferred until a caller actually needs it.
- Refunds are not implemented. The first caller does not need them; adding them
  later means a `service_refunds` table, not a change to this seam.

## Remaining Risks

- **Production still runs a test YooKassa key.** Nothing in this change has been
  exercised against live credentials; the swap itself is the first real test.
- **Cross-service replay of `external_ref`.** Uniqueness is scoped per key, so
  two services can legitimately reuse the same ref string. That is intended, but
  it means the ref alone is not a global identifier in logs.
