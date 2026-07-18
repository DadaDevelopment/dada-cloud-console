#!/usr/bin/env bash
set -euo pipefail

fail() { echo "::error::$*"; exit 1; }

TOKEN="${INPUT_TOKEN:-}"
IMAGE="${INPUT_IMAGE:-}"
BASE_URL="${INPUT_BASE_URL:-https://console.dada-tuda.ru}"
WAIT="${INPUT_WAIT:-false}"
TIMEOUT="${INPUT_TIMEOUT:-300}"

BASE_URL="${BASE_URL%/}"

[ -n "$TOKEN" ] || fail "token input is empty. Store your dadadh_ token as a secret and pass it as 'token'."
[ -n "$IMAGE" ] || fail "image input is empty."

have_jq=0
if command -v jq >/dev/null 2>&1; then have_jq=1; fi

json_field() {
  local body="$1" field="$2"
  if [ "$have_jq" = "1" ]; then
    printf '%s' "$body" | jq -r --arg f "$field" '.[$f] // empty'
  else
    printf '%s' "$body" | tr -d '\n' | sed -n "s/.*\"$field\"[[:space:]]*:[[:space:]]*\"\{0,1\}\([^\",}]*\)\"\{0,1\}.*/\1/p" | head -n1
  fi
}

echo "Deploying $IMAGE -> $BASE_URL"

resp="$(curl -sS -w $'\n%{http_code}' -X POST "$BASE_URL/api/v1/deploy" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"image\":\"$IMAGE\"}")" || fail "request to Dada Cloud failed (network)."

code="$(printf '%s' "$resp" | tail -n1)"
body="$(printf '%s' "$resp" | sed '$d')"

if [ "$code" != "202" ] && [ "$code" != "200" ]; then
  msg="$(json_field "$body" error)"
  [ -n "$msg" ] || msg="$body"
  fail "deploy rejected (HTTP $code): $msg"
fi

op_id="$(json_field "$body" operation_id)"
[ -n "$op_id" ] || op_id="$(json_field "$body" operationId)"
[ -n "$op_id" ] || fail "deploy accepted but no operation id in response: $body"

echo "operation-id=$op_id" >> "${GITHUB_OUTPUT:-/dev/null}"
echo "::notice::Deploy queued for $IMAGE (operation $op_id)"

if [ "$WAIT" != "true" ]; then
  echo "Not waiting (wait=false). Operation $op_id is running."
  exit 0
fi

echo "Waiting up to ${TIMEOUT}s for operation $op_id to finish..."
elapsed=0
interval=5
while :; do
  presp="$(curl -sS -w $'\n%{http_code}' "$BASE_URL/api/v1/deploy/operations/$op_id" \
    -H "Authorization: Bearer $TOKEN")" || fail "status poll failed (network)."
  pcode="$(printf '%s' "$presp" | tail -n1)"
  pbody="$(printf '%s' "$presp" | sed '$d')"
  [ "$pcode" = "200" ] || fail "status poll returned HTTP $pcode: $pbody"

  terminal="$(json_field "$pbody" terminal)"
  status="$(json_field "$pbody" status)"
  ok="$(json_field "$pbody" ok)"

  if [ "$terminal" = "true" ]; then
    if [ "$ok" = "true" ]; then
      echo "::notice::Deploy succeeded (operation $op_id, status $status)"
      exit 0
    fi
    ecode="$(json_field "$pbody" error_code)"
    emsg="$(json_field "$pbody" error_message)"
    fail "deploy failed (status $status, ${ecode:-no-code}): ${emsg:-see console}"
  fi

  if [ "$elapsed" -ge "$TIMEOUT" ]; then
    fail "timed out after ${TIMEOUT}s waiting for operation $op_id (last status ${status:-unknown})."
  fi
  sleep "$interval"
  elapsed=$((elapsed + interval))
done
