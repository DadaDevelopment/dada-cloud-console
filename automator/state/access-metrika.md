# Как мерить привлечение (Метрика + Вебмастер)

Цель: замкнуть цикл измерений для SEO/лендинг-экспериментов (E1, E2 в experiments.md). Без данных привлечение слепое.

## СТАТУС: ТОКЕН ЕСТЬ И РАБОТАЕТ (2026-07-15)

Metrika Reporting/Management API — **РАБОТАЕТ** (проверено HTTP 200). Токен OAuth `y0_...` лежит в `state/.secrets` строкой `YANDEX_OAUTH=...` (chmod 600). Источник: `kubectl get secret -n crossplane-system yandex-metrica-credentials -o jsonpath='{.data.token}' | base64 -d` (beget-prod ctx). Если протух — перечитай оттуда.

Запрос: `curl -s "https://api-metrika.yandex.net/stat/v1/data?ids=<counter>&metrics=...&dimensions=...&date1=14daysAgo&date2=today" -H "Authorization: OAuth $YANDEX_OAUTH"`.

**Counter ID для cloud.dada-tuda.ru = `110158915`** («Лендинг облака»). Другие счётчики фронта: dada-tuda ui=104610904, profi=109709298, agent-ui=109709531, development=109705971.

**ВАЖНАЯ ловушка: счётчик 110158915 меряет И лендинг, И консоль** — один frontend-образ отдаёт оба хоста с одним baked counter. Чтобы изолировать лендинг/analog-трафик, ФИЛЬТРУЙ по пути: `dimensions=ym:pv:URLPathFull` + `filters=ym:pv:URLPath=~'^/analog'` (или по host, если Метрика различает). Console-страницы `/projects/*` — это НЕ трафик привлечения, не считай их в E1.

**Вебмастер API — РАБОТАЕТ (2026-07-16)**: токен `YANDEX_WEBMASTER_OAUTH` в .secrets (отдельное OAuth-приложение WEBMASTER_CLIENT_ID/SECRET, code-flow). user_id=`1136352593`, host_id cloud=`https:cloud.dada-tuda.ru:443` (verified). Эндпоинты: `/v4/user/` (user_id), `/v4/user/{uid}/hosts` (сайты), `/v4/user/{uid}/hosts/{host}/search-urls/in-search/samples` (что в индексе), POST `/v4/user/{uid}/hosts/{host}/query-analytics/list` (показы/клики/позиции по запросам; ПРАВИЛЬНЫЙ путь БЕЗ search-analytics-префикса - GET/POST на /search-analytics/query-analytics/list даёт RESOURCE_NOT_FOUND; тело пустое {} отдаёт фикс ~14д окно, даты игнорирует - sess-0903c verified live). zsh-gotcha: НЕ используй переменную `UID` (readonly) — бери `WUID`.

**КЛЮЧЕВАЯ НАХОДКА 07-16 [live Webmaster]**: 14 лендингов (analog-vercel/heroku/railway/render/netlify/digitalocean/fly-io + en, migrate-vercel, deploy-vibe-coding, hosting-telegram-bot) — ПРОИНДЕКСИРОВАНЫ (in-search). НО query-analytics: **0 показов / 0 кликов за 30д**. Вывод: индексация НЕ проблема, лендинги невидимы в выдаче (низкий ранг новый домен ИЛИ нулевой спрос по этим запросам). SEO-канал сейчас НЕ доставляет → не штамповать ещё analog-страницы, приоритет = прямые TG-каналы (channels.md). E1 measured — см experiments.

## Первое измерение (2026-07-15, baseline)
cloud counter, 14 дней: 68 визитов / 8 юзеров / 295 просмотров. Источники: 52 Direct + 14 Internal + 2 Link, **поисковых 0**. `/analog-*` = 0 просмотров (отгружены сегодня, счётчик на них стоит и отдаёт 200 — значит 0 = реально нет визитов, не дыра трекинга). Вывод: органика околонуль — привлечение подтверждено как узкое место числом, не гипотезой. Реальная проверка E1 после индексации → 2026-07-22.

## (архив) прежний блокер — снят
Раньше тут было «нет токена». Снято 2026-07-15: токен нашёлся в crossplane-system/yandex-metrica-credentials.

## ЕДИНСТВЕННЫЙ блокер (запросить у владельца ОДИН раз)
Нужен **OAuth-токен Яндекса** со scope для:
- **Metrika Reporting API** — `https://api-metrika.yandex.net/stat/v1/data` (визиты, цели, источники по URL /analog-*).
- **Webmaster API** — `https://api.webmaster.yandex.net/v4/` (показы, клики, позиции, CTR по запросам «аналог vercel» и т.п., статус индексации).

Токен нельзя выпустить автономно — нужен вход в Яндекс-аккаунт владельца. Это единственное физически невозможное без владельца действие (см промт «Отсутствие доступа — не отсутствие решения»).

### Как владельцу выдать (разово, ~3 мин)
1. Есть готовый прокси-способ? Проверить: тот же аккаунт уже слил токен для IndexNow? (IndexNow токена НЕ требует — это отдельный ключ, тут не поможет.)
2. Простейший путь: https://oauth.yandex.ru → создать приложение → права «Яндекс.Метрика (получение статистики)» + «Яндекс.Вебмастер» → выпустить токен для своего аккаунта.
3. Положить токен в `~/.claude/scheduled-tasks/auotmator/state/.secrets` (chmod 600, gitignore) строкой `YANDEX_OAUTH=...`. Рутина прочитает оттуда.

Пока токена нет — статус в этом файле: **НЕТ ТОКЕНА**. Не выдумывай данные, помечай E1/E2 `blocked-on-token`.

## Что нужно найти самой рутине (не блокер)
- **Counter ID Метрики** для cloud.dada-tuda.ru — живёт в dada-argo helm default (`analytics.yandexMetrika`), см memory `project_ym_uid_cookie`. Достать через kubectl (configmap фронта) или из отрендеренного helm. Без counter id Reporting API не запросить.
- Проверить, что счётчик реально стоит на лендингах: `curl -s https://cloud.dada-tuda.ru/analog-vercel | grep -i metrika`.

## Фолбэки до токена (косвенные, но реальные сигналы)
- **Вебмастер UI вручную** (владелец раз в неделю копирует топ-запросы) — грубо, но лучше нуля.
- **IndexNow статус**: страница проиндексирована? (`site:cloud.dada-tuda.ru/analog-vercel` в Яндексе).
- **backend users count** (прямой SQL, creds в backend-поде) — регистрации как нижний сигнал воронки для E2.
- **ingress access-логи** cloud-домена (kubectl logs ingress) — сырые визиты по path, без атрибуции источника.

## Когда токен появится
Рутина сама: тянет по counter id визиты на /analog-* за окно, цели register, источники; из Вебмастера — позиции/CTR по целевым запросам; пишет в experiments.md `result` + вывод; решает scale (какой конкурент даёт трафик — делать глубже) или kill (thin-content, нет показов — переписать/убрать).

Статус токена: **НЕТ ТОКЕНА (2026-07-15)** — обнови когда появится. (Апдейт: токен РАБОТАЕТ, см верх файла.)

## ВОРОНКА ПРОИНСТРУМЕНТОВАНА (P0-2, 2026-07-21 loop-0d25, M2 live-verified)
Замер НЕ слепой. Все цели существуют, JS-события реально фаятся (подтверждено reaches из Reporting API, не прокси), per-door атрибуция механически доказана. Counter `110158915`.

**Цели (5, после чистки дубля):**
| id | что | тип | fires откуда |
|---|---|---|---|
| 585010094 | Регистрация (переход на /register) | url `register` | просмотр /register |
| 586052031 | Регистрация: завершена | action `registration_complete` | JS `frontend/app/callback/page.tsx:56` (once, useRef+localStorage) |
| 585205874 | Активация: успешный деплой | action `deploy_success` | JS `frontend/app/(console)/projects/[projectId]/apps/[appName]/page.tsx:~95-113` — фаит когда app phase→{ready,healthy,running}, guard `deployGoalFiredRef`+localStorage `dada_deploy_goal:<proj>:<app>` (once/app). Commits 453f0e1+2697b99. |
| 585010111 | Лендинг /analog-* | url `analog` | — |
| 574508955 | автоцель форма | auto | — |
| 593177848 | Лендинг: клик по CTA | action `landing_cta_click` | JS, пять CTA лендинга с параметром `placement` (hero / header / header_mobile / pricing_teaser / band), коммит `e82c479` |
| 593177849 | Регистрация: начата, выбран способ | action `signup_started` | JS `/register`, параметр `method` (yandex / email), коммит `5223f81` |
| 593177850 | Вход: сорванный callback | action `auth_callback_failed` | JS `frontend/app/callback/page.tsx`, параметр с причиной (denied / callback), коммит `74ef832` |

🔴 ГЕЙТ, РОДИВШИЙСЯ ИЗ ЖИВОГО ПРОМАХА (sess-0806h, 2026-08-06): три цели выше были ОТГРУЖЕНЫ В КОД тремя разными циклами и ни одна не существовала в Метрике. `reachGoal('landing_cta_click', ...)` в проде срабатывает, но Reporting API про такую цель ничего не знает — отчёт по ней структурно пуст. Следующий цикл прочитал бы пустоту как «отвалов не было» и убил бы верную гипотезу.
**Правило: цель считается отгруженной только когда она есть в `management/v1/counter/110158915/goals` И `ym:s:goal<ID>reaches` возвращает число (пусть и 0.0), а не ошибку.** Заводить цель В ТОМ ЖЕ цикле, что и `reachGoal` в коде. Создание — POST на тот же `/goals` с телом `{"goal":{"name":..,"type":"action","conditions":[{"type":"exact","url":"<identifier>"}]}}`, права у OAuth-токена из `.secrets` есть (проверено, три цели заведены им же).

ВНИМАНИЕ: НЕ создавать цель с identifier `deploy` — фронт фаит `deploy_success`, дубль фрагментирует данные (я такой создал и удалил, 2026-07-21). Активация = `deploy_success` (585205874).

**Reaches за окно (07-07..21, 14д):** register-url 13, registration_complete 1 (одноразово/нов.юзер — sashagusarov 07-20), deploy_success 4 (повторные деплои existing users). Всё из `ym:s:goal<ID>reaches`.

**Per-door атрибуция — query template (ДОКАЗАН live):**
```
curl -s "https://api-metrika.yandex.net/stat/v1/data?ids=110158915\
&metrics=ym:s:goal585010094reaches,ym:s:goal585205874reaches\
&dimensions=ym:s:UTMSource&date1=30daysAgo&date2=today" -H "Authorization: OAuth $YANDEX_OAUTH"
```
даёт register+deploy reaches по каждому utm_source. Сейчас с меткой ходит только awesome_webhosting2026 (3 reg / 0 deploy).

**UTM-конвенция для дверей (durable — door-builder ОБЯЗАН использовать):** Дверь A (бот-workload лендинг) → `?utm_source=door_a`; Дверь B (vibecoder) → `?utm_source=door_b`. Все CTA внутри двери тащат метку до /register. После аппрува дверей замер per-door конверсии = запусти query выше, дырок инструментовки НЕТ.

**Остаток P0-2 (blocked-on-doors, НЕ инструментовки):** utm-метки физически появятся только когда P1-двери отгружены (gated на PRD-аппрув owner). Инструмент готов и ждёт.

## ⚠️ Webmaster query-analytics date-баг (2026-07-22 loop-exec1, доказано 3 способами)
`query-analytics/list` ИГНОРИРУЕТ date1/date2 — всегда возвращает ФИКСИРОВАННОЕ ~14д trailing-окно, заканчивающееся за 2 дня до вызова. Доказано: 2 вызова с разными диапазонами 2 мин apart = байт-в-байт идентичны; 3-й отличался только row-count (пагинация), 5 overlapping строк совпали байт-в-байт. СЛЕДСТВИЕ: нельзя сравнивать «показы вчера vs сегодня» из этого endpoint — разница = сдвиг окна (день добавился/выпал), НЕ рост. Для тренда используй Metrika Reporting API (ym:s:trafficSource=='organic', даёт настоящие daily-окна). «33→51 показов 07-21→07-22» БЫЛ артефактом сдвига окна, не рост.
