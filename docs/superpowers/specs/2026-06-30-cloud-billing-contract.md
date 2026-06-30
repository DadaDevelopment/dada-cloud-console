# Billing Implementation Contract — single source of truth

Companion to `2026-06-30-cloud-billing-design.md`. Backend `plans.yaml` and frontend plan cards MUST match this file. Numbers are launch defaults; cluster-cost is calibrated later from real Beget invoice (config-swappable, no code change).

## Plans (quotas + capabilities)

| key | name | apps | databases | storage_gb | domains | environments | team_members | backup_retention_days | support_level | price (₽/mo) |
|---|---|---|---|---|---|---|---|---|---|---|
| free | Free | 1 | 1 | 1 | 1 | 1 | 1 | 0 | community | 0 |
| startup | Startup | 5 | 2 | 10 | 5 | 2 | 3 | 7 | email | 990 |
| business | Business | 20 | 10 | 100 | 20 | 5 | 10 | 30 | priority | 2900 |
| enterprise | Enterprise | custom | custom | custom | custom | custom | custom | custom | priority+SLA | custom (contact) |

EN names identical; price shown as "$0 / from $12 / from $35 / Custom" (approx, marketing only).

## Markup
`markup_default = 2.7` (single tunable profit lever). `plan_price_floor = plan_cost × markup`. Published `price` MUST be ≥ floor; a test asserts this for every plan.

## Internal cost (NEVER customer-facing)

`config/billing/cluster-cost.yaml` launch placeholder (calibrate later):
```yaml
nodes:
  - flavor: 8vcpu-16gb
    count: 3
    monthly_cost_rub: 4000
capacity:
  vcpu: 24
  ram_gb: 48
  storage_gb: 300
egress_rub_per_gb: 0       # included for MVP
```
`internal_cost_per_vcpu_mo = cluster_cost / capacity.vcpu` etc. (divide by FULL capacity; idle headroom absorbed by markup).

Plan internal footprint (estimated reservation, drives plan_cost), in `plans.yaml` per plan:
```
free:     { vcpu: 0.25, ram_gb: 0.5,  storage_gb: 1 }
startup:  { vcpu: 1.0,  ram_gb: 2.0,  storage_gb: 10 }
business: { vcpu: 4.0,  ram_gb: 8.0,  storage_gb: 100 }
```
`plan_cost = vcpu*cost_vcpu + ram_gb*cost_ram + storage_gb*cost_storage`.

## API (all under `/api/v1`, behind authMW; project-scoped paths resolve org via h.projectOrg)

| Method | Path | Body / Resp |
|---|---|---|
| GET | `/billing/plans` | → `{ plans: Plan[] }` (public-ish; for landing + console) |
| GET | `/projects/:projectId/billing/account` | → `{ plan, quotas, usage, invoicePreview }` |
| GET | `/projects/:projectId/billing/usage` | → `{ usage: { apps:{used,limit}, databases:{used,limit}, storage_gb:{used,limit}, domains:{used,limit}, environments:{used,limit}, members:{used,limit} } }` |
| POST | `/billing/recommend-plan` | body `{ apps, databases, domains, members, storage_gb }` → `{ recommended: planKey, reason }` |
| PUT | `/projects/:projectId/billing/plan` | body `{ plan: planKey }` → assign/upgrade (admin/manual; requires write role) |

`invoicePreview = { period: "YYYY-MM", amount: <plan price>, currency: "RUB", status: "preview" }` — flat plan fee, NOT metered.

## Quota enforcement

`checkQuota(ctx, orgID, resource) error` — counts current resource for org, compares to plan limit.
- HARD gate (block create, return 403 with `{ error: "quota_exceeded", resource, limit, upgrade: true }` and message "Upgrade your plan to add more <resource>") on: **apps, databases, domains, team_members**.
- Wire into create handlers: `CreateApp` (apps), `CreateServiceDatabase` (databases), `AddDomainAuthorization` (domains), member-invite handler (members — locate it).
- SOFT only (no block) for **storage_gb** usage — surfaced as alert in account/usage, never gates.
- No hard spending caps anywhere.

## Tenant / plan storage

`billing_accounts(org_id PK, plan text default 'free', plan_assigned_at, created_at, updated_at)`. Org with no row ⇒ treated as `free`. Default plan = free.

## Out of scope this pass
Real money (YooKassa), 54-ФЗ receipts, hard spending caps, customer-facing CPU/RAM/GB-hour metering. `PaymentProvider` interface present but only manual/admin plan assignment implemented.
