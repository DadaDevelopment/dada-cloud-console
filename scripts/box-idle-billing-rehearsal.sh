#!/usr/bin/env bash
# box-idle-billing-rehearsal.sh — proves, against a real Postgres, the one claim the
# Dada Box price rests on: AN IDLE BOX ACCUMULATES ZERO, and a busy one accumulates
# exactly the minutes it worked.
#
# Modelled on scripts/vm-rehearse.sh, including its most important habit: a NEGATIVE
# CONTROL. Asserting "the idle box has no rows" is worthless on its own — an empty
# table satisfies it, and so does a meter that never ran at all. So the script runs
# both arms in the same database, in the same pass, and requires the busy arm to have
# produced rows before it will believe the idle arm's zero.
#
# The three things it checks, in the order they can fail:
#
#   1. The ledger EXISTS and the funnel view was created (migration 063). Both, not
#      one: every environment that migrated past 060 before 063 existed skipped that
#      view silently, so its absence is the expected pre-063 state and its presence
#      is what un-darkens the experiment's headline metric.
#   2. AN IDLE HOUR WRITES NO ROW AT ALL — not a row with cost_rub = 0. The absence
#      of the row is the "not billed" statement, and it is the only version of that
#      statement a later pricing change cannot quietly reverse.
#   3. A busy box bills exactly the minutes it was busy, at the derived per-minute
#      price, and re-running the meter over the same minutes changes nothing.
#
# It does NOT invent its own arithmetic. Every number it checks comes from the same
# code the production meter runs, executed through `go test`, because a rehearsal
# that reimplements the thing it is rehearsing proves only that two implementations
# agree with each other.
#
# Usage:
#   TEST_DATABASE_URL=postgres://dada@127.0.0.1:5433/box_rehearsal?sslmode=disable \
#     scripts/box-idle-billing-rehearsal.sh
#
#   The database must exist and be reachable; the script applies the migrations
#   itself. Use a THROWAWAY database — it writes and deletes rows.
#
# Requires: go, psql, and a Postgres 16 reachable at TEST_DATABASE_URL.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT/backend"

command -v go   >/dev/null || { echo "go not found" >&2; exit 2; }
command -v psql >/dev/null || { echo "psql not found" >&2; exit 2; }
: "${TEST_DATABASE_URL:?set TEST_DATABASE_URL to a THROWAWAY database}"

FAILED=0
say() { printf '\n\033[1m[box-billing] %s\033[0m\n' "$*"; }
ok()  { printf '  \033[32m✔ %s\033[0m\n' "$*"; }
bad() { printf '  \033[31mx %s\033[0m\n' "$*"; FAILED=1; }

q() { psql "$TEST_DATABASE_URL" -At -c "$1"; }

say "1/4  schema: applying migrations"
go run ./cmd/migrate >/dev/null
if [ "$(q "SELECT to_regclass('public.box_usage') IS NOT NULL")" = "t" ]; then
  ok "box_usage exists"
else
  bad "box_usage is missing — migration 063 did not apply"
fi
if [ "$(q "SELECT to_regclass('public.box_repeat_use_7d') IS NOT NULL")" = "t" ]; then
  ok "box_repeat_use_7d exists (the headline funnel metric has a data source)"
else
  bad "box_repeat_use_7d is missing — 063 did not re-run the gated block from 060, so \
dada_box_repeat_use_7d_ratio stays NaN forever on this environment"
fi

say "2/4  the ledger's shape: an idle minute must write NO ROW"
# The DB-backed metering tests ARE the assertion. They drive the production meter
# with an injected clock over 60 minutes per arm. Captured with -v because the pass/
# fail exit code cannot distinguish "asserted" from "skipped" — see step 3.
TESTLOG="$(mktemp)"
trap 'rm -f "$TESTLOG"' EXIT
if go test ./internal/api/ -count=1 -run 'TestMeterBoxMinutes_' -v >"$TESTLOG" 2>&1; then
  ok "TestMeterBoxMinutes_* pass: 10 active of 60 bills exactly 10; an idle hour writes 0 rows; replays are idempotent"
else
  bad "TestMeterBoxMinutes_* FAILED:"
  tail -40 "$TESTLOG" || true
fi

say "3/4  negative control: the zero must be a MEASURED zero"
# THE FAILURE THIS CATCHES IS THE LIKELY ONE. Every DB-backed test in this repo
# skips itself when TEST_DATABASE_URL is unset, and a skipped test reports success.
# So "go test passed" and "an idle box was proven to accumulate nothing" are two
# different statements, and without this step the script would happily confirm the
# second while only having established the first.
PASSED="$(grep -c '^--- PASS: TestMeterBoxMinutes_' "$TESTLOG" || true)"
SKIPPED="$(grep -c '^--- SKIP: TestMeterBoxMinutes_' "$TESTLOG" || true)"
if [ "${SKIPPED:-0}" -ne 0 ]; then
  bad "$SKIPPED metering test(s) SKIPPED — the ledger was never exercised, so its zero proves nothing"
  grep '^--- SKIP' -A1 "$TESTLOG" || true
elif [ "${PASSED:-0}" -lt 4 ]; then
  bad "only ${PASSED:-0} metering tests ran, expected at least 4 (ten-of-sixty, idle hour, sleeping box, idempotency)"
else
  ok "$PASSED metering tests actually executed against this database, 0 skipped"
fi

# A positive control on the STORAGE itself, independent of Go: if the grants in 063
# were wrong or the PK were missing, the tests above could still pass while the
# production role could not write a single row. One insert, one conflicting insert,
# one delete.
CTL_BOX='00000000-0000-4000-8000-00000000dead'
q "DELETE FROM box_usage WHERE box_id = '$CTL_BOX'" >/dev/null
q "INSERT INTO box_usage (box_id, minute_start, kind, cost_rub)
   VALUES ('$CTL_BOX', '2020-01-01T00:00:00Z', 'active', 1.5)" >/dev/null
q "INSERT INTO box_usage (box_id, minute_start, kind, cost_rub)
   VALUES ('$CTL_BOX', '2020-01-01T00:00:00Z', 'active', 99)
   ON CONFLICT (box_id, minute_start, kind) DO NOTHING" >/dev/null
CTL_SUM="$(q "SELECT sum(cost_rub) FROM box_usage WHERE box_id = '$CTL_BOX'")"
if [ "$CTL_SUM" = "1.500000" ]; then
  ok "the ledger accepts a row, refuses to overwrite it on PK conflict, and sums as NUMERIC (got $CTL_SUM)"
else
  bad "PK/append-only control returned '$CTL_SUM', want 1.500000 — either the primary key is missing \
(a replayed tick would double-bill) or the conflicting insert overwrote history"
fi
q "DELETE FROM box_usage WHERE box_id = '$CTL_BOX'" >/dev/null

# The tests clean up after themselves, so anything left here is a leak worth naming.
LEFTOVER="$(q "SELECT count(*) FROM box_usage")"
if [ "${LEFTOVER:-0}" -eq 0 ]; then
  ok "the harness left no rows behind"
else
  printf '  \033[33m! %s\033[0m\n' "$LEFTOVER ledger rows remain; a test cleanup did not run (harmless in a throwaway database)"
fi

say "4/4  the price, derived rather than declared"
# Printed, not asserted: the number changes whenever the hardware bill does, and a
# script that pinned it would have to be edited every time the fleet cost is edited.
# What IS asserted (in internal/billing/costengine) is the identity that makes the
# claim hold: 43200 minutes cost exactly one month.
if go test ./internal/billing/... -count=1 >/dev/null; then
  ok "the per-minute price is the monthly price / 43200 exactly (TestPerMinuteCostTimesMinutesPerMonthIsTheMonthlyCost)"
else
  bad "the box unit-cost derivation FAILED — box minutes would be priced wrong or at zero"
  go test ./internal/billing/... -count=1 2>&1 | tail -20 || true
fi

say "verdict"
if [ "$FAILED" -eq 0 ]; then
  ok "idle accumulates zero, busy accumulates exactly its minutes, and the zero is measured"
  exit 0
fi
bad "rehearsal FAILED — do not ship a price built on this"
exit 1
