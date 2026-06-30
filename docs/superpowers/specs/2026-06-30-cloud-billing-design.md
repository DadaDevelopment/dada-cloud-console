# Dada Cloud Billing — Plan-Based Pricing (Slices 1–3)

Date: 2026-06-30
Status: Design — approved shape, pending spec review
Scope: internal cost engine, pricing policy + plan model, operational metering. **No real money movement in this pass** (YooKassa deferred to a later slice; PaymentProvider abstraction stubbed here).

## 1. Product direction

Customers buy **plans**, not resources. The platform feels like Vercel/Railway pricing, not AWS.

- External pricing = plan + quotas + capabilities. Flat price per plan.
- CPU/RAM/vCPU/GB-hour and other cloud-provider metering concepts are **internal implementation details** and MUST NOT appear in any customer-facing surface in the MVP.
- Prometheus usage is **operational/informational only** — never a customer billing input in slices 1–3.

Aligns with the product vision north star (`docs/product/product-gtm-vision.md`): predictable, user-controllable value metric; price estimate and clear plan before deploy.

### Billing model

```
internal_cost  = cluster_cost / full_capacity        (internal unit cost)
plan_cost      = costengine(plan internal footprint)  (sum of internal resources a plan reserves)
plan_price     = plan_cost × markup                   (price FLOOR; published price rounded up to a clean number, never below floor)
```

Customer is charged for the **selected plan**, not actual consumption.

## 2. Two separated worlds

```
INTERNAL (never shown to user)
  config/billing/cluster-cost.yaml ─► costengine ─► internal unit cost
  plan internal footprint ───────────┘─► plan_cost ─► ×markup ─► plan_price floor   (pricing + margin analysis)
  Prometheus actual usage (org_id) ─► profitability / oversell / anomaly analytics

EXTERNAL (the product)
  config/billing/plans.yaml: {quotas, capabilities, price}
        ─► landing pricing page
        ─► estimator (= plan recommender)
        ─► console usage dashboard + invoice preview + upgrade CTA
        ─► quota enforcement (hard gate on countable resources)
```

## 3. Billing tenant

Tenant = **Org** (`project.OrgID`, IAM org per ADR-009). Today prod is single-org "dada"; per-org billing is correct and is designed + tested multi-org, biting fully once personal orgs land (issue #10). One `billing_accounts` row per org; default plan = Free.

## 4. Components & boundaries

| Package | Kind | In → Out | Customer-facing? |
|---|---|---|---|
| `internal/billing/costengine` | pure | cluster-cost config → internal unit cost; plan footprint → plan_cost | No |
| `internal/billing/pricing` | pure | plans config + unit cost × markup → plan price floor, quota model, plan recommender | Price floor is internal; quotas/capabilities/published price are external |
| `internal/billing/metering` | IO | Prometheus + DB object counts (per org_id) → usage_records | Aggregated %s only |
| `internal/billing/account` | DB | usage_records + plan → quota status, invoice preview, soft alerts, plan assignment | Yes |
| `internal/billing` API handlers | HTTP | read models + plan recommend + limits | Yes |
| `PaymentProvider` interface | boundary | assign/charge plan — **manual/admin stub now**, YooKassa later | Future |

Design rule: every package must be understandable and testable in isolation. `costengine` and `pricing` are pure (no DB, no clock) → table-tested. `metering` is the only Prometheus consumer. `account` is the only DB writer for ledgers.

## 5. Slice 1 — cost engine + internal unit economics

- `config/billing/cluster-cost.yaml` (git, swappable without code):
  - cluster node flavors, counts, ₽/mo each → `cluster_cost`
  - total capacity (internal units: compute, memory, storage GB)
  - source: Beget k8s API `/v1/k8s` node list or invoice; hardcoded now, auto-sync optional later
- Derived: `internal_cost = cluster_cost / full_capacity` per internal unit. (Idle headroom absorbed by markup — accepted business choice.)
- Plan internal footprint: each plan declares an estimated internal resource reservation. `plan_cost = costengine(footprint)`. `plan_price_floor = plan_cost × markup` (markup default ~2.7×, single tunable profit lever).
- `costengine` pkg: pure functions, table tests. **Fail closed**: missing/invalid cost config → plan pricing flagged invalid + alert, never silently $0.
- Output consumed by slice 2 (validate published prices ≥ floor) and internal margin analytics. **No customer-facing output.**

## 6. Slice 2 — pricing policy + plan model + landing

### Plan schema (`config/billing/plans.yaml`) — central entity

```
plan:
  key
  name
  price            # published ₽/mo (0 for Free, "custom" for Enterprise); must be ≥ plan_price_floor
  quotas:
    apps
    databases
    storage_gb
    domains
    environments
    team_members
    backup_retention_days
  capabilities: []  # e.g. priority_support, sso, audit_log
  support_level     # community | email | priority
  internal_footprint: {...}   # internal resource estimate, drives plan_cost
```

### Plans (MVP)

| Plan | Apps | DBs | Storage | Domains | Envs | Members | Backups | Support | Price |
|---|---|---|---|---|---|---|---|---|---|
| Free | 1 | 1 | small | 1 | 1 | 1 | none | community | 0 ₽ |
| Startup | several | several | medium | several | 2 | small team | short retention | email | от N ₽/mo |
| Business | many | many | large | many | several | larger team | longer retention | priority | от M ₽/mo |
| Enterprise | custom | custom | custom | custom | custom | custom | custom | priority + SLA | custom / contact |

(Exact quota numbers and N/M set during implementation from cost-engine floor + ICP sizing; solo/startup/agency segments per north star.)

### Landing + estimator

- Rewrite `frontend/app/(marketing)/pricing/page.tsx` + `frontend/lib/i18n/dict.ts` (ru/en): Vercel/Railway-style plan cards with quota+capability matrix. Remove "цены ориентировочные".
- **Estimator = plan recommender** (NOT a resource calculator): user answers "how many apps / DBs / domains / team members?" → returns best-fit plan. Endpoint `POST /api/v1/billing/recommend-plan`. Zero raw-resource (CPU/RAM/GB-hour) language anywhere.
- Docs page: "How plans & quotas work" explainer (transparency requirement).

## 7. Slice 3 — metering (operational/informational only)

### Migration 027
`billing_accounts(org_id PK, plan, plan_assigned_at, alert_state, ...)`, `usage_records(org_id, resource, qty, period_start, period_end)` idempotent on `(org_id, resource, period_start)`, `plan_invoices(org_id, period, plan, amount, status=preview)`.

### Metering job
Hourly. Sources per `org_id`:
- countable resources (apps, DBs, domains, members): counted from platform DB
- storage / runtime usage: Prometheus `QueryRange` (existing client, authoritative `org_id` label per ADR-012)

Writes `usage_records`. Prometheus gaps → skip interval, never double-count (idempotent key). Purposes: usage-vs-quota %, upgrade recommendation, invoice preview, internal profitability (actual usage vs plan footprint → oversell/anomaly).

### Quota enforcement
- **Countable quotas (apps, databases, domains, team_members) = HARD gate at creation.** Create handlers check `count < plan.quota`; over → block with "upgrade to add more". Wired into existing create endpoints in `backend/internal/api` (apps, databases, domains, members).
- **Storage / usage quotas = SOFT alert** (banner/email at ~80%), no blocking.
- **No hard spending caps.** Soft alerts only.

### Console
- **Usage dashboard**: X/Y apps, X/Y DBs, X/Y GB storage, quota bars, current plan card.
- **Invoice preview**: upcoming plan fee (flat, not metered).
- **Upgrade CTA** when near/over quota or outgrowing Free.

### Payments boundary
`PaymentProvider` interface; MVP impl = manual/admin plan assignment (no money movement). YooKassa (cards + SBP) + 54-ФЗ receipts implement this interface in a later slice.

## 8. API surface

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/billing/account` | plan, quota status, MTD usage %, invoice preview |
| GET | `/api/v1/billing/usage` | per-resource usage vs quota |
| GET | `/api/v1/billing/invoices` | preview invoices |
| POST | `/api/v1/billing/recommend-plan` | estimator: answers → best-fit plan |
| PUT | `/api/v1/billing/plan` | assign/upgrade plan (admin/manual now) |

OIDC scope note: write endpoints require appropriate scope (see console OIDC scopes contract); read endpoints gated by org membership.

## 9. Error handling

- Missing cost config → plan pricing invalid + alert, never free.
- Prometheus gap → usage interval skipped, idempotent, no double-count.
- Quota gate failure path → clear upgrade message, never silent.
- Plan published below floor → build/config validation error (margin guard).

## 10. Testing

- `costengine` + plan_price floor: pure table tests (incl. fail-closed on bad config).
- `pricing` plan recommender: boundary tests (just-fits / just-over each plan).
- `metering`: fake Prometheus + DB counts, gap + idempotency.
- quota gate: hard-block at limit for each countable resource; soft-alert for storage.
- estimator parity: backend recommendation == landing widget result.

## 11. Deliverables → original goals

| Goal | Covered by |
|---|---|
| Прозрачность для юзера | real plan cards + plan recommender + docs explainer |
| Расчёт от железа и реального потребления (overhead не в минус) | internal costengine(cluster spend) sets plan_price floor; markup keeps margin positive; metering watches actual usage for oversell |
| Работающая система платежей | balance/plan model + PaymentProvider boundary now; YooKassa = later slice |
| Удобный UX/UI | Vercel/Railway-style plan cards + console usage dashboard + estimator |

## 12. Explicitly out of scope (this pass)

- Real payment gateway / money movement (YooKassa, 54-ФЗ чеки) — later slice.
- Hard spending caps.
- Customer-facing CPU/RAM/GB-hour metering or metered overage billing.
- Per-resource usage-based invoices.
