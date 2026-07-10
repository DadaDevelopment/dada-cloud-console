# Managed Postgres databases

> **Doc-vs-code note:** confirmed against the actual UI — the inventory's claim that there is
> **no restore and no delete from the UI** is accurate. Both are genuinely missing today.
> Restore is support-only; there is no delete action anywhere for a database resource.

## What it's for

A hands-off Postgres instance for your app: create it, optionally point it at an application,
and a connection string is injected automatically — no server to patch, no dumps to babysit
(backups are automatic once enabled).

## How to create a database

1. Go to **Databases** → **Create Database**.
2. Fill in:
   - **Database Name** — the Kubernetes resource name (lowercase, digits, hyphens).
   - **PostgreSQL DB Name** — the actual database name inside Postgres.
   - **App Reference** (optional) — bind credentials to a specific app, or leave empty for an
     environment-level database shared across apps.
   - **Backups** toggle (on by default) with **Schedule** (hourly/daily) and **Retention**
     (7/14/30 days).
3. Submit — you're redirected to **Operations** to watch provisioning finish.
4. Once ready, the database card shows its size and last-sync time. Click it for details.

## Connecting your app

Credentials are delivered as a Kubernetes Secret in the app's namespace — they are **never
displayed in the console**. Reference them as environment variables in your app (e.g. inject
`DATABASE_URL` via the app's env var settings, scoped `runtime`), rather than looking for a
"copy connection string" button — it doesn't exist.

## Gotchas

- **No restore from the UI.** Backups run on the schedule you set, but if you need to restore
  one, you currently have to go through support — there is no self-service restore button.
  Don't treat the backup toggle as a full disaster-recovery guarantee yet.
- **No delete.** Once created, a database can't be removed from the console. Plan names
  carefully — if you need it gone, contact support.
- **Postgres only.** Managed MySQL and Redis are not available as this kind of resource. They
  exist only as `ServiceDatabase` on VM-based App Servers (Docker Compose) — a different
  mechanism, see [App Servers](app-servers-bring-your-own-vm.md).
- Leaving **App Reference** empty creates an environment-level database any app in that
  environment can be wired to; setting it scopes the database (and its generated Secret) to
  one app.

## Not yet supported

- Restore from a backup (support-only for now).
- Delete.
- Connection/query metrics in the console.
- MySQL/Redis as a managed resource on Cloud/K8s environments (only via VM `ServiceDatabase`).
