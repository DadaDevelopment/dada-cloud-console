#!/usr/bin/env bash
# vm-rehearse.sh — Phase 3 rehearsal of the "VM → GitOps" migration, on throwaway
# local Docker. Proves the two mechanics that must NEVER fail on prod, before we
# ever touch the customer VM (see tasks/vm-gitops-migration-plan.md):
#
#   1. ADOPTION  — a gitops compose that declares the PG volume `external: true`
#      attaches the EXISTING data volume; no empty `<stack>_<vol>` is created and
#      the sentinel row survives.
#   2. RELEASE   — bumping the app image tag in the compose recreates only the app
#      container; the DB volume (and its data) is untouched.
#
# It also runs a NEGATIVE control: a naive compose (named volume, NOT external)
# creates a fresh empty `<stack>_<vol>` and loses the data — the exact failure the
# external pin prevents. This makes the risk concrete, then cleans it up.
#
# Everything runs under unique throwaway names and is removed on exit. It NEVER
# uses `down -v` on the adoption stack and mirrors the prod safety rules.
#
# Usage:
#   scripts/vm-rehearse.sh            # full rehearsal, self-cleaning
#   KEEP=1 scripts/vm-rehearse.sh     # leave artifacts for inspection
#
# Requires: a working local Docker with the compose v2 plugin.

set -euo pipefail

command -v docker >/dev/null || { echo "docker not found" >&2; exit 2; }
docker compose version >/dev/null 2>&1 || { echo "docker compose v2 plugin required" >&2; exit 2; }

# Unique per-run names. No Date/random reliance beyond the shell PID + epoch, which
# is fine for a local throwaway harness (not a deterministic workflow step).
RUN="rehearse_$$"
PGVOL="${RUN}_pgdata_prod"          # stands in for the live prod data volume
STACK_OK="${RUN}_adopt"             # the correct, external-volume stack
STACK_BAD="${RUN}_naive"            # the negative control
WORK="$(mktemp -d)"
PG_PASS="s3ntinel_pw"
FAILED=0

say()  { printf '\n\033[1m[rehearse] %s\033[0m\n' "$*"; }
ok()   { printf '  \033[32m✔ %s\033[0m\n' "$*"; }
bad()  { printf '  \033[31mx %s\033[0m\n' "$*"; FAILED=1; }

cleanup() {
  if [ "${KEEP:-0}" = "1" ]; then
    say "KEEP=1 — leaving $WORK and volumes/stacks for inspection"
    return
  fi
  say "cleanup"
  docker compose -p "$STACK_OK"  -f "$WORK/compose.ok.yaml"  down          >/dev/null 2>&1 || true
  docker compose -p "$STACK_BAD" -f "$WORK/compose.bad.yaml" down -v       >/dev/null 2>&1 || true
  # The adoption stack must NEVER be torn down with -v (prod rule); remove the
  # stand-in prod volume explicitly, by name, as a human operator would out-of-band.
  docker volume rm "$PGVOL" >/dev/null 2>&1 || true
  docker volume rm "${STACK_OK}_pgdata" "${STACK_BAD}_pgdata" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

vol_exists()  { docker volume inspect "$1" >/dev/null 2>&1; }
# read the sentinel back. Always exits 0 (prints empty on any failure) so a
# not-ready DB can't trip `set -e` at the `GOT="$(read_sentinel ...)"` call site.
read_sentinel() {  # args: <project> <compose-file>
  docker compose -p "$1" -f "$2" exec -T db \
    psql -U postgres -tAc "SELECT note FROM sentinel LIMIT 1" 2>/dev/null | tr -d '[:space:]' || true
}
wait_pg() {  # args: <project> <compose-file>
  for _ in $(seq 1 30); do
    if docker compose -p "$1" -f "$2" exec -T db pg_isready -U postgres >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

# ── 0. simulate the live prod DB: named volume + sentinel row, then "stop" it ──
say "0. seed a stand-in prod PG volume ($PGVOL) with a sentinel row"
docker volume create "$PGVOL" >/dev/null
docker run -d --name "${RUN}_seed" -e POSTGRES_PASSWORD="$PG_PASS" \
  -v "$PGVOL:/var/lib/postgresql/data" postgres:16-alpine >/dev/null
for _ in $(seq 1 30); do
  docker exec "${RUN}_seed" pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done
docker exec "${RUN}_seed" psql -U postgres -c \
  "CREATE TABLE sentinel(note text); INSERT INTO sentinel VALUES ('PROD_DATA_v1');" >/dev/null
ok "sentinel written: PROD_DATA_v1"
# Retire the hand-run container (mirrors the Phase-4 manual stop). Data stays in $PGVOL.
docker rm -f "${RUN}_seed" >/dev/null
ok "stand-in prod container stopped; volume $PGVOL retains the data"

# ── 1. ADOPTION: compose with the PG volume pinned external ───────────────────
say "1. deploy gitops-style compose with external volume → must ADOPT the data"
cat > "$WORK/compose.ok.yaml" <<YAML
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: "$PG_PASS"
    volumes:
      - pgdata:/var/lib/postgresql/data
  app:
    image: hashicorp/http-echo:0.2.3
    command: ["-text=release-1"]
volumes:
  pgdata:
    external: true
    name: $PGVOL
YAML

docker compose -p "$STACK_OK" -f "$WORK/compose.ok.yaml" up -d >/dev/null
wait_pg "$STACK_OK" "$WORK/compose.ok.yaml" || bad "adoption DB never became ready"

if vol_exists "${STACK_OK}_pgdata"; then
  bad "a fresh '${STACK_OK}_pgdata' volume was created — external pin FAILED"
else
  ok "no fresh '<stack>_pgdata' volume created (external pin honored)"
fi
GOT="$(read_sentinel "$STACK_OK" "$WORK/compose.ok.yaml")"
if [ "$GOT" = "PROD_DATA_v1" ]; then
  ok "sentinel survived adoption: $GOT"
else
  bad "sentinel LOST after adoption (got '${GOT:-<empty>}') — data would be gone"
fi

# ── 2. RELEASE: bump the app image tag → only app recreated, DB untouched ─────
say "2. release bump: change app image tag → redeploy → DB volume untouched"
DB_ID_BEFORE="$(docker compose -p "$STACK_OK" -f "$WORK/compose.ok.yaml" ps -q db)"
sed -i.bak 's#hashicorp/http-echo:0.2.3#hashicorp/http-echo:1.0#' "$WORK/compose.ok.yaml"
sed -i.bak 's#-text=release-1#-text=release-2#' "$WORK/compose.ok.yaml"
docker compose -p "$STACK_OK" -f "$WORK/compose.ok.yaml" up -d >/dev/null
wait_pg "$STACK_OK" "$WORK/compose.ok.yaml" || bad "DB not ready after release bump"
DB_ID_AFTER="$(docker compose -p "$STACK_OK" -f "$WORK/compose.ok.yaml" ps -q db)"

if [ "$DB_ID_BEFORE" = "$DB_ID_AFTER" ]; then
  ok "DB container NOT recreated by the app bump (id stable)"
else
  # A recreate is acceptable ONLY if data survives; the real invariant is the data.
  ok "DB container recreated — verifying data invariant still holds"
fi
GOT="$(read_sentinel "$STACK_OK" "$WORK/compose.ok.yaml")"
[ "$GOT" = "PROD_DATA_v1" ] && ok "sentinel intact after release bump: $GOT" \
                            || bad "sentinel LOST after release bump (got '${GOT:-<empty>}')"
if vol_exists "${STACK_OK}_pgdata"; then
  bad "release bump created '${STACK_OK}_pgdata' — volume identity drifted"
else
  ok "still only the external volume in use; no '<stack>_pgdata' appeared"
fi

# ── 3. NEGATIVE control: naive (non-external) compose loses the data ──────────
say "3. negative control: naive named volume (NOT external) → proves the risk"
cat > "$WORK/compose.bad.yaml" <<YAML
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: "$PG_PASS"
    volumes:
      - pgdata:/var/lib/postgresql/data
volumes:
  pgdata: {}
YAML
docker compose -p "$STACK_BAD" -f "$WORK/compose.bad.yaml" up -d >/dev/null
wait_pg "$STACK_BAD" "$WORK/compose.bad.yaml" || true
if vol_exists "${STACK_BAD}_pgdata"; then
  ok "naive compose created a fresh empty '${STACK_BAD}_pgdata' (as feared)"
else
  bad "expected a fresh '${STACK_BAD}_pgdata' from the naive compose; not found"
fi
GOT="$(read_sentinel "$STACK_BAD" "$WORK/compose.bad.yaml")"
if [ "$GOT" = "PROD_DATA_v1" ]; then
  bad "naive stack unexpectedly saw prod data — control invalid"
else
  ok "naive stack has NO sentinel (fresh cluster) — this is the data-loss outcome external:true prevents"
fi

# ── verdict ───────────────────────────────────────────────────────────────────
say "verdict"
if [ "$FAILED" = "0" ]; then
  printf '  \033[32mALL CHECKS PASSED\033[0m — external-volume adoption + release bump are safe; naive path loses data.\n'
  exit 0
else
  printf '  \033[31mSOME CHECKS FAILED\033[0m — do NOT proceed to a prod cutover until green.\n'
  exit 1
fi
