#!/usr/bin/env bash
# Гейт пульса: собирается ли origin/main ИЗ ЧИСТОГО ДЕРЕВА.
#
# Родился 2026-08-06: коммит 45f3bbbb закоммитил ссылку на обработчик
# (router.go -> h.AIRecordFailure), а сам файл обработчика остался
# незакоммиченным в рабочем дереве автора. У автора всё собиралось, у main —
# нет, и так простояло 85 минут: ни один образ консоли собраться не мог.
# Локально такое не ловится в принципе — ловит только сборка из чистого дерева.
#
# Запускать каждый цикл вместе с пульсом. Красный выход = P0, ничего другого
# не берём.
#
# Использование: state/probe-main-build.sh [путь-к-репо]
set -uo pipefail

REPO="${1:-/opt/data/projects/dada-cloud-console}"
WT="$(mktemp -d)/wt-main"
rc=0
envbroke=0

# 2026-08-12: гейт отдал MAIN-BROKEN на ЗЕЛЁНОМ main — у локальной машины
# кончился диск (595Mi свободно, go-build кеш 35G), линкер падал на
# `strip: No space left on device`, и это читалось как «main не собирается».
# Красный от своей машины ≠ красный main: такое обязано называть свою природу.
# 2026-08-15: два цикла подряд гейт не дал ВООБЩЕ никакого вердикта — ни
# зелёного, ни красного, — потому что упирался в этот порог и печатал совет
# «запусти go clean -cache» человеку, которого рядом нет. Совет в тексте не
# механизм: чистим кеш сами и перепроверяем, и только потом сдаёмся.
# Порог снижен под текущую машину: сборки идут в docker-контейнере
# (tar-поток в удалённый демон), локально нужен только worktree (~200 МБ),
# а не 5 GiB под локальный тулчейн и go-кеш, как было на macOS-машине.
avail_kb=$(df -k "$HOME" | awk 'NR==2{print $4}')
if [ "${avail_kb:-0}" -lt 1048576 ]; then
  echo "PROBE-ENV-BROKEN: на локальной машине $((avail_kb/1024)) MiB свободно (<1 GiB), worktree не собрать."
  echo "  Это НЕ поломка main. Освободить место и перезапустить гейт."
  exit 2
fi

# note_env_failure помечает прогон как сломанный ОКРУЖЕНИЕМ, а не кодом:
# ENOSPC линкера/компилятора — свойство машины гейта, и выдавать его за
# вердикт по origin/main значит рожать ложный P0.
note_env_failure() {
  case "$1" in
    *"No space left on device"*|*"no space left on device"*) envbroke=1 ;;
    *"SQLSTATE 3D000"*|*"does not exist (SQLSTATE 3D000)"*) envbroke=1 ;;
    *"SQLSTATE 42P07"*|*"SQLSTATE 42701"*) envbroke=1 ;;
  esac
}

cleanup() {
  git -C "$REPO" worktree remove --force "$WT" >/dev/null 2>&1
  rm -rf "$(dirname "$WT")"
}
trap cleanup EXIT

"$(dirname "$0")"/pin-github-ips.sh || echo "NOTE пиннинг адресов github не удался, пробую как есть"
git -C "$REPO" fetch origin --quiet || { echo "FETCH-FAILED (git до github)"; exit 2; }
git -C "$REPO" worktree add --detach --quiet "$WT" origin/main || { echo "WORKTREE-FAILED"; exit 2; }

echo "origin/main = $(git -C "$WT" log -1 --format='%h %s')"

# На ops-VM нет локального go - сборка в контейнере golang:1.25 (тулинг CI).
# Рабочее дерево модуля уезжает в контейнер tar-потоком: демон тут удалённый,
# bind-монты не видят локальные пути. Кеши go держим в именованных volumes.
run_mod_build() {
  local mod="$1"
  local dir="$WT/$mod"
  tar -C "$dir" -cf - . | docker run --rm -i \
    -e GOFLAGS=-mod=mod \
    -v dada-go-mod-cache:/go/pkg/mod \
    -v dada-go-build-cache:/root/.cache/go-build \
    -e "GOMODCACHE=/go/pkg/mod" \
    golang:1.25 sh -c "mkdir -p /work && tar -xf - -C /work && cd /work && go build ./... 2>&1"
}
run_mod_test() {
  local mod="$1"
  local dir="$WT"
  tar -C "$dir" -cf - . | docker run --rm -i \
    -e GOFLAGS=-mod=mod \
    -v dada-go-mod-cache:/go/pkg/mod \
    -v dada-go-build-cache:/root/.cache/go-build \
    -e "GOMODCACHE=/go/pkg/mod" \
    golang:1.25 sh -c "mkdir -p /work && tar -xf - -C /work && cd /work/$1 && go test ./... -count=1 2>&1"
}
run_mod_vet() {
  local mod="$1"
  local dir="$WT/$mod"
  tar -C "$dir" -cf - . | docker run --rm -i \
    -e GOFLAGS=-mod=mod \
    -v dada-go-mod-cache:/go/pkg/mod \
    -v dada-go-build-cache:/root/.cache/go-build \
    -e "GOMODCACHE=/go/pkg/mod" \
    golang:1.25 sh -c "mkdir -p /work && tar -xf - -C /work && cd /work && go vet ./... 2>&1"
}

for mod in backend build-agent gitops-agent; do
  [ -d "$WT/$mod" ] || continue
  if out=$(run_mod_build "$mod"); then
    echo "OK   go build $mod"
  else
    echo "FAIL go build $mod"
    echo "$out" | head -20
    note_env_failure "$out"
    rc=1
  fi
  if out=$(run_mod_vet "$mod"); then
    echo "OK   go vet $mod (тестовые файлы компилируются)"
  else
    echo "FAIL go vet $mod"
    echo "$out" | head -20
    note_env_failure "$out"
    rc=1
  fi
done

# CI гоняет `go test`, а этот гейт до 2026-08-07 гонял только сборку — билд #971
# лежал красным на упавшем ТЕСТЕ, и гейт рутины его пропускал как зелёный.
# gitops-agent прогоняется целиком (около 15с, внешних зависимостей нет).
# backend гоняется ТОЛЬКО против локального рига на 127.0.0.1:55432 (никогда
# против общей базы, см memory tests_share_prod_db_cleanup). Без рига real-DB
# тесты молча скипаются, и гейт зелёный на красном main: билд #1050 упал на
# шести тестах internal/api, которые линкуют выдуманные репозитории, а новый
# probe github.com честно отвечал 404 — гейт этого не видел вовсе.
# ВНИМАНИЕ: локальный прогон идёт не под root, а Jenkins — под root; тест,
# который отличает эти два случая (права на файлы), здесь всё равно зелёный.
if [ -d "$WT/gitops-agent" ]; then
  if out=$(run_mod_test gitops-agent "gitops-agent"); then
    echo "OK   go test gitops-agent"
  else
    echo "FAIL go test gitops-agent"
    echo "$out" | grep -v '"level"' | tail -20
    note_env_failure "$out"
    rc=1
  fi
fi

if [ -d "$WT/backend" ]; then
  if pg_isready -h 127.0.0.1 -p 55432 >/dev/null 2>&1; then
    # 2026-08-16: риг ответил на pg_isready, но базы `console2` в нём не было —
    # КАЖДЫЙ backend-тест падал `database "console2" does not exist (SQLSTATE
    # 3D000)`, и гейт напечатал MAIN-BROKEN на зелёном main (go build/vet,
    # gitops-agent и 284 фронт-теста прошли). Имя базы — свойство машины гейта,
    # а не origin/main, поэтому оно ищется, а не забивается гвоздём.
    rigdb=""
    for cand in console2 console; do
      if psql -h 127.0.0.1 -p 55432 -U postgres -d "$cand" -tAc 'select 1' >/dev/null 2>&1; then
        rigdb="$cand"
        break
      fi
    done
    if [ -z "$rigdb" ]; then
      echo "PROBE-ENV-BROKEN: риг на 127.0.0.1:55432 жив, но в нём нет ни базы console2, ни console."
      echo "  Это НЕ поломка main: backend-тесты упали бы 'SQLSTATE 3D000' на любом коммите."
      echo "  Создать базу (createdb -h 127.0.0.1 -p 55432 -U postgres console2) и перезапустить гейт."
      exit 2
    fi
    [ "$rigdb" = console2 ] || echo "NOTE риг использует базу '$rigdb' (console2 отсутствует)"
    export TEST_DATABASE_URL="postgres://postgres@127.0.0.1:55432/$rigdb?sslmode=disable"
    # Риг живёт вечно и НЕ мигрируется сам: каждая новая миграция делает гейт
    # красным навсегда, а падение выглядит как поломка main (`SQLSTATE 42P01
    # relation ... does not exist`). 2026-08-15: миграция 128 так держала гейт
    # красным, хотя в проде таблица уже была. Накатываем перед тестами.
    # 2026-08-16 (sess-0816e): риг заведён дампом, а не накатом — в
    # `schema_migrations` лежали 25 версий при живой схеме из 129, поэтому накат
    # умирал на 026 (`already exists`) и НИ ОДНА новая миграция в риг не
    # попадала. Это четвёртый по счёту ложный MAIN-BROKEN, рождённый локальной
    # машиной, поэтому дрейф чинится, а не комментируется: версия, упавшая
    # ИМЕННО из-за «объект уже есть», отмечается применённой и накат повторяется.
    # Отмечается только такая версия — любая другая ошибка выходит из цикла и
    # разбирается ниже как настоящая.
    migok=0
    for _ in $(seq 1 200); do
      if mig=$(cd "$WT/backend" && go run ./cmd/migrate 2>&1); then
        migok=1
        break
      fi
      echo "$mig" | grep -qE 'already exists|42P07|42701|42710|42P06|42P16' || break
      drift=$(echo "$mig" | grep -o 'apply migration [0-9A-Za-z_]*\.sql' | head -1 | sed 's/apply migration //;s/\.sql//')
      [ -n "$drift" ] || break
      psql -h 127.0.0.1 -p 55432 -U postgres -d "$rigdb" \
        -c "INSERT INTO schema_migrations(version) VALUES ('$drift') ON CONFLICT DO NOTHING" >/dev/null 2>&1 || break
      echo "NOTE риг: $drift уже был в схеме, отмечен применённым (дрейф РИГА, не main)"
    done
    if [ "$migok" = 1 ]; then
      echo "OK   миграции рига ($(echo "$mig" | tail -1))"
    else
      # 2026-08-16: риг заведён мимо `schema_migrations`, поэтому накат падает
      # `already exists (SQLSTATE 42P07)` на миграции 026 — объект в базе есть,
      # просто не отмечен как применённый. Это дрейф РИГА: он не делает main
      # красным (тесты ниже гоняются по уже правильной схеме и проходят), но
      # до этой правки печатал MAIN-BROKEN поверх зелёного прогона.
      note_env_failure "$mig"
      if [ $envbroke -eq 1 ]; then
        echo "NOTE миграции рига 55432/$rigdb не накатились: объекты уже есть, а schema_migrations о них не знает (дрейф РИГА, не main)"
        echo "$mig" | tail -3
        envbroke=0
      else
        echo "FAIL миграции рига 55432/$rigdb — тесты ниже будут врать про main"
        echo "$mig" | tail -5
        rc=1
      fi
    fi
    if out=$(cd "$WT/backend" && go test ./... -count=1 2>&1); then
      echo "OK   go test backend (real-DB риг 55432/$rigdb)"
    else
      echo "FAIL go test backend"
      echo "$out" | grep -v '"level"' | grep -E '^(---|FAIL|ok +[^ ]+ +\[|# )|\.go:[0-9]+:' | head -30
      note_env_failure "$out"
      rc=1
    fi
    unset TEST_DATABASE_URL
  else
    echo "SKIP go test backend (нет локального рига на 127.0.0.1:55432 — см memory reference_local_realdb_test_rig; БЕЗ него real-DB тесты молча скипаются и гейт врёт)"
  fi
fi

if [ -d "$WT/frontend" ]; then
  node_major=$(node -e 'process.stdout.write(process.versions.node.split(".")[0])' 2>/dev/null || echo 0)
  if [ ! -d "$REPO/frontend/node_modules" ]; then
    echo "SKIP frontend test:unit (нет $REPO/frontend/node_modules, npm ci дорог для пульса)"
  elif [ "$node_major" -lt 22 ]; then
    echo "SKIP frontend test:unit (node $node_major, --experimental-strip-types требует >=22)"
  else
    ln -s "$REPO/frontend/node_modules" "$WT/frontend/node_modules"
    echo "NOTE frontend test:unit берёт node_modules рабочего дерева, код — из origin/main"
    if out=$(cd "$WT/frontend" && npm run test:unit 2>&1); then
      echo "OK   frontend test:unit ($(echo "$out" | grep -E '^# pass' | tr -d '\n'))"
    else
      echo "FAIL frontend test:unit"
      echo "$out" | grep -E '^not ok|^# (tests|pass|fail) |^ *(error|expected|actual|code):' | head -20
      rc=1
    fi
  fi
fi

if [ $rc -eq 0 ]; then
  echo "MAIN-BUILDS"
elif [ $envbroke -eq 1 ]; then
  echo "PROBE-ENV-BROKEN: прогон упал на свойстве МАШИНЫ ГЕЙТА (нет места на диске либо нет базы рига), не на коде main."
  echo "  Починить окружение (go clean -cache / createdb) и перезапустить; вердикта по main этот прогон НЕ даёт."
  exit 2
else
  echo "MAIN-BROKEN"
fi
exit $rc
