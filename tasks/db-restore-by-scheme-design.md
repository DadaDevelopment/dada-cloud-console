# DB backup + restore "by scheme" (per-database) — final design

Decision: **K10/Kanister imperative**, build **per-DB backup AND restore**, restore gated by
**typed-name confirm**. Backups scheduled + cataloged by the console backend; execution reuses
the K10 Kanister tooling (postgres image, kopia, S3 profile) via imperative ActionSets.

## Verified facts (live/origin — do not re-litigate)

- Shared server `postgresql.databases.svc.cluster.local`; each ServiceDatabaseV2 = a logical
  `Database` + owner role `svc-<name>` via provider-sql (composition sql step). [live/origin]
- Current backups: DR policy `k10-disaster-recovery-policy` dumps the WHOLE server (`pg_dumpall
  --clean`) hourly/daily → the only working restore points. [live]
- The 8 `<name>-pg-policy` per-DB policies are **no-ops**: `selector: {}` → zero restore points.
  Data is protected only at whole-server granularity today. [live]
- K10 Policy cannot pass a `database` param or select a blueprint per-policy; `BlueprintBinding`
  is keyed by `(namespace, statefulset)` so all logical DBs share one blueprint. Per-DB via K10
  Policy/RestorePoint is impossible. [live schema]
- A raw Kanister `ActionSet` DOES carry `spec.actions[].options` → `.Options.database` is the
  per-DB channel. RestoreAction/Policy do not. [origin: restore-job.yaml stub]
- Blueprint source = `dada-argo/infrastructure/k10-backup-config/values.yaml` (backup L114
  pg_dumpall, restore L149 psql); app `k10-backup-config-beget`. NOT in argo-infra. [origin]
- pg superuser: secret `postgresql`/`postgres-password` (ns databases) as the blueprint uses;
  S3 store `f32d26dcb2b9-public`, creds `k10-s3-backups` (ns databases), path `k10/postgresql-logical`. [live/origin]
- Crossplane compose SA already has create on blueprints/actionsets; console SA has get/list on
  restorepoints only. [origin rbac]

## Architecture

### 1. Executor — per-DB Kanister blueprint (dada-argo)
New blueprint `postgres-logical-db-blueprint` (leave the whole-server DR blueprint UNTOUCHED —
don't break working DR backup). Actions, all `kind: StatefulSet` against `databases/postgresql`,
parameterized by `.Options.database` + `.Options.dumpPath`:
- `backup`: `pg_dump -Fc --no-owner -d "$DB"` | `kando location push --path "$dumpPath" --output-name kopiaOutput`.
- `restore`: `kando location pull --path "$dumpPath" --kopia-snapshot "$snap"` | `pg_restore --clean --if-exists --no-owner -d "$DB"`.
- `delete`: kando location delete of the artifact.
Superuser creds + S3 profile identical to the existing blueprint.

### 2. Catalog — console backend (dada-cloud)
Migration `db_backups`: `{id, project_id, environment_id, resource_name, database_name,
kopia_snapshot, dump_path, size_bytes, status, action_set, created_by, created_at, expires_at}`.
Source of truth the console lists from (K10 has no per-DB restore points).

### 3. Backup driver — backend
- Scheduler goroutine (ticker, per-plan frequency; default daily) → for each ServiceDatabaseV2
  with backup enabled → create a backup ActionSet (options.database=<db>,
  dumpPath=`dumps/<project>/<db>/<ts>.dump`, blueprint=postgres-logical-db-blueprint,
  target=postgresql STS, profile=`<name>-pg-profile`) → watch to terminal → record kopiaSnapshot
  + size into db_backups → enforce retention (delete ActionSet + row for expired).
- Also an on-demand `POST .../databases/{name}/backups` (write-gated) to snapshot now.
- New backend K10 client (typed/dynamic) that creates+watches ActionSets — mirrors cloudtask.

### 4. Restore — backend + frontend
- `GET .../databases/{name}/backups` → list db_backups rows (write-gated).
- `POST .../databases/{name}/restore` {backup_id} → RestoreServiceDatabase op (audited),
  backend creates a restore ActionSet from that backup's kopiaSnapshot; poll to terminal.
- Frontend: on DB detail, Backups panel (list + "Back up now"), Restore picks a backup →
  **typed-name confirm modal** (user types the db name) → triggers restore → "restoring" phase.

### 5. RBAC (helm dada-cloud-console)
Add to console ClusterRole: `cr.kanister.io/actionsets` create/get/list/watch/delete,
`cr.kanister.io/blueprints` get/list, `config.kio.kasten.io/profiles` get/list. (Keep the
existing restorepoints read.) Reuse the crossplane-system S3-creds Role pattern for any
namespaced grant if needed.

### 6. Cleanup of the dead per-DB policies
The `<name>-pg-policy`/PolicyPreset/Profile that the composition emits are no-op decoys
(selector:{}). Either fix their selector to make them real, or (since we drive per-DB via
ActionSets) drop the Policy/PolicyPreset from the composition and KEEP the Profile (we reuse
`<name>-pg-profile` for ActionSet push/pull). Decision: keep Profile, drop the dead Policy.

## Safety
- Only ever single `-d <db>`; never pg_dumpall/psql-whole-server in the per-DB path.
- Restore: typed-name confirm; take a fresh pre-restore backup automatically before restoring
  (so a bad restore is itself reversible).
- `--clean --if-exists --no-owner`; owner role guaranteed by provider-sql.
- RBAC scoped; every backup/restore audited; retention bounded.
- Do NOT touch the whole-server DR policy/blueprint (working protection floor).

## Build order
1. dada-argo per-DB blueprint (executor).
2. backend migration db_backups + K10 ActionSet client.
3. backend backup driver (scheduler + on-demand) + list endpoint.
4. backend RestoreServiceDatabase op + restore ActionSet + pre-restore backup.
5. helm RBAC.
6. frontend backups panel + typed-name restore modal.
7. swagger regen + tests; drift-check.
