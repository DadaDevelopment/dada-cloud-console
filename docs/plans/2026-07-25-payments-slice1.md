# Payments Slice 1 — YooKassa core + tenant #0 (console billing)

Owner directive 2026-07-25: payments as managed resource, slice order fixed.
Slice 1 = payments core consumed by our own billing (own YooKassa shop, own keys,
no multi-tenant OAuth). Slice 2 (project resource + Partners API OAuth) and
slice 3 (partner cashback + landing) are out of scope here.

## Grounded state [code]

- Seam exists: `backend/internal/billing/payment.go:15` `PaymentProvider` interface,
  only `ManualProvider` (admin upsert of `billing_accounts`, no money). Comment names
  YooKassa as the planned implementation.
- `billing_accounts(org_id TEXT PK, plan, plan_assigned_at)` (mig 027). No payments table.
- Quota gate works but `BILLING_ENABLED=false` by default (config.go:397,537);
  call sites: create app / database / domain (403 `quota_exceeded`).
- Plans source of truth: `config/billing/plans.yaml` (embedded copy `data/plans.yaml`):
  free 0 / startup 990 / business 2900 / enterprise 0=custom. Prices are ALSO hardcoded
  in `frontend/lib/i18n/dict.ts` (marketing) and `frontend/app/(console)/.../billing/page.tsx`
  (`PLAN_PRICES_RUB`) — triplication; slice 1 fixes the console copy (use API), marketing
  dict left as residual.
- Console billing page has no pay action: "upgrade" buttons link out to marketing /pricing.
- Public (non-JWT) endpoint pattern: `router.go` registers callbacks/webhooks outside the
  JWT group; auth inside handler (`webhooks_dadagent.go`, `deploy_hooks.go`). Console
  ingress routes `/api/*` to backend — no ingress change needed for a new webhook path.
- Platform creds pattern: env vars in `config.go` + one k8s Secret via
  `envFrom.secretRef` (`helm/dada-cloud-console/templates/backend-deployment.yaml:42-46`,
  prod values in argo-infra).
- Notify pattern: `backend/internal/notify/notify.go` Compose* + off-hot-path goroutine
  (`audit_notify.go`); customer email available via `auth.Claims.Email`.

## YooKassa facts [web, official docs]

- `POST https://api.yookassa.ru/v3/payments`, Basic auth (shopId:secretKey),
  header `Idempotence-Key: <uuid>` required.
- Minimal one-off RUB: amount{value,currency}, capture:true,
  confirmation{type:redirect,return_url}, description.
- Webhooks: events `payment.succeeded` / `payment.canceled` / `refund.succeeded`;
  NO signature. Authenticity = re-fetch object by id via `GET /v3/payments/{id}`
  before trusting anything (mandatory in our design); optional IP allowlist.
  Must answer HTTP 200, else up to 7 retries / 24h. Registration for Basic-auth
  shops = merchant dashboard (owner action), not API.
- Receipts 54-ФЗ: when shop fiscalization enabled, receipt JSON goes INSIDE the
  payment request (customer.email + items[]). Config-gated in our client.
- Recurring: no native subscription object — save `payment_method.id`, platform
  schedules charges itself. Slice 1 = one-off payments only (manual monthly renew);
  saved-method autorenew = later slice.
- Test shop: created in the merchant dashboard (owner has to own a YooKassa account) —
  live money AND test shop are both owner-gated creds.

## Design

### Data (migration 049)

```sql
CREATE TABLE payments (
  id              UUID PRIMARY KEY,            -- also the Idempotence-Key
  org_id          TEXT NOT NULL,
  plan            TEXT NOT NULL,
  amount_value    NUMERIC(10,2) NOT NULL,
  currency        TEXT NOT NULL DEFAULT 'RUB',
  status          TEXT NOT NULL DEFAULT 'pending',  -- pending|succeeded|canceled
  yk_payment_id   TEXT UNIQUE,
  confirmation_url TEXT,
  customer_email  TEXT,
  created_by_sub  TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  paid_at         TIMESTAMPTZ
);
CREATE INDEX payments_org_idx ON payments(org_id, created_at DESC);
```

### Backend

1. `backend/internal/billing/yookassa/client.go` — thin HTTP client:
   `CreatePayment(ctx, req) (Payment, error)`, `GetPayment(ctx, id)`. Basic auth,
   Idempotence-Key, context timeouts, base URL injectable (httptest).
2. `backend/internal/billing/yookassa/provider.go` — `YooKassaProvider` wraps
   client + pool; implements checkout flow and embeds `ManualProvider.AssignPlan`
   for the success path.
3. Config (`config.go`): `YOOKASSA_SHOP_ID`, `YOOKASSA_SECRET_KEY`,
   `YOOKASSA_RETURN_URL` (default `https://console.dada-tuda.ru/billing/return`),
   `YOOKASSA_SEND_RECEIPT` (default false). Unconfigured → checkout returns
   409 `payments_not_configured` (frontend shows honest message).
4. `POST /api/v1/projects/:id/billing/checkout` (JWT, canWrite): body `{plan}`.
   Server resolves price from loaded plans (NEVER client amount); free/enterprise
   or unknown plan → 400. Insert payments row → YK CreatePayment (receipt block if
   `YOOKASSA_SEND_RECEIPT`, customer email from claims) → store yk_payment_id +
   confirmation_url → return `{confirmation_url, payment_id}`.
5. `POST /api/v1/webhooks/yookassa` (public, outside JWT): parse
   `{event, object.id}`. ALWAYS re-fetch `GetPayment(object.id)` — payload is
   untrusted (no signature). On authoritative `succeeded`: match row by
   yk_payment_id, if status=pending → tx: mark succeeded + paid_at +
   `AssignPlan(org, plan)` → 200; already-processed → 200 (idempotent);
   unknown id → 200 (log, do not leak); YK API error → 500 (YK retries).
   `payment.canceled` → mark canceled. Payment-success email to customer via
   notifier goroutine (Compose pattern), operator copy via audit lane.
6. `GET /api/v1/projects/:id/billing/payments` (JWT, member) — last N payments
   for the billing page (status polling after return).
7. SECURITY FIX in same slice: `PUT /projects/:id/billing/plan` (AssignPlan)
   currently reachable by any org canWrite → self-assign paid plan free.
   Restrict to platform admin (reuse existing admin check from /admin endpoints).
8. Helm: add YOOKASSA_* keys to backend secret template (empty defaults);
   argo-infra values wired when owner supplies keys.
9. Swag regen + TestOpenAPICoverage.

### Frontend

1. `billingApi.checkout(projectId, plan)`, `billingApi.payments(projectId)`.
2. Console billing page: per-plan «Оплатить N ₽/мес» buttons (paid plans above
   current); handler = raw fetch + SYNC navigation to confirmation_url —
   WebKit gesture-loss rule [memory webkit-redirect-gesture-loss]: no await
   before `window.location`, anchor fallback «Открыть страницу оплаты».
   Prices from `GET /billing/plans` (kill local `PLAN_PRICES_RUB`).
3. `/billing/return` page (console): reads latest payment via API, shows
   pending/succeeded/canceled state, polls a few times on pending.
4. i18n ru/en.

### Tests (gates)

- yookassa client: httptest server — auth header, idempotence key, error paths.
- checkout handler: fake provider — price resolved server-side, forbidden plans.
- webhook: httptest YK API — spoofed payload with mismatched authoritative status
  is REJECTED (the no-signature threat, RED-style); idempotent double delivery;
  canceled path; AssignPlan actually flips billing_accounts row (live-pg pattern
  used elsewhere in billing tests).
- Full `go test ./...` + swag + OpenAPI coverage + tsc/eslint/next build.

## Out of scope (residuals, recorded)

- Recurring autocharge (save_payment_method) — after first real payments.
- Refunds API, BILLING_ENABLED flip (owner decision — flip turns quota gate on),
  marketing dict price consolidation, slice 2 OAuth/Partners, slice 3 cashback+landing.

## Owner-gated (goes to owner-actions)

- YooKassa account + shop (ИП/ООО/самозанятый ok per docs): need TEST shop
  shopId+secretKey now (real ones later), fiscalization on/off decision,
  webhook URL registration in dashboard: `https://console.dada-tuda.ru/api/v1/webhooks/yookassa`
  (events payment.succeeded + payment.canceled).

## M2 gates

- This cycle (no keys): full flow vs fake YK (httptest) green + deploy-M2
  (prod flip, checkout returns honest 409 not_configured, webhook 200 on replayed
  sample with re-fetch failing closed).
- With owner test-shop keys: live-M2 = test-card payment end-to-end → billing_accounts
  plan flipped + email received.
- With real keys: first real ruble.
