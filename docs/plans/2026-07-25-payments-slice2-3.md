# Payments Slices 2-3 — merchant OAuth connect (project resource) + landing

Owner directive 2026-07-25. Slice 1 (own-shop core, b882aff) live. This doc: slice 2 =
«Подключить оплату» as an app-level resource via YooKassa Partners API OAuth; slice 3 =
landing. Partner cashback enrollment is owner-gated (application process) — not code.

## Grounded facts

YooKassa OAuth [web, official docs]:
- Authorize: `https://yookassa.ru/oauth/v2/authorize?response_type=code&client_id=<app>&state=<state>`.
  NO redirect_uri param — Callback URL is bound at app registration; NO scope param —
  rights chosen at registration.
- Token: `POST https://yookassa.ru/oauth/v2/token`, Basic(client_id:client_secret),
  body `grant_type=authorization_code&code=...` (code TTL 5 min).
  Response: `{access_token, expires_in}` ONLY. No refresh_token — expiry means
  re-authorize; do NOT build a refresh path.
- Use: `Authorization: Bearer <token>` on api.yookassa.ru/v3; ops gated by rights → 403.
- Webhooks for OAuth shops ARE managed via API: `POST /v3/webhooks`
  {event,url} + Idempotence-Key, `GET /v3/webhooks`, `DELETE /v3/webhooks/{id}`.
  Events per OAuth token are separate; fire only for objects created by this app's token.
- Merchant identity after OAuth: `GET /v3/me` (schema unconfirmed) — parse tolerantly
  (`account_id` | `id` | `shop_id`, keep raw JSON), verify on first live call.

Repo reuse [code]:
- OAuth state pattern: git_oauth.go:37/106 — random one-time DB row + TTL 10m,
  consume via DELETE RETURNING; callback public (router.go:180), browser 302 back to UI.
  `randomHex` at gitrepos.go:1066.
- Encrypted external creds pattern: ai_credentials.go:31 (EncryptToken with
  GitopsEncryptionKey, upsert ON CONFLICT).
- Env inject: NO internal helper exists — SetEnvVar's inline upsert (envvars.go:286)
  must be EXTRACTED into a shared internal func (upsertEnvVar) used by both the HTTP
  handler and the payments connect flow. SetEnvVar does NOT trigger re-render —
  env lands on next deploy; UI must say so.
- App public URL: domain_hostnames rows (managed surrogate) per (environment_id,
  app_name); buildDefaultHostname domains.go:1326. Worker/no-HTTP apps may have none.
- Next migration: 052 (049 duplicated by parallel sessions — flagged separately,
  not ours).

## Slice 2 design

### Migration 052_payment_connections.sql

```sql
CREATE TABLE payment_oauth_states (
  state       TEXT PRIMARY KEY,
  project_id  UUID NOT NULL,
  environment_id UUID NOT NULL,
  app_name    TEXT NOT NULL,
  user_sub    TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE payment_connections (
  id               UUID PRIMARY KEY,
  project_id       UUID NOT NULL,
  environment_id   UUID NOT NULL,
  app_name         TEXT NOT NULL,
  account_id       TEXT,
  me_raw           JSONB,
  access_token_enc TEXT NOT NULL,
  expires_at       TIMESTAMPTZ,
  status           TEXT NOT NULL DEFAULT 'active',   -- active|error|disconnected
  webhook_ids      JSONB NOT NULL DEFAULT '[]',
  webhook_note     TEXT,
  connected_by_sub TEXT NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (environment_id, app_name)
);
```

### Backend

Config: `YOOKASSA_PARTNER_CLIENT_ID`, `YOOKASSA_PARTNER_CLIENT_SECRET`. Empty →
connect start returns 409 `payments_not_configured` (slice-1 idiom).

`backend/internal/billing/yookassa/oauth.go` (same package as slice-1 client):
- `AuthorizeURL(clientID, state)`.
- `ExchangeCode(ctx, clientID, clientSecret, code) (accessToken string, expiresIn int64, err)`.
- `Me(ctx, accessToken) (accountID string, raw json.RawMessage, err)` — tolerant field parse.
- `RegisterWebhook(ctx, accessToken, event, url) (id string, err)`,
  `DeleteWebhook(ctx, accessToken, id) error` — Bearer + Idempotence-Key.
  Base URLs injectable for httptest (oauth base + api base separately).

Endpoints:
1. `POST /projects/:id/environments/:envId/apps/:appName/payments/connect` (JWT,
   canWrite): validate app exists; 409 if partner creds unconfigured; insert
   payment_oauth_states (randomHex(24), TTL 10m at consume); return `{authorize_url}`.
2. `GET /api/v1/payments/oauth/callback?code&state` (PUBLIC, root router, git-oauth
   pattern): consume state (DELETE RETURNING + TTL check) → ExchangeCode → Me →
   EncryptToken(access_token) → upsert payment_connections (ON CONFLICT
   (environment_id,app_name) DO UPDATE — reconnect replaces token) →
   env inject via shared upsertEnvVar: `YOOKASSA_OAUTH_TOKEN` (is_secret=true,
   scope runtime), `YOOKASSA_ACCOUNT_ID` (plain, runtime) →
   webhook registration: resolve app host from domain_hostnames (managed first,
   else any); if host found → RegisterWebhook payment.succeeded + payment.canceled
   to `https://<host>/yookassa/webhook`, store ids; if none (worker app) →
   webhook_note='no_public_hostname', continue (not fatal) →
   302 redirect to `/projects/{id}/apps/{app}/settings?tab=payments&connected=1`
   (error paths: 302 with `&payments_error=<code>` — browser flow, no JSON).
3. `GET .../apps/:appName/payments` (JWT, member): `{status, account_id, expires_at,
   webhooks:[{id,event}], webhook_note, env_keys:["YOOKASSA_OAUTH_TOKEN","YOOKASSA_ACCOUNT_ID"],
   connected_at}`. Token NEVER returned.
4. `DELETE .../apps/:appName/payments` (JWT, canWrite): best-effort DeleteWebhook per id
   (decrypt token), delete env_vars rows for the two keys, connection row status →
   deleted (hard DELETE row). Errors from YK logged, not fatal.

Refactor: extract envvars.go inline upsert (encrypt+INSERT ON CONFLICT+audit optional)
into `(h *Handler) upsertEnvVar(ctx, envID, appName, key, value string, secret bool, scope, createdBy string) error`;
SetEnvVar and callback both call it. NO behavior change to SetEnvVar (tests must stay green).

Swag + TestOpenAPICoverage. Tests: oauth client httptest (exchange Basic auth + form,
Me tolerant parse of 3 field shapes, webhook register/delete headers); callback flow
live-pg (state consume one-time — second call 4xx redirect; expired state rejected;
success inserts connection + env_vars rows encrypted + webhook ids stored; reconnect
upserts); connect 409 unconfigured; delete removes env rows.

### Frontend

- Settings tab `payments` in apps/[appName]/settings/page.tsx (Tab union + tabs array +
  block) → new `frontend/components/payments/payments-manager.tsx`:
  - Disconnected: «Подключить оплату ЮKassa» button → POST connect → navigate to
    authorize_url (WebKit sync-nav + anchor fallback, slice-1 idiom); 409 → honest
    «не настроено на платформе».
  - Connected: account_id, статус, вебхуки (или warning no_public_hostname), env-ключи,
    hint «переменные применятся при следующем деплое», snippet block (Python requests +
    Node fetch: create payment via Bearer env token, Idempotence-Key uuid), disconnect
    button with confirm.
  - `?connected=1` / `?payments_error=` toast handling.
- api.ts: paymentsApi {connect, get, disconnect}.
- i18n ru/en.

### Slice 3 — landing

`/accept-payments` ru+en (marketing), landing pattern 3929eb8/railway: hero «Принимай
оплату в приложении в один клик», how-to 4 шага (деплой → Настройки→Оплата → OAuth
ЮKassa → сниппет), честно: нужен свой магазин ЮKassa (ИП/ООО/самозанятый), деньги идут
напрямую юзеру, платформа ключи не видит (OAuth, не secret key), FAQ (безопасность
токена, 54-ФЗ чеки на стороне магазина, что если магазина нет), FaqJsonLd+HowToJsonLd,
sitemap+hreflang, CTA /register?utm_source=accept_payments. Ban-words/superlatives 0,
unicode ASCII. НЕ обещать кэшбек юзеру (партнёрская комиссия = наш доход, owner-gated
заявка).

## Owner-gated additions (owner-actions)

- Register partner app: yookassa.ru/oauth/v2/client — Callback URL
  `https://console.dada-tuda.ru/api/v1/payments/oauth/callback`, rights: создание/чтение
  платежей + вебхуки; put YOOKASSA_PARTNER_CLIENT_ID/SECRET into argo-infra backend secret.
- Партнёрская программа ЮKassa (комиссия с оборотов) — заявка, менеджер.

## M2 gates

- Deploy-M2 (no partner creds): connect → 409 live; callback with bogus state → error
  redirect (no 500); tests green vs fake OAuth/API servers; bundle-proof tab strings;
  landing live 200 + SSR + IndexNow.
- Live-M2 (after owner registers app): real OAuth round-trip on a throwaway app —
  connection row, env vars visible in app, webhook registered on test shop.

## Residuals

- Expiry handling: status shows expires_at; no auto-re-auth (YK has no refresh) —
  reconnect button covers. Optional later: email nudge before expiry.
- Platform-routed webhook relay (event log in console) — later; MVP registers YK
  webhook directly onto the user's app URL.
- Payment events dashboard, slice-3 cashback mechanics after partner enrollment.
