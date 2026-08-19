# Dada Cloud → FinCore

Dada Cloud pushes its own economics into FinCore, the analytical CRM this team
also builds. Users become clients, money that arrived becomes an incoming fact,
the Beget hosting bill becomes an expense. Nothing about this lives in FinCore's
product code: the whole integration is `backend/internal/fincore` plus the
customer's own machine ingest seam.

## What is pushed

| Dada Cloud | FinCore | key |
| --- | --- | --- |
| console user | client | `external_id` = `users.id`, source system `dada_cloud` |
| succeeded YooKassa payment | CREDIT transaction | `source_identity` = `payment:<uuid>` |
| Beget monthly bill | DEBIT transaction | `source_identity` = `beget:<YYYY-MM>` |

FinCore namespaces those keys with the source system, so a row lands as
`dada_cloud:payment:<uuid>`. Re-running the backfill converges on the same rows
instead of booking the money a second time.

## Contract

Two endpoints, both authenticated by a FinCore **service token** (`fcs_...`):

- `POST /api/ingest/clients/upsert` — scope `clients:write`
- `POST /api/ingest/transactions` — scope `ingest:write`

A personal JWT is refused with 401: the ingest router gates on `require_scopes`,
not on a human's role. `internal/fincore.Client.LooksLikeServiceToken` checks
the prefix at startup so the mistake is named once rather than every hour.

Both endpoints are `extra="forbid"` and cap a batch at 500 items; the client
splits larger batches itself. A CREDIT must carry `payer_name` and a DEBIT must
carry `payee_name` — the DTO rejects the whole batch otherwise.

The tenant is chosen by the `x-tenant-slug` header. It is sent explicitly on
every request: without it FinCore falls back to the caller's active membership,
which is a silent write into whichever tenant the token happened to sit in.

## Judgement calls the code makes

These are decisions, not laws — each one is visible in `internal/fincore/sync.go`
and reversible.

**Not every signup is a client.** Registration has been open through Yandex ID
since 2026-08-13 and a bot wave already landed in `users`; a straight dump would
file farm accounts as counterparties. The filter is "owns a project, is a member
of one, or has ever paid".

**Ownership is read from `projects.owner_id`.** `project_members` holds 4 rows
in production against 65 projects and 24 distinct owners, so filtering on
membership alone would find 3 people and drop the rest.

**Our own accounts are not clients.** `@dada.local`, `@keycloak.local`,
`@dada-tuda.ru` and `service-account-*` are the platform and its staff.

**The platform's own orgs are not revenue.** The only succeeded payment in
production belongs to org `dada` — the company paying itself, 990 RUB. Booking
it as income would print revenue no customer ever sent. Set
`FINCORE_INCLUDE_INTERNAL_ORGS=true` (or pass `-include-internal-orgs`) to
include it.

**Payments join through `org_id → username`, not `created_by_sub`.** That column
is `NOT NULL` but empty on every production row, so joining on it links nothing.
A personal org's id is the username (`internal/auth/jwt.go`).

**Only the current month's hosting bill is emitted.** Beget's API reports the
present price of the clusters, not a history of invoices. Stamping today's price
onto past months would invent expenses that were never charged; those live in
the invoice PDFs and belong to FinCore's own PDF pipeline.

**`app_usage.cost_rub` and `box_usage.cost_rub` are never pushed.** They are an
allocation of the same Beget bill across tenants, not cash that left the
account. Pushing both would double-count the hosting spend.

## Running it

Backfill, printing everything and writing nothing:

```bash
go run ./cmd/fincore-sync -dry-run -payloads
```

Real push (idempotent, safe to repeat):

```bash
go run ./cmd/fincore-sync -dry-run=false
```

The ongoing hourly pass runs inside the server and starts only when the
integration is configured (`internal/api/fincore_sync.go`).

## Configuration

| env | meaning |
| --- | --- |
| `FINCORE_BASE_URL` | `https://profi-backend.dada-tuda.ru` |
| `FINCORE_TOKEN` | service token, `fcs_...` |
| `FINCORE_TENANT_SLUG` | `dada_development` |
| `FINCORE_PROJECT_ID` | optional: pin every fact to one FinCore project |
| `FINCORE_INCLUDE_INTERNAL_ORGS` | optional: count the platform's own orgs as revenue |

Any of the first three empty switches the whole integration off:
`fincore.New` returns nil and the syncer never starts.

## SDK

FinCore ships a generated Go SDK at `python.profi-ru/sdk/go` (module
`github.com/dada-tuda/fincore-sdk-go`). It is not published yet — the repository
does not exist on GitHub, so importing it here would break every build that
cannot reach a local checkout. `internal/fincore/client.go` is a thin
hand-written stand-in over the same two endpoints; swapping it for the SDK is a
one-file change once the module is fetchable.
