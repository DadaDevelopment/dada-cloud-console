#!/usr/bin/env bash
#
# probe-promo-redeem.sh — живая проверка разделяемого промокода на проде.
#
# ЗАЧЕМ. Владелец пообещал промокод чату «Студенческий стартап» (2802 чел).
# Пока код не погашен ВЖИВУЮ, обещание не проверено: зелёные юнит-тесты
# доказывают логику, но не доставку роута, миграции и гейта авторизации.
#
# ФАЗА A (безопасная, ничего не меняет) — гоняется всегда:
#   несуществующий код -> ждём 404 promo_code_not_found.
#   Доказывает: роут жив, хендлер подключён, таблица promo_codes доехала.
#   Отсутствие таблицы даёт 500, а не 404 — полюса различимы.
#
# ФАЗА B (пишет в биллинг) — только при PROMO_APPLY=1:
#   гасит STUDSTARTUP, сверяет сдвиг plan_expires_at, гасит повторно и ждёт
#   отказ promo_already_redeemed. Печатает точный SQL отката ДО записи.
#
# Usage:  bash probe-promo-redeem.sh              # только фаза A
#         PROMO_APPLY=1 bash probe-promo-redeem.sh
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
API="https://console.dada-tuda.ru/api/v1"
SANDBOX_ORG="dada"

say() { printf '%s\n' "$*"; }
post() {
  "$DIR/dada-curl.sh" -s -o /tmp/promo-body.json -w '%{http_code}' \
    -X POST "$API/billing/promo/redeem" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    --data "$1"
}
dbq() {
  local dsn
  dsn=$(kubectl -n argocd-prod exec deploy/dada-cloud-console-backend -- printenv DATABASE_URL 2>/dev/null)
  [ -z "$dsn" ] && { say "НЕ СМОГ прочитать DSN из пода"; return 1; }
  kubectl -n databases exec postgresql-0 -c postgresql -- psql "$dsn" -At -c "$1"
}

TOKEN=$(bash "$DIR/get-mcp-token.sh") || { say "токен не выписался"; exit 1; }

say "=== ФАЗА A: несуществующий код (ничего не меняет)"
code=$(post '{"code":"NO-SUCH-CODE-PROBE"}')
body=$(cat /tmp/promo-body.json)
say "http=$code body=$body"
# Вердикт обязан опираться на СИГНАТУРУ В ТЕЛЕ, а не на голый статус.
# Первый прогон 2026-08-21 дал http=404 с телом "404 page not found" -- это
# gin «роута нет», то есть НЕдоставка, а скрипт напечатал OK. Улика должна
# содержать признак вердикта, иначе полюса неразличимы.
if [ "$code" = "404" ] && [[ "$body" == *promo_code_not_found* ]]; then
  say "OK  роут и таблица доехали (хендлер ответил promo_code_not_found)"
elif [ "$code" = "404" ]; then
  say "КРАСНЫЙ  404 БЕЗ promo_code_not_found -- это gin «роута нет», образ старый"
elif [ "$code" = "500" ]; then
  say "КРАСНЫЙ  500 -- вероятно миграция 136 не применена, таблицы promo_codes нет"
elif [ "$code" = "401" ] || [ "$code" = "403" ]; then
  say "КРАСНЫЙ  гейт авторизации отверг сервисный аккаунт"
else
  say "КРАСНЫЙ  неожиданный статус $code -- разбирать по телу"
fi

if [ "${PROMO_APPLY:-0}" != "1" ]; then
  say ""
  say "ФАЗА B пропущена. Она ПИШЕТ в billing_accounts орги $SANDBOX_ORG."
  say "Запуск: PROMO_APPLY=1 bash $0"
  exit 0
fi

say ""
say "=== ФАЗА B: реальное погашение STUDSTARTUP"
before=$(dbq "select plan || '|' || coalesce(plan_expires_at::text,'NULL') from billing_accounts where org_id='$SANDBOX_ORG'")
say "ДО:  $before"
say "ОТКАТ (сохрани перед записью):"
say "  update billing_accounts set plan='${before%%|*}', plan_expires_at='${before##*|}' where org_id='$SANDBOX_ORG';"
say "  delete from promo_redemptions where org_id='$SANDBOX_ORG' and code='STUDSTARTUP';"

code=$(post '{"code":"STUDSTARTUP"}')
say "первое погашение: http=$code body=$(cat /tmp/promo-body.json)"
after=$(dbq "select plan || '|' || coalesce(plan_expires_at::text,'NULL') from billing_accounts where org_id='$SANDBOX_ORG'")
say "ПОСЛЕ: $after"
[ "$before" = "$after" ] && say "КРАСНЫЙ  строка биллинга не сдвинулась -- грант не применился" \
                         || say "OK  срок сдвинулся"

code2=$(post '{"code":"STUDSTARTUP"}')
body2=$(cat /tmp/promo-body.json)
say "повтор: http=$code2 body=$body2"
case "$body2" in
  *promo_already_redeemed*) say "OK  повтор отвергнут по коду, а не по тексту" ;;
  *) say "КРАСНЫЙ  повтор НЕ отвергнут -- одна орга может выесть лимит" ;;
esac
