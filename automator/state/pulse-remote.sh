#!/usr/bin/env bash
# pulse-remote.sh - читает почасовой снимок платформы из ветки pulse
# (pulse/latest.json) через gh api. На этой VM нет jq, поэтому весь
# разбор делает python-хелпер pulse-read.py рядом со скриптом.
#
# Коды выхода:
#   0 - снимок свежий и разобран
#   2 - снимок протух (возраст > порога) - поломки могли уйти из-под наблюдения
#   3 - снимка нет / gh не смог достать / JSON битый - пульс-долг = unmeasured
#
# Usage: ./pulse-remote.sh [--json]

set -uo pipefail

REPO="DadaDevelopment/argo-infra"
BRANCH="pulse"
PATH_IN_REPO="pulse/latest.json"
STALE_MINUTES=90
HERE="$(cd "$(dirname "$0")" && pwd)"

raw_json_mode=0
[ "${1:-}" = "--json" ] && raw_json_mode=1

content_b64=$(gh api "repos/${REPO}/contents/${PATH_IN_REPO}?ref=${BRANCH}" --jq .content 2>/tmp/pulse-remote-gh-err.$$)
gh_rc=$?
gh_err=$(cat /tmp/pulse-remote-gh-err.$$ 2>/dev/null)
rm -f /tmp/pulse-remote-gh-err.$$

if [ $gh_rc -ne 0 ] || [ -z "$content_b64" ]; then
  echo "ПУЛЬС НЕДОСТУПЕН, долг цикла = unmeasured"
  echo "причина: gh api не отдал ${PATH_IN_REPO}@${BRANCH} (rc=${gh_rc})"
  [ -n "$gh_err" ] && echo "gh stderr: ${gh_err}"
  exit 3
fi

snapshot=$(printf '%s' "$content_b64" | base64 -d 2>/dev/null)

json_ok=$(printf '%s' "$snapshot" | python3 -c 'import sys,json
try:
  json.load(sys.stdin); print(1)
except Exception:
  print(0)' 2>/dev/null)

if [ "$json_ok" != "1" ]; then
  echo "ПУЛЬС НЕДОСТУПЕН, долг цикла = unmeasured"
  echo "причина: содержимое ${PATH_IN_REPO} не разобралось как JSON"
  exit 3
fi

generated_at=$(printf '%s' "$snapshot" | python3 "$HERE/pulse-read.py" field generated_at)
if [ -z "$generated_at" ]; then
  echo "ПУЛЬС НЕДОСТУПЕН, долг цикла = unmeasured"
  echo "причина: в снимке нет поля generated_at"
  exit 3
fi

now_epoch=$(date -u +%s)
gen_epoch=$(date -u -d "$generated_at" +%s 2>/dev/null)
if [ -z "$gen_epoch" ]; then
  echo "ПУЛЬС НЕДОСТУПЕН, долг цикла = unmeasured"
  echo "причина: не смог распарсить generated_at=${generated_at}"
  exit 3
fi

age_min=$(( (now_epoch - gen_epoch) / 60 ))

if [ "$raw_json_mode" -eq 1 ]; then
  printf '%s\n' "$snapshot"
  exit 0
fi

if [ "$age_min" -gt "$STALE_MINUTES" ]; then
  echo "СНИМОК ПРОТУХ, это НЕ значит что поломок нет"
  echo "возраст снимка: ${age_min} мин (порог ${STALE_MINUTES} мин), generated_at=${generated_at}"
  exit 2
fi

echo "== пульс платформы (удалённый снимок) =="
echo "возраст снимка: ${age_min} мин, generated_at=${generated_at}"
echo
echo "панель поломок:"
for key in not_ready not_ready_freshness not_ready_other domain_issues stuck_operations failed_builds; do
  arr=$(printf '%s' "$snapshot" | python3 "$HERE/pulse-read.py" field "overview.$key")
  if [ -z "$arr" ]; then
    echo "  ${key}: нет данных в снимке"
  elif [ "$key" = "not_ready_freshness" ]; then
    echo "  ${key}: ${arr}"
  else
    count=$(printf '%s' "$snapshot" | python3 "$HERE/pulse-read.py" count "overview.$key")
    if [ "$count" -eq 0 ]; then
      echo "  ${key}: пусто (проверено)"
    else
      echo "  ${key}: ${count} шт."
      printf '%s' "$snapshot" | python3 "$HERE/pulse-read.py" names "overview.$key"
    fi
  fi
done
echo
echo "counters:"
printf '%s' "$snapshot" | python3 "$HERE/pulse-read.py" counters
err_out=$(printf '%s' "$snapshot" | python3 "$HERE/pulse-read.py" errors)
n=$(echo "$err_out" | head -1)
if [ "$n" = "0" ]; then
  echo "errors: пусто (проверено)"
else
  echo "errors: ${n} шт."
  echo "$err_out" | tail -n +2
fi
