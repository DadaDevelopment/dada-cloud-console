#!/usr/bin/env bash
# box-unit-cost-check.sh — reconciles the DERIVED Dada Box price against reality and
# alerts when the margin on a box-hour is not positive.
#
# Why this needs to be a recurring check and not a one-off spreadsheet: the price is
# derived (decision D5 — no price table). Nobody types a per-minute figure anywhere;
# it falls out of billing/data/box-fleet-cost.yaml divided by 43200. That is what
# makes the "a box-month costs what the VM costs" claim hold by arithmetic, and it is
# ALSO what makes the price move silently the moment somebody edits the hardware bill
# or the pool's reserved capacity. A negative margin would then be a config edit away,
# with no error anywhere and no test that would fail.
#
# TWO ARMS, and the second is the one that can actually surprise us:
#
#   A. INTERNAL CONSISTENCY (always runs, needs nothing but this repo). The derived
#      unit cost is positive, the markup is applied, and price > internal cost for
#      every catalog profile. This catches a broken box-fleet-cost.yaml, a markup
#      set to 1.0 or below, and a zero unit cost that would price every minute free.
#
#   B. RECONCILIATION AGAINST MEASURED COST (needs OPENCOST_URL). Our modelled cost
#      per box-hour is compared with what the cluster actually charges for the same
#      shape. This is the only arm that can reveal the model being WRONG rather than
#      merely self-consistent — arm A would happily bless a model that is internally
#      tidy and off by 3x. It is SKIPPED, loudly, when OPENCOST_URL is unset: a
#      reconciliation that silently passes without its reference is worse than one
#      that admits it did not run.
#
# Usage:
#   scripts/box-unit-cost-check.sh                       # arm A only
#   OPENCOST_URL=http://opencost.monitoring:9003 \
#     scripts/box-unit-cost-check.sh                     # both arms
#
#   MARGIN_FLOOR=0.25   minimum acceptable margin as a fraction of price (default 0.25)
#
# Requires: go, jq. curl only for arm B.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT/backend"

command -v go >/dev/null || { echo "go not found" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq not found" >&2; exit 2; }

MARGIN_FLOOR="${MARGIN_FLOOR:-0.25}"
FAILED=0
say()  { printf '\n\033[1m[box-unit-cost] %s\033[0m\n' "$*"; }
ok()   { printf '  \033[32m✔ %s\033[0m\n' "$*"; }
bad()  { printf '  \033[31mx %s\033[0m\n' "$*"; FAILED=1; }
warn() { printf '  \033[33m! %s\033[0m\n' "$*"; }

say "derived unit economics (cmd/boxcost — the same code the meter prices with)"
COST_JSON="$(go run ./cmd/boxcost)" || { echo "boxcost failed — the box price cannot be derived at all" >&2; exit 1; }

MINUTES="$(jq -r '.minutes_per_month' <<<"$COST_JSON")"
MARKUP="$(jq -r '.markup' <<<"$COST_JSON")"
if [ "$MINUTES" != "43200" ]; then
  bad "minutes_per_month = $MINUTES, want 43200 — the \"43200 minutes cost one month\" identity is broken"
else
  ok "minutes_per_month = 43200"
fi
if jq -e '.markup > 1.0' >/dev/null <<<"$COST_JSON"; then
  ok "markup = $MARKUP"
else
  bad "markup = $MARKUP: at or below 1.0 every box is sold at or under cost"
fi
if jq -e '.unit_cost | (.per_vcpu_rub_month > 0 and .per_gb_ram_rub_month > 0 and .per_gb_storage_rub_month > 0)' \
     >/dev/null <<<"$COST_JSON"; then
  ok "unit cost positive in all three dimensions"
else
  bad "a unit cost dimension is zero or negative — minutes would be metered free"
  jq '.unit_cost' <<<"$COST_JSON"
fi

say "arm A: margin per box-hour, per profile (floor ${MARGIN_FLOOR})"
printf '  %-14s %10s %10s %10s %9s\n' profile price/h internal/h sleep/h margin
while read -r profile price internal sleeping margin; do
  printf '  %-14s %10.4f %10.4f %10.4f %8.1f%%\n' \
    "$profile" "$price" "$internal" "$sleeping" "$(jq -n "$margin * 100")"
  if jq -e -n "$margin <= 0" >/dev/null; then
    bad "$profile: NEGATIVE margin — every active minute of this size loses money"
  elif jq -e -n "$margin < $MARGIN_FLOOR" >/dev/null; then
    bad "$profile: margin below the ${MARGIN_FLOOR} floor"
  fi
  # A sleeping box must be strictly cheaper than an active one, or "asleep costs less"
  # is not true and the suspend-on-cap behaviour saves the customer nothing.
  if jq -e -n "$sleeping <= 0 or $sleeping >= $price" >/dev/null; then
    bad "$profile: sleeping accrual ($sleeping/h) is not strictly between 0 and the active price ($price/h)"
  fi
done < <(jq -r '.profiles[] | [.profile, .price_rub_hour, .internal_rub_hour, .sleeping_rub_hour,
                               ((.price_rub_hour - .internal_rub_hour) / .price_rub_hour)] | @tsv' <<<"$COST_JSON")
[ "$FAILED" -eq 0 ] && ok "every profile clears the margin floor and sleeps cheaper than it runs"

say "arm B: reconciliation against measured cluster cost (OpenCost)"
if [ -z "${OPENCOST_URL:-}" ]; then
  warn "SKIPPED: OPENCOST_URL is unset."
  warn "Arm A only proves the model is SELF-CONSISTENT. It cannot tell you the model is right:"
  warn "a box-fleet-cost.yaml that understates the pool by 3x passes every check above."
  warn "Run this arm from inside the cluster before quoting the price publicly."
else
  command -v curl >/dev/null || { echo "curl not found" >&2; exit 2; }
  # OpenCost's allocation API, same surface internal/opencost reads. The window is a
  # day because a box-hour figure derived from a five-minute window is dominated by
  # whatever happened to be scheduled.
  ALLOC="$(curl -fsS "${OPENCOST_URL%/}/allocation/compute?window=24h&aggregate=namespace" || true)"
  if [ -z "$ALLOC" ]; then
    bad "OpenCost at $OPENCOST_URL did not answer; reconciliation not performed"
  else
    # Deliberately printed rather than auto-compared: the mapping from box pods to an
    # OpenCost aggregation depends on how the box pool is labelled in the cluster,
    # which is a runtime decision (ADR-019: the box is a container in the cloud we
    # already have, not a fleet). Hard-coding a namespace here would produce a
    # confident number about the wrong workload — the failure mode this whole exercise
    # exists to avoid.
    warn "OpenCost answered. Compare the box pool's measured \$/hour below against internal_rub_hour above;"
    warn "the mapping from box pods to an OpenCost aggregation is cluster-labelling-specific and is NOT guessed here."
    jq '.data | to_entries | map({ns: .key, cpuCost: (.value.cpuCost // 0), ramCost: (.value.ramCost // 0), pvCost: (.value.pvCost // 0)}) | sort_by(-.cpuCost) | .[0:10]' <<<"$ALLOC" || true
  fi
fi

say "verdict"
if [ "$FAILED" -eq 0 ]; then
  ok "the derived box price is internally consistent and every profile has a positive margin"
  exit 0
fi
bad "unit-cost check FAILED"
exit 1
