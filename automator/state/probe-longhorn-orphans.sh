#!/usr/bin/env bash
# Гейт пульса: осиротевшие Longhorn-снапшоты, которые держат диск ноды.
#
# Родился 2026-08-06 (второй раз за сутки): kubectl delete snapshots.longhorn.io
# ставит только markRemoved=true, реальный coalesce/освобождение места не
# происходит без явного POST .../action=snapshotPurge через longhorn-backend.
# Про это уже была память (project_longhorn_deleted_snapshot_keeps_disk.md) —
# и всё равно повторилось: значит дыра в механизме (никто не гоняет purge
# сам), а не в знании. Этот скрипт закрывает дыру диагностикой на пульсе.
#
# Журнал state/.longhorn-purge-log существует потому, что в CRD v1.6.1 нет ни
# одного durable-поля для "purge гонялся": проверены живьём markRemoved,
# readyToUse, size, error, finalizers, deletionTimestamp, purgeStatus —
# purgeStatus гаснет в null через несколько минут после успешного purge
# (мирроит live-процесс движка, не история), остальные вообще не про purge.
# Поэтому источник правды — сам факт вызова, записанный этим скриптом.
#
# Диагностика (дефолт) ничего не меняет в кластере. Действие — только через
# --purge <volume>, и только тогда пишется строка в журнал.
#
# Использование:
#   state/probe-longhorn-orphans.sh                 diagnostics only
#   state/probe-longhorn-orphans.sh --purge <vol>    purge one volume + log it

if ! command -v jq >/dev/null 2>&1; then
  echo "ПРОБА НЕ ПРОВОДИЛАСЬ: на этой машине нет jq, а весь разбор снапшотов Longhorn написан на нём."
  echo "Это НЕ 'LONGHORN-CLEAN' - раньше скрипт печатал именно его после того, как jq падал."
  exit 3
fi

set -uo pipefail

PROXY_PORT="${PROXY_PORT:-8899}"
PROBE_IP="${PROBE_IP:-155.212.223.198}"
MIN_AGE_SEC=3600
MIN_SIZE_BYTES=1073741824
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_FILE="${SCRIPT_DIR}/.longhorn-purge-log"

iface=$(route -n get "$PROBE_IP" 2>/dev/null | awk '/interface:/{print $2}')
case "$iface" in
  utun*|ipsec*|ppp*)
    if ! (exec 3<>"/dev/tcp/127.0.0.1/${PROXY_PORT}") 2>/dev/null; then
      echo "PROXY-DOWN: маршрут к ${PROBE_IP} идёт через ${iface} (туннель), а bypass-прокси на :${PROXY_PORT} не отвечает."
      echo "Подними: nohup python3 ${SCRIPT_DIR}/vpn-bypass-proxy.py --port ${PROXY_PORT} & export HTTPS_PROXY=http://127.0.0.1:${PROXY_PORT}"
      exit 2
    fi
    export HTTPS_PROXY="http://127.0.0.1:${PROXY_PORT}"
    ;;
esac

if ! kubectl get nodes >/dev/null 2>&1; then
  echo "KUBECTL-FAILED: нет доступа к кластеру даже через прокси (HTTPS_PROXY=${HTTPS_PROXY:-<пусто>})."
  exit 2
fi

pick_manager_pod() {
  kubectl -n longhorn-system get pods -l app=longhorn-manager -o json |
    jq -r '[.items[] | select(.status.phase=="Running")][0] | "\(.metadata.name)\t\(.status.podIP)"'
}

# --purge <volume> — единственный режим, что-то меняющий в кластере.
if [ "${1:-}" = "--purge" ]; then
  vol="${2:-}"
  if [ -z "$vol" ]; then
    echo "USAGE: $0 --purge <volume-name>"
    exit 2
  fi
  if ! kubectl -n longhorn-system get volumes.longhorn.io "$vol" >/dev/null 2>&1; then
    echo "NO-SUCH-VOLUME: $vol"
    exit 2
  fi

  IFS=$'\t' read -r mgr_pod mgr_ip < <(pick_manager_pod)
  if [ -z "$mgr_pod" ] || [ -z "$mgr_ip" ]; then
    echo "NO-LONGHORN-MANAGER-POD"
    exit 2
  fi

  before=$(kubectl -n longhorn-system get volumes.longhorn.io "$vol" -o json)
  state=$(echo "$before" | jq -r '.status.state')
  robust=$(echo "$before" | jq -r '.status.robustness')
  if [ "$state" != "attached" ] || [ "$robust" != "healthy" ]; then
    echo "NOT-SAFE: volume $vol is state=$state robustness=$robust (нужно attached healthy) — НЕ пургаю."
    exit 1
  fi

  echo "Purging $vol via $mgr_pod ($mgr_ip)..."
  resp=$(kubectl -n longhorn-system exec "$mgr_pod" -- curl -s -X POST \
    "http://${mgr_ip}:9500/v1/volumes/${vol}?action=snapshotPurge" -H "Content-Type: application/json" -d '{}')
  echo "$resp" | jq -e '.purgeStatus' >/dev/null 2>&1 || { echo "PURGE-CALL-FAILED"; echo "$resp"; exit 1; }

  for i in $(seq 1 30); do
    sleep 2
    cur=$(kubectl -n longhorn-system exec "$mgr_pod" -- curl -s "http://${mgr_ip}:9500/v1/volumes/${vol}")
    ps=$(echo "$cur" | jq -c '.purgeStatus // []')
    done_all=$(echo "$ps" | jq '(length>0) and all(.[]; .state=="complete" and .progress==100 and ((.error//"")==""))')
    err_any=$(echo "$ps" | jq -r '[.[] | select((.error//"")!="")] | length')
    if [ "$err_any" != "0" ]; then
      echo "PURGE-ERROR:"; echo "$ps" | jq .
      exit 1
    fi
    if [ "$done_all" = "true" ]; then
      st=$(echo "$cur" | jq -r '.state')
      rb=$(echo "$cur" | jq -r '.robustness')
      echo "PURGE-COMPLETE: $vol state=$st robustness=$rb"
      snaps=$(kubectl -n longhorn-system get snapshots.longhorn.io -o json |
        jq -r --arg v "$vol" '.items[] | select(.spec.volume==$v and ((.status.markRemoved==true) or (.status.readyToUse==false))) | [.metadata.name,(.status.size//0)] | @tsv')
      ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
      if [ -n "$snaps" ]; then
        while IFS=$'\t' read -r sname ssize; do
          [ -z "$sname" ] && continue
          sgib=$(echo "$ssize $((1024*1024*1024))" | awk '{printf "%.2f", $1/$2}')
          printf '%s\t%s\t%s\t%s\n' "$ts" "$vol" "$sname" "$sgib" >> "$LOG_FILE"
          echo "logged: $ts $vol $sname ${sgib}GiB -> $LOG_FILE"
        done <<< "$snaps"
      else
        echo "NOTE: purge complete, но markRemoved/not-ready снапшотов на $vol сейчас не нашлось — нечего логировать."
      fi
      exit 0
    fi
  done
  echo "PURGE-TIMEOUT: не дождался complete за 60с, проверь вручную: kubectl -n longhorn-system get volumes.longhorn.io $vol -o json | jq .status.purgeStatus"
  exit 1
fi

echo "=== Longhorn nodes: storageAvailable / Schedulable ==="
printf '%-40s %12s %-6s %s\n' "NODE" "AVAIL_GiB" "SCHED" "REASON"
kubectl -n longhorn-system get nodes.longhorn.io -o json | jq -r '
  .items[] | .metadata.name as $n | .status.diskStatus // {} | to_entries[] |
  .value as $d |
  ($d.storageAvailable // 0) as $avail |
  (($d.conditions // []) | map(select(.type=="Schedulable")) | .[0]) as $c |
  [$n, ($avail / 1073741824 | tostring), ($c.status // "?"), ($c.reason // "-")] | @tsv
' | while IFS=$'\t' read -r node avail sched reason; do
  printf '%-40s %12.2f %-6s %s\n' "$node" "$avail" "$sched" "$reason"
done

echo
echo "=== Снапшоты markRemoved/not-ready (кандидаты) ==="
now_epoch=$(date -u +%s)
rows=$(kubectl -n longhorn-system get snapshots.longhorn.io -o json | jq -r '
  .items[] |
  select((.status.markRemoved == true) or (.status.readyToUse == false)) |
  [.spec.volume, .metadata.name, (.status.size // "0"), .metadata.creationTimestamp] | @tsv
')

purged_lines=()
orphan_lines=()
found_count=0
found_bytes=0

if [ -n "$rows" ]; then
  while IFS=$'\t' read -r vol name size created; do
    [ -z "$name" ] && continue
    size="${size:-0}"
    [ "$size" -lt "$MIN_SIZE_BYTES" ] 2>/dev/null && continue
    created_epoch=$(date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$created" +%s 2>/dev/null || date -u -d "$created" +%s 2>/dev/null)
    [ -z "$created_epoch" ] && continue
    age_sec=$(( now_epoch - created_epoch ))
    [ "$age_sec" -lt "$MIN_AGE_SEC" ] && continue

    size_gib=$(echo "$size $((1024*1024*1024))" | awk '{printf "%.2f", $1/$2}')
    age_h=$(( age_sec / 3600 ))
    line=$(printf '%-42s %-38s %10s %sh' "$vol" "$name" "$size_gib" "$age_h")

    logged_gib=""
    if [ -f "$LOG_FILE" ]; then
      logged_gib=$(awk -F'\t' -v s="$name" '$3==s{g=$4} END{print g}' "$LOG_FILE")
    fi

    if [ -n "$logged_gib" ] && awk -v cur="$size_gib" -v logged="$logged_gib" 'BEGIN{exit !(cur <= logged + 0.05)}'; then
      purged_lines+=("$line")
    else
      orphan_lines+=("$line")
      found_count=$((found_count + 1))
      found_bytes=$((found_bytes + size))
    fi
  done <<< "$rows"
fi

if [ "${#purged_lines[@]}" -gt 0 ]; then
  echo
  echo "=== Уже отпурганы, ждут GC (действий не требуют) ==="
  printf '%-42s %-38s %10s %s\n' "VOLUME" "SNAPSHOT" "SIZE_GiB" "AGE"
  printf '%s\n' "${purged_lines[@]}"
fi

if [ "${#orphan_lines[@]}" -gt 0 ]; then
  echo
  echo "=== Требуют purge ==="
  printf '%-42s %-38s %10s %s\n' "VOLUME" "SNAPSHOT" "SIZE_GiB" "AGE"
  printf '%s\n' "${orphan_lines[@]}"
  echo
  echo "Purge: $0 --purge <volume>  (сам найдёт longhorn-manager под, вызовет snapshotPurge, дождётся complete, запишет в журнал)"
fi

if [ "$found_count" -eq 0 ]; then
  echo
  echo "LONGHORN-CLEAN"
  exit 0
fi

found_gib=$(echo "$found_bytes $((1024*1024*1024))" | awk '{printf "%.2f", $1/$2}')
echo
echo "LONGHORN-ORPHANS ${found_count} ${found_gib}"
exit 1
