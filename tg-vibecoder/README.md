# tg-vibecoder

**Live:** MCP `http://tg-vibecoder-service.agent-sandbox-prod.svc.cluster.local:8000/mcp`, агент `tg-vibecoder` в `agent-sandbox/prod` (чат обсуждений канала пока не назначен, токен бота не выписан)

Агент в комментарии телеграм-канала. Говорит про ИИ, кодинг и инструменты как
человек, который этим занимается каждый день, а не как ассистент. Свежесть
берёт из собственной базы, а не из головы: всё, что имеет версию, цену или
дату, он обязан посмотреть инструментом либо сказать «не проверял».

Собран на том же рантайме, что и `tg-agent-tools` (GTR: инструменты это строки
в `tool_manifests`, а не код), общие части вынесены в `../agentkit`.

## Что внутри

| Файл | Зачем |
|---|---|
| `server.py` | MCP-процесс: динамические инструменты из манифестов плюс статические `load_skill`, `reply_budget`, `record_reply`, `today` |
| `ops.py` | Два примитива исполнения: `http_call_v1`, `sql_query_v1` |
| `storage.py` | Схема: `tool_manifests`, `ai_news`, `tool_facts`, `channel_posts`, `reply_ledger` |
| `search_sql.py` | SQL-контракты поиска и их чистые python-двойники для офлайн-проверки |
| `news_ingest.py` | Забор RSS/Atom из `sources.py`, дедуп по отпечатку |
| `channel_ingest.py` | Забор постов самого канала через публичный превью `t.me/s/<канал>` |
| `facts.py` | Долгоживущие факты, проставленные руками с датой проверки |
| `manifests_seed.py` | Сид манифестов `news_search`, `fact_lookup`, `channel_search` |
| `agents/vibecoder/` | `core.md` плюс домены `debug`, `takes`, `news`, `banter`, `boundaries` |
| `evals/persona/` | 29 кейсов золотого набора, 17 dev / 12 holdout |
| `scripts/persona_lint.py` | Механический анти-слоп гейт |
| `scripts/bakeoff.py` | Прогон золотого набора по кандидатам-моделям |
| `k8s/` | ModelConfig победителя, кроны ingest и `apply-cronjobs.sh` |
| `scripts/sync_deploy_repo.sh` | Регенерация деплой-репозитория `DadaDevelopment/tg-vibecoder` из монорепы |

## Почему свежесть разделена надвое

`tool_facts` — то, что не протухает за неделю: архитектурные развилки,
устойчивые компромиссы, «почему так делают». Проставлено руками, у каждого
факта дата проверки.

`ai_news` — всё остальное: версии, цены, лимиты, релизы. Только из ingest,
только с датой и источником. Засеянная константа с протухшей датой это ровно
то, как агент оказывается уверенно неправ на публике.

## Прогнать эвалы

```bash
cd tg-vibecoder
cp scripts/candidates.example.json scripts/candidates.json
export ZHIPU_API_KEY=... OPENAI_API_KEY=...
python3 scripts/bakeoff.py --candidates scripts/candidates.json --split dev
```

Промпт крутится по `dev`. Решение о выкате — один прогон по `holdout` с
порогом, объявленным заранее:

```bash
python3 scripts/bakeoff.py --candidates scripts/candidates.json --split holdout --threshold 0.75
```

Проверить одну реплику руками:

```bash
echo 'бери claude 4.5, там 200к контекста' | python3 scripts/persona_lint.py
```

## Ingest

```bash
python3 news_ingest.py --dry-run
python3 channel_ingest.py --channel <slug> --pages 3 --dry-run
```

В кластере оба ходят по расписанию, манифесты в `k8s/`.

## Деплой

Сборка идёт не из монорепы: build-agent отдаёт Jenkins только корень
репозитория, `root_dir` в параметры сборки не попадает, поэтому подкаталог
монорепы контекстом сборки быть не может. Деплой-дерево регенерируется в
отдельный плоский репозиторий:

```bash
scripts/sync_deploy_repo.sh          # сухой прогон
scripts/sync_deploy_repo.sh --push   # публикация
```

Скрипт собирает дерево заново из текущей монорепы, поэтому расхождение,
которое случилось у `tg-agent-tools` (деплой-репо застрял на старом коммите),
здесь не накапливается молча.

Кроны ingest пинятся образом, который реально крутится в приложении:

```bash
k8s/apply-cronjobs.sh
```

Крон канала выключен (`suspend: true`), пока владелец не назовёт канал:
ему нужен `channel_slug` в ConfigMap `vibecoder-config`.

## Транспорт

Сервер stateless (`http_app(stateless_http=True)`): приложение крутится в двух
репликах, а сессия streamable-http живёт в памяти пода, который её открыл.
Без этого каждый второй ход падал с `Session terminated`.

`/mcp` требует `Authorization: Bearer $MCP_AUTH_TOKEN`, когда переменная
задана. Приложению по умолчанию выдаётся публичный домен, и без токена
инструменты отвечали из интернета кому угодно. Тот же токен лежит заголовком
на `RemoteMCPServer` со стороны агента.

## Молчание

Молчание это исход, а не отсутствие исхода. Агент пишет его в `reply_ledger`
как `decision=skipped`, а своим сообщением возвращает ровно `SKIP`
(`agentkit.ledger.SILENCE_SENTINEL`). Тот, кто отправляет сообщение в
телеграм, обязан проверить `ledger.is_silence(text)` и не отправлять ничего.

Слово, а не пустая строка, потому что у A2A нет пустого хода: то, чего модель
не написала сама, рантайм добирает результатом последнего вызова, и в чат
уезжает `{"ok":true,"id":3}`. Так и случилось на живом прогоне 2026-09-08.

## Чего ещё нет

- не выбран чат обсуждений и не выписан токен бота: без них агент живёт только
  через A2A, в телеграм он не пишет;
- `channel_search` пуст по той же причине: ingest канала ждёт слаг.
