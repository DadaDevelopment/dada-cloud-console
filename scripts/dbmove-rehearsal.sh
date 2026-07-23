#!/usr/bin/env bash
# scripts/dbmove-rehearsal.sh: create a throwaway ServiceDatabaseV2 + RWO PVC with
# sentinel data in a scratch namespace, run dbmove against it, assert data survived,
# then clean up. Proves the full path (incl. Longhorn RWO->RWX restore) before prod.
set -euo pipefail
KCTX="83.222.27.62:26443"
NS_SRC="dbmove-rehearsal-src"
NS_DST="dbmove-rehearsal-dst"
# ... create ns, a small RWO longhorn-prod PVC, write a sentinel file + a throwaway
# postgres logical DB with a sentinel table; run: dbmove --config configs/rehearsal.yaml
# --execute; assert the dst PVC (RWX) file sha256 matches + the DB row is present;
# print PASS/FAIL; delete both scratch namespaces + scratch PV.
echo "rehearsal harness: fill in per live Longhorn restore shape during Task 8"
