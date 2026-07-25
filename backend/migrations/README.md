# Migrations

Runner: `backend/internal/db/migrations.go` (`RunMigrations`).

How it works:

- Discovers every `*.sql` file in this directory, sorts lexically, applies each one not yet recorded in `schema_migrations`.
- Tracking key (`version`) is the **full filename without `.sql`** — e.g. `049_user_onboarding`, not `049`.
- No high-water mark: a file that lands in git after a higher-numbered one has already been applied still gets picked up and applied on next boot.

Consequence: duplicate number prefixes are **safe**. `049_snapshot_first_seen.sql` and `049_user_onboarding.sql` (parallel sessions collided on the number) are two distinct versions — each applies exactly once, neither skips or shadows the other. Verified on prod `cloud-console.schema_migrations` 2026-07-25: both rows present, applied in lexical order.

Conventions:

- Still prefer unique, monotonically increasing numbers — the prefix only controls apply ORDER, so keep a migration's number higher than anything it depends on.
- Never rename an applied migration file: the tracking key is the filename, so a rename makes the runner re-apply it under the new name.
- Write migrations idempotent-friendly (`IF NOT EXISTS`, guarded `ALTER`) where cheap.
