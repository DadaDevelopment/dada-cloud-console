# Dada Cloud → FinCore

Dada Cloud pushes its own economics into FinCore, the analytical CRM this team
also builds. Users become clients and money that arrived becomes an incoming
fact. Nothing about this lives in FinCore's product code: the whole integration is `backend/internal/fincore` plus the
customer's own machine ingest seam.

## What is pushed

| Dada Cloud | FinCore | key |
| --- | --- | --- |
| console user | client | `external_id` = `users.id`, source system `dada_cloud` |
| succeeded card payment | CREDIT transaction | `source_identity` = `payment:<uuid>` |

The Beget hosting bill used to be pushed as a DEBIT and no longer is -- see
"The bank is the source of money" below.

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

## The bank is the source of money

The company's T-Bank account is already streamed into this same FinCore tenant
by the findata T-Bank integration: 76 statements from 2025-10-29 onward, the
real Beget outflow among them (5000 RUB on 2026-08-19 and on 2026-07-20, ООО
"БЕГЕТ", INN 7801451618). Anything this console mints that describes the same
money is a second booking of it.

That is not hypothetical. The hourly push wrote statement id 3,
`dada_cloud:beget:2026-08`, 13194 RUB dated 2026-08-01, and FinCore classified
it into financial fact id 3, `outgoing_payment`, `is_current=true`. August
DEBIT then read 149193 RUB across 8 rows with Beget appearing as 18194 RUB
instead of the 5000 RUB that actually left the account. The figure was not even
wrong in the same direction: 13194 RUB is modelled monthly consumption from the
Beget API, 5000 RUB is what was paid.

So the console no longer ingests the hosting bill. `Syncer.collectHostingCost`
still measures it -- it lands in the report and in the `fincore sync done` log
line as `hosting_cost_rub` -- but it is management accounting, not a bank fact,
and FinCore has no seam for that yet. When one exists, per-project allocation is
the thing worth sending: the bank knows 5000 RUB left, only the console knows
which project ate it.

Statement id 3 and fact id 3 are still in the customer's production tenant and
have to be removed by hand -- this integration has no delete path and must not
grow one.

## Attribution

Money has to end up on a client card, and there are two routes into the
company, so there are two mechanisms.

**Card payments.** YooKassa collects them and settles to the account as one
aggregated payout, so no bank line ever names the customer. The console is the
only witness of who paid what, and it pushes each payment with
`client_external_id` = the console user id. That is the whole attribution: the
fact lands on the client the ingest seam resolves by external key.

**Bank transfers.** A customer who asks for an invoice pays from their own
account, and that payment reaches FinCore on its own through the findata T-Bank
feed. The console does **not** push those -- `CloudPayment.SettledInBank` drops
every row whose `payment_method` is `invoice`, and the count shows up as
`payments_settled_in_bank` in the report. Pushing them would repeat the hosting
bill mistake exactly.

What the console contributes there is identity. `payments` captures
`payer_inn`, `payer_kpp`, `payer_org_name` and `payer_legal_address` when the
invoice is issued, and the client sync carries the latest of them onto the
client card as `iin` and `requisites`, with the legal entity taking over
`short_name`. FinCore binds an incoming transfer to the client whose card
matches the payer -- client 1 in this tenant carries `iin` 7840394339 and all
seven transfers from that INN are classified `credit_with_domain_match` with
`client_id: 1`. A cloud client with no INN can never be credited with a
transfer, whatever else the console knows about them.

**Where this stands today.** Live dry-run on 2026-08-20: 21 clients, **0 with
an INN**, 1 transaction. No customer has ever paid by invoice -- the only two
rows carrying requisites are our own sandbox test and Dada Development itself,
and both are `canceled`/`pending`. So the transfer half of attribution is wired
and provably idle; it starts producing the moment a real invoice is paid.

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

## Timestamps

`operation_date` is sent as a naive local timestamp (`internal/fincore.WallTime`),
not RFC3339. FinCore stores it in `sber_statements.operation_date`, a
`TIMESTAMP WITHOUT TIME ZONE`, and hands the parsed value straight to asyncpg,
so an offset-bearing value is refused by the driver and the whole batch returns
`http 503: {"detail":"database_unavailable"}` -- a transport error that names
the database while the real cause is one rejected parameter.

## What the first production backfill did

Run on 2026-08-19 against `dada_development`, confirmed in the
`profi-backend-deploy` pod's own log:

- `Client sync ... received=21 created=21 updated=0`
- `Ingest ... received=1 created=1 updated=0 unchanged=0`
- a replay came back `clients_updated=21`, `transactions_unchanged=1`

The single transaction is `beget:2026-08`, DEBIT, **13194.00 RUB**
(`cluster_d5c373` 12226.00 + `cluster_e7b608` 968.00) read live from the Beget
API. No revenue was pushed: the only succeeded payment in production is the
platform paying itself.

FinCore stamps `company_id` from its own global `db_schema` setting rather than
from the request's tenant, so these rows carry `company_id='profi'`. Tenant
isolation still holds -- a service token that asks for a tenant it is not bound
to resolves to nothing -- but the column does not name the tenant.

## SDK

FinCore's generated Go client is **vendored**, not imported: the module
`github.com/dada-tuda/fincore-sdk-go` is not published, so a plain import would
break every build that cannot reach a local checkout. The generated code lives
in `backend/internal/fincore/client/client.gen.go` (oapi-codegen v2.8.0 from
FinCore's `openapi.json`, contract 1.1.0) and is regenerated wholesale --
`client/fincore.go` next to it is the only hand-written file there and holds the
constructor that demands both `Authorization: Bearer` and `x-tenant-slug`.

`internal/fincore/client.go` builds on it: base URL, paths, headers and typed
response decoding are the SDK's.

**The transaction batch is encoded by hand on purpose.** The generated
`IngestTransactionIn` declares `operation_date` as `time.Time`, which marshals
with a zone offset; the column behind it is `TIMESTAMP WITHOUT TIME ZONE`, so
asyncpg refuses the value and the whole batch returns
`http 503: database_unavailable` (see Timestamps above). Until the contract
declares a naive type, the batch goes out through
`IngestTransactionsWithBodyWithResponse` with our own `WallTime` marshalling.
Everything else on the call is the SDK's.

**The SDK drags gin 1.9.1 -> 1.10.1.** `oapi-codegen/runtime` requires
`gin-gonic/gin v1.10.1` in its own `go.mod`, so minimal version selection bumps
the console's HTTP framework even though the generated client never touches
gin. Pinning lower would need an `exclude`. The bump is carried, not worked
around: `go build ./...`, `go vet`, the full `go test ./...` and `-race` on
`internal/fincore` are green, including `internal/api` and `internal/auth`,
which are the two packages that actually use gin.

**A 2xx without a JSON content type is still a success.** The SDK only fills its
typed `JSON200` field when the response header says JSON; `internal/fincore`
decodes the body itself in that case instead of reporting a failure.
