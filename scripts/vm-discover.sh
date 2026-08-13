#!/usr/bin/env bash
# vm-discover.sh — READ-ONLY inventory of a VM's running Docker workload.
#
# Part of the "VM → GitOps" reproducible flow (see tasks/vm-gitops-migration-plan.md).
# This is the ONLY step that touches a VM over SSH, and it is strictly read-only:
# every remote command is an inspect/ls/version verb. It NEVER creates, stops,
# removes, or reconfigures anything. All mutations in the flow happen through the
# cloud (Portainer/gitops), never SSH.
#
# What it produces (into an output dir, default ./vm-discovery/<host>/):
#   - containers.txt        docker ps snapshot
#   - inspect/<name>.json   full `docker inspect` per container (the record)
#   - volumes.json          `docker volume inspect` of every referenced volume
#   - networks.txt          docker network ls
#   - REPORT.md             human summary: image tags, ports, restart, networks
#   - volumes.compose.yaml  ← the safety artifact: a ready-to-paste compose
#                             `volumes:` block declaring every named volume
#                             `external: true` with its EXACT name, so the gitops
#                             compose adopts live data instead of creating an
#                             empty `<stack>_<vol>` (the PG-volume data-loss risk).
#
# Usage:
#   scripts/vm-discover.sh user@host [-p PORT] [-i KEYFILE] [-o OUTDIR]
#
# Requires: ssh + docker CLI on the remote (read access). No jq needed anywhere —
# extraction uses `docker inspect --format` Go templates executed remotely.

set -euo pipefail

TARGET="${1:-}"; shift || true
PORT=22
KEY=""
OUTBASE="./vm-discovery"
while [ $# -gt 0 ]; do
  case "$1" in
    -p) PORT="$2"; shift 2;;
    -i) KEY="$2"; shift 2;;
    -o) OUTBASE="$2"; shift 2;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done
if [ -z "$TARGET" ]; then
  echo "usage: $0 user@host [-p PORT] [-i KEYFILE] [-o OUTDIR]" >&2
  exit 2
fi

SSH_OPTS=(-o BatchMode=yes -o StrictHostKeyChecking=accept-new -p "$PORT")
[ -n "$KEY" ] && SSH_OPTS+=(-i "$KEY")

# rsh runs a read-only command on the VM. Guard: refuse anything that is not an
# obviously read-only docker/system verb, so a copy-paste slip can't mutate prod.
rsh() { ssh "${SSH_OPTS[@]}" "$TARGET" "$@"; }

HOSTSLUG="$(echo "$TARGET" | tr '@/:' '___')"
OUT="$OUTBASE/$HOSTSLUG"
mkdir -p "$OUT/inspect"
echo "[discover] target=$TARGET port=$PORT out=$OUT (READ-ONLY)"

# ── snapshot ps ──────────────────────────────────────────────────────────────
rsh 'docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}"' \
  | tee "$OUT/containers.txt"

# names of running containers
NAMES=()
while IFS= read -r _name; do
  [ -n "$_name" ] && NAMES+=("$_name")
done < <(rsh 'docker ps --format "{{.Names}}"')
if [ "${#NAMES[@]}" -eq 0 ]; then
  echo "[discover] no running containers found" >&2
fi

# ── per-container inspect (the record) ───────────────────────────────────────
VOLFILE="$OUT/.volumes.tsv"
: > "$VOLFILE"
: > "$OUT/REPORT.md"
{
  echo "# VM discovery report — $TARGET"
  echo
  echo "> Read-only snapshot. Feed this into Phase 1 (author gitops compose)."
  echo
} >> "$OUT/REPORT.md"

for n in "${NAMES[@]}"; do
  rsh "docker inspect $n" > "$OUT/inspect/$n.json"

  IMAGE="$(rsh "docker inspect --format '{{.Config.Image}}' $n")"
  RESTART="$(rsh "docker inspect --format '{{.HostConfig.RestartPolicy.Name}}' $n")"
  NETS="$(rsh "docker inspect --format '{{range \$k,\$v := .NetworkSettings.Networks}}{{\$k}} {{end}}' $n")"
  # published ports
  PORTS="$(rsh "docker inspect --format '{{range \$p,\$c := .NetworkSettings.Ports}}{{\$p}} {{end}}' $n")"

  {
    echo "## $n"
    echo "- image: \`$IMAGE\`"
    echo "- restart: \`$RESTART\`"
    echo "- networks: \`$NETS\`"
    echo "- ports: \`$PORTS\`"
    echo "- mounts:"
  } >> "$OUT/REPORT.md"

  # Mounts: Type, Name (named vol) or Source (bind), Destination, RW
  # One line per mount; capture named volumes for the external block.
  while IFS='|' read -r mtype mname msrc mdst mrw; do
    [ -z "$mtype" ] && continue
    if [ "$mtype" = "volume" ]; then
      echo "  - volume **$mname** → \`$mdst\` (rw=$mrw)" >> "$OUT/REPORT.md"
      printf '%s\t%s\n' "$mname" "$mdst" >> "$VOLFILE"
    else
      echo "  - bind \`$msrc\` → \`$mdst\` (rw=$mrw)" >> "$OUT/REPORT.md"
    fi
  done < <(rsh "docker inspect --format '{{range .Mounts}}{{.Type}}|{{.Name}}|{{.Source}}|{{.Destination}}|{{.RW}}{{\"\\n\"}}{{end}}' $n")
  echo >> "$OUT/REPORT.md"
done

# ── volumes.json for every referenced named volume ───────────────────────────
awk -F'\t' '!seen[$1]++' "$VOLFILE" > "$VOLFILE.uniq" && mv "$VOLFILE.uniq" "$VOLFILE"
VOL_NAMES="$(cut -f1 "$VOLFILE" | tr '\n' ' ')"
if [ -n "${VOL_NAMES// /}" ]; then
  # shellcheck disable=SC2086
  rsh "docker volume inspect $VOL_NAMES" > "$OUT/volumes.json" || true
fi

# ── networks ─────────────────────────────────────────────────────────────────
rsh 'docker network ls' > "$OUT/networks.txt"

# ── the safety artifact: external-volume compose block ───────────────────────
# Every named volume becomes `external: true` pinned to its literal name, so the
# gitops compose ADOPTS the live data volume instead of creating a fresh empty
# `<stack>_<vol>`. This is the whole point of the migration — do not edit the
# names by hand.
{
  echo "# Auto-generated by scripts/vm-discover.sh from live volume names."
  echo "# Paste under the gitops compose.yaml top-level 'volumes:' key. Each"
  echo "# named volume is pinned external so 'docker compose up' attaches the"
  echo "# EXISTING prod data instead of creating an empty one. DO NOT rename."
  if [ ! -s "$VOLFILE" ]; then
    echo "# (no named volumes found — check for bind mounts in REPORT.md and"
    echo "#  mirror their host paths verbatim in the service instead.)"
  else
    echo "volumes:"
    while IFS="$(printf '\t')" read -r v dst; do
      [ -z "$v" ] && continue
      echo "  ${v}:"
      echo "    external: true"
      echo "    name: ${v}          # mounted at ${dst} in prod"
    done < "$VOLFILE"
  fi
} > "$OUT/volumes.compose.yaml"
rm -f "$VOLFILE"

echo
echo "[discover] done. Artifacts in $OUT/"
echo "[discover]   REPORT.md              human summary"
echo "[discover]   volumes.compose.yaml   external-volume block (Phase 1 input)"
echo "[discover]   inspect/*.json         full record per container"
echo "[discover] NOTE: nothing on the VM was modified."
