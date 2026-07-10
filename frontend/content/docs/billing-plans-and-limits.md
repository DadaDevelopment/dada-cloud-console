# Billing, plans, and limits

## What it's for

See what plan your project is on, how close you are to its limits, and roughly what your
resources are costing — all read-only from inside the console.

## How to check your usage

1. Go to **Billing** (visible to Owner/Admin only).
2. **Current plan** card shows your plan name (Free / Startup / Business / Enterprise) and
   monthly price in ₽ — Enterprise shows no price at all (it's negotiated, not listed).
3. **Quota usage** bars show Applications, Databases, Storage, Domains, Environments, and
   Members — each as `used / limit`, with unlimited quotas shown as `∞`.
4. If any quota is at 80% or higher, a **Near limit** banner appears at the top calling out
   which resource and its percentage.
5. Scroll down to **Resource consumption** for a cost breakdown grouped by Applications /
   Databases / Storage, each showing compute (vCPU/RAM) or storage (GB) and a subtotal —
   labeled explicitly as an **estimate at current rates, not a bill**.
6. If a **Next invoice** preview is available, it shows the period, amount, and status.

## How to upgrade

Click **View plans**. This opens `https://dada-tuda.ru/pricing` in a new destination — it is
**not** an in-app checkout. There is no plan-switch button, no payment form, and no downgrade
button inside the console itself.

## Gotchas

- **There is no in-app way to change plans.** "View plans" is an outbound link to the public
  pricing page; upgrading/downgrading happens outside the console (talk to your account
  contact or use whatever process is linked there).
- **The consumption breakdown is an estimate, not your actual invoice** — the UI says so
  explicitly (`estimated at our rates, not a bill`). Don't reconcile it against a real invoice
  expecting an exact match.
- Enterprise plans show **no price** in the Current Plan card at all, not even "Contact us" —
  don't read the blank space as a bug.
- This page is hidden entirely for Developer/Read Only roles, not just visually de-emphasized
  — non-admins won't see **Billing** in the nav and get no way to check quotas themselves.

## Not yet supported

- Upgrading, downgrading, or paying for a plan from inside the console.
- Historical invoice list (only a preview of the *next* one, when available).
- Per-resource cost alerts beyond the single 80%-of-quota banner.
