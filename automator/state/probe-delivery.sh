#!/usr/bin/env bash
# Вердикт доставки: что из кода main НЕ работает в проде прямо сейчас.
#
# Почему скрипт, а не команда по памяти (sess-0822i, две ошибки за один цикл):
#   1) `git log <tag>..origin/main` через СЛИЯНИЕ показывает дубли коммитов с
#      обеих сторон merge и рисует несуществующее отставание. Ложная тревога.
#   2) `git merge-base --is-ancestor <tag> origin/main` отвечает на ДРУГОЙ
#      вопрос — «содержится ли тег в main», а не «сколько main сверху». Ответ
#      "yes" был прочитан как «доставка полная». Ложное всё-хорошо: в тот
#      момент прод не имел ~3200 строк кода с другой стороны слияния.
# Вердикт даёт только сравнение ДЕРЕВЬЕВ по путям кода.
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 2

NS=argocd-prod
CODE_PATHS=(backend frontend helm gitops-agent build-agent gateway)

git fetch origin main -q 2>/dev/null

tags=$(kubectl get deploy -n "$NS" \
  -l app.kubernetes.io/instance=dada-cloud-console \
  -o jsonpath='{range .items[*]}{.spec.template.spec.containers[0].image}{"\n"}{end}' 2>/dev/null \
  | sed 's/.*://' | sort -u)

if [ -z "$tags" ]; then
  tags=$(for d in backend frontend gitops-agent build-agent gateway portainer-agent; do
    kubectl get deploy -n "$NS" "dada-cloud-console-$d" \
      -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null | sed 's/.*://'
    echo
  done | grep -v '^$' | sort -u)
fi

[ -z "$tags" ] && { echo "НЕ СМОГЛИ ИЗМЕРИТЬ: kubectl не отдал ни одного образа в $NS"; exit 2; }

n=$(echo "$tags" | wc -l | tr -d ' ')
if [ "$n" -ne 1 ]; then
  echo "РАЗЪЕХАЛИСЬ компоненты, теги: $(echo $tags | tr '\n' ' ')"
  echo "  (раскатка на лету или застряла — повтори через минуту)"
fi

live=$(echo "$tags" | head -1)
echo "живой тег:  $live"
echo "origin/main: $(git rev-parse --short=8 origin/main)"

if ! git cat-file -e "$live^{commit}" 2>/dev/null; then
  echo "ВЕРДИКТ: НЕ СМОГЛИ ИЗМЕРИТЬ — коммита $live нет локально (git fetch?)"
  exit 2
fi

echo "--- дельта кода живой тег -> origin/main ---"
delta=$(git diff --stat "$live" origin/main -- "${CODE_PATHS[@]}")
if [ -z "$delta" ]; then
  echo "ВЕРДИКТ: ДОСТАВЛЕНО ПОЛНОСТЬЮ (0 строк кода сверху)"
  exit 0
fi
echo "$delta"
echo "ВЕРДИКТ: ОТСТАЁТ — перечисленные файлы в проде СТАРОЙ версии"
exit 1
