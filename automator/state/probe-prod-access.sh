#!/usr/bin/env bash
# Гейт достижимости прода С МАШИНЫ ЦИКЛА.
# Задача — не чинить сеть, а не дать циклу тихо записать долги как "нет данных".
# Зелёный = замеры цикла (psql-долги, kubectl-пульс, админ-панель) ПРОВОДИМЫ.
# Красный = любой замер, который эти каналы не прошёл, обязан быть помечен
# `unmeasured` (не "ноль"), и это надо написать вслух в cycle-log.
set -u

RED=0
ok()   { printf 'OK   %s\n' "$1"; }
dead() { printf 'DEAD %s -- %s\n' "$1" "$2"; RED=1; }

K8S_HOST="${K8S_HOST:-83.222.27.62}"
K8S_PORT="${K8S_PORT:-26443}"
CONSOLE="${CONSOLE:-https://console.dada-tuda.ru/}"

# 1. k8s API. Авторитет — реальный ответ apiserver, не TCP-коннект:
#    IP умеет принять SYN и замолчать (см. память github:443).
if out=$(timeout 25 kubectl get --raw='/readyz' 2>&1); then
  ok "k8s apiserver ${K8S_HOST}:${K8S_PORT} (/readyz=${out})"
else
  dead "k8s apiserver ${K8S_HOST}:${K8S_PORT}" "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-160)"
fi

# 2. psql прод-консоли. Ходим через под, поэтому это отдельный канал от п.1:
#    apiserver может отвечать, а exec в под -- нет. DSN берём из того же секрета,
#    что читает бэкенд, чтобы гейт не разъезжался с продом.
DSN=$(timeout 25 kubectl get secret -n argocd-prod dada-cloud-console-backend \
        -o jsonpath='{.data.DATABASE_URL}' 2>/dev/null | base64 -d 2>/dev/null)
if [ -z "$DSN" ]; then
  dead "psql cloud-console" "не смог прочитать DATABASE_URL из секрета argocd-prod/dada-cloud-console-backend"
elif out=$(timeout 45 kubectl exec -n databases postgresql-0 -c postgresql -- \
        psql "$DSN" -tAc 'select 1' 2>&1); then
  case "$out" in
    *1*) ok "psql cloud-console (exec postgresql-0)" ;;
    *)   dead "psql cloud-console" "неожиданный ответ: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-160)" ;;
  esac
else
  dead "psql cloud-console" "$(printf '%s' "$out" | tr '\n' ' ' | cut -c1-160)"
fi

# 3. Консоль снаружи. 000 = мёртвый путь с этой машины; любой HTTP-код = путь жив.
code=$(timeout 20 curl -s -o /dev/null -w '%{http_code}' "$CONSOLE" 2>/dev/null || echo 000)
if [ "$code" = "000" ]; then
  dead "console $CONSOLE" "http=000 (нет сетевого пути с этой машины)"
else
  ok "console $CONSOLE (http=$code)"
fi

echo
if [ "$RED" -eq 0 ]; then
  echo "ВЕРДИКТ: ЗЕЛЁНЫЙ -- замеры цикла проводимы, долги закрывать числами."
else
  echo "ВЕРДИКТ: КРАСНЫЙ -- прод НЕдостижим с этой машины."
  echo "  Прод при этом может быть жив: сверь ./probe-external.sh (check-host.net)."
  echo "  Все замеры этого цикла через мёртвый канал пиши как unmeasured, НЕ как ноль,"
  echo "  и вынеси строку про слепоту в cycle-log.md."
fi
exit "$RED"
