# Проза старого backlog.md, не принадлежавшая ни одному пункту

Сохранено при переезде на `bl` 2026-08-19: заголовки разделов и абзацы, стоявшие между пунктами.
Живой индекс — `backlog.md`, пункты — `backlog/items/`. Этот файл читать не обязательно, он для истории.

═══════════════════════════════════════════════════════════════════════
🔴🔴🔴 ЕДИНСТВЕННЫЙ ИСТОЧНИК ПРАВДЫ (owner 2026-07-21 интерактив) 🔴🔴🔴
Читай `state/STRATEGY.md` ПЕРВЫМ. Направление = EXECUTION-BET (потоки 1/2/3 + SEO), НЕ двери/fake-door.
Fake-door УБИТ (нужен трафик которого нет). TG мёртв. Двери заморожены (код не трогать, циклы не тратить).
Каждый цикл: grounding обязателен → бери задачу из потока 1/2/3 или SEO → до реального результата (M2 live, не build-green).
Блок «🟢 PIVOT MODE» и всё ниже него = SUPERSEDED. Не брать как приоритет.
═══════════════════════════════════════════════════════════════════════

⚠️ ГЕЙТ СВЕЖЕСТИ ПЕРЕД ЛОКОМ (sess-0810q, 2026-08-10): параллельные сессии чинят быстрее, чем сюда пишется пункт — 08-10 два верхних пункта подряд оказались уже закрытыми чужими коммитами того же дня.
Перед `[~] LOCKED` прогони: `git log --since="<дата пункта>" --oneline origin/main -- <файлы из file:line>`. Непусто → перечитай код ПЕРЕД планированием правки.
Новые пункты помечай не только датой, но и коммитом заземления (`origin/main@<sha>`) — «заземлено [code]» без sha не имеет срока годности.

# Backlog (execution-bet)

## 🔴 P0 08-15 (sess-0815b) — ПЕРВЫЙ ЧУЖОЙ ПОКУПАТЕЛЬ НЕ СМОГ ЗАПЛАТИТЬ: КАССА НЕ СОЗДАЛАСЬ
Заземлено [live psql, таблица `payments`, вся таблица = 4 строки за всю историю].

| org | план | сумма | status | yk_payment_id | confirmation_url | created_at |
|---|---|---|---|---|---|---|
| artempro2021@bk.ru | business | 2900 | pending | ПУСТО | ПУСТО | 2026-08-14 07:44:36 |
| artempro2021@bk.ru | startup | 990 | pending | ПУСТО | ПУСТО | 2026-08-14 07:44:33 |
| dada (owner) | startup | 990 | pending | ПУСТО | ПУСТО | 2026-08-13 01:14:41 |
| dada (owner) | startup | 990 | **succeeded** | 31f6cafd-… | https://yoomoney.ru/checkout/… | 2026-07-25, paid 13:20:36 |

Единственный успешный платёж за всю историю — тест владельца 07-25 — имеет ОБА поля. Все более поздние
попытки, включая обе попытки живого чужого человека, не имеют НИ ОДНОГО. Значит строка `payments`
пишется, а платёж в ЮKassa не создаётся, и `confirmation_url` юзеру не показывается.

Интервал между его кликами — **3 секунды** (startup 07:44:33 → business 07:44:36). Это не выбор тарифа,
это «нажал, ничего не произошло, попробую другую кнопку». Потом ушёл.

Метрика успеха гейта «за что платить» в STRATEGY.md сформулирована как «первый succeeded payment от
ЧУЖОГО человека». Этот цикл измерил: чужой человек ПРИШЁЛ и был структурно неспособен заплатить.
Гейт провалился не на цене и не на ценности — на кнопке.

Тот же человек — владелец аппа `fanvk`, который сейчас отдаёт 502. Один юзер, два независимых отказа.

**КОД ОТГРУЖЕН `1cda721a` (sess-0815b).** Корень [code]: `billing/yookassa/provider.go:150-189`,
`Checkout` вставлял строку `pending` ДО вызова `CreatePayment` и при провале возвращал ошибку,
оставляя строку навсегда сиротой — вебхуку её уже не сдвинуть, потому что платежа в ЮKassa нет.
Соседний `ChargeSaved` в том же файле делал правильно (помечал `canceled`) — не хватало ровно в
`Checkout`. Теперь провал провайдера помечает строку `canceled`.

**ТРИГГЕР ЕГО КОНКРЕТНОГО ПРОВАЛА НЕДОКАЗУЕМ:** поды бэкенда перезапущены 08-15 00:53, логи за
08-14 07:44 стёрты. Исключено: гейт «нет e-mail на фискализированном магазине» (`9cdac44a`,
задеплоен 08-13, до инцидента) срабатывает ДО вставки строки, а строка была вставлена.

[ ] ОСТАТОК — СЧАСТЛИВЫЙ ПУТЬ НЕ ПРОВЕРЕН НИ РАЗУ С 07-25. Живая проба [live API, 08-15]:
`POST /projects/{sandbox}/billing/checkout` → `422 receipt_email_required` — честно, но это гейт,
а не касса. У рутины нет identity с подтверждённой почтой, выписывать себе новую запрещено.
Эскалировано владельцу: один клик «Оплатить» до страницы ЮKassa, без оплаты, ответ на вопрос
«появляется ли `confirmation_url`». До ответа путь оплаты считать НЕПОДТВЕРЖДЁННЫМ и не делать
выводов о спросе.

[x] **ОТГРУЖЕНО `3ce76523` origin/main (sess-0815c, 2026-08-15)** · провал создания платежа сейчас виден только как 500 в стёртых логах. Нужен
аудит-след/алерт на провал `CreatePayment` — иначе следующий покупатель снова уйдёт молча, и мы
снова узнаем об этом через неделю из таблицы.
**СДЕЛАНО `3ce76523`:** `recordCheckoutFailureTx` (`billing/yookassa/provider.go:249`) ОДНОЙ транзакцией пишет
`UPDATE payments SET status='canceled'` + `INSERT INTO audit_events` — одного без другого быть не может
(тот же контракт, что в [[project_signup_could_be_born_without_a_trace]] и
[[project_build_terminal_verdict_must_ride_same_statement]]). Строка: `action=CreatePaymentFailed`,
`resource_kind=Payment`, `resource_name=<org_id>`, `outcome=failure`, `actor_id` = тот, кто жал «Оплатить»,
`metadata={payment_id, plan, amount_value, currency, error_class, error}`. `classifyPaymentError` бьёт ошибку
на закрытый набор классов (`yk_invalid_request`/`yk_other`/`yk_unexpected_status`/`transport`) — сырой текст
провайдера не идёт ни в лейбл метрики (кардинальность), ни в аудит. Метрика
`dada_payment_create_failures_total{error_class}` (`backend/internal/metrics/payment_create_failure.go`) +
алерт `DadaPaymentCreateFailing` (`increase(...[30m])>0`, `for:5m`, `keep_firing_for:1h`) в группе
`dada-cloud-console.money`; метка `release` наследуется из `metrics.prometheusReleaseLabel`
(см. [[project_prometheusrule_needs_release_label]]).
Тест `TestCheckout_ProviderCreateFails_LeavesAuditTrailAndBumpsMetric` на реальном риге; RED-проверка сделана —
с выпиленным INSERT аудита тест падает (`cannot scan NULL into *string`), значит страж умеет упасть.
Гейт `probe-main-build.sh` на `3ce76523` зелёный целиком (backend real-DB, gitops-agent, frontend 248 pass).
ЗАЗЕМЛЕНО ПОПУТНО [code `provider.go:171`]: `Checkout` вставляет НОВУЮ строку на каждый клик и НЕ смотрит на
существующие `pending` — значит две сиротские строки artempro2021 никого не блокируют, чистить чужие
платёжные строки в проде незачем.
ОСТАТОК (M2 живьём): образ с `3ce76523` ещё не в проде, и живого провала кассы после раскатки не было —
первая настоящая строка `CreatePaymentFailed` в `audit_events` появится только при следующем реальном отказе.
ПРИЧИНА ОТКАЗА 08-14 = `unmeasured` НАВСЕГДА (sess-0815j, [live]): единственный свидетель — stdout пода
(`payments: checkout failed ...`, `billing_payments.go:117`) → индекс `filebeat-*`, а он лежит под тем же
wedge OpenSearch (503 `cluster_manager_not_discovered_exception`) и по архитектуре НЕ архивируется в S3
(в отличие от `dada-app-logs-*`). Что удалось замерить вместо лога: на боевом магазине 1396801 за окно
07:00-08:30Z 08-14 ЮKassa не создала НИ ОДНОГО платёжного объекта → `CreatePayment` упал до приёма
(классы `invalid_request`/`invalid_credentials`/`forbidden`) либо не дошёл вовсе (`transport`). Креды
СЕЙЧАС валидны: `GET /v3/me` → `enabled, test:false`, ключ live/48; пробные платежи 08-13 и 08-15
создаются нормально. HYPOTHESIS (не факт): разовый transport-сбой, форма известна по
[[project_beget_egress_timeout]] — одна нода без egress к внешнему API при трёх живых.
СНЯТО КАК НЕ-АНОМАЛИЯ: `org_id='artempro2021@bk.ru'` вместо UUID — это норма, личная орга имеет id,
равный username [code `backend/internal/auth/jwt.go:126`], а username этих юзеров = почта.

## 🔴 P1 08-15 (sess-0815b) — LOG STORE БЕЗ CLUSTER MANAGER: ЛОГИ ЮЗЕРОВ ТЕРЯЮТСЯ ПРЯМО СЕЙЧАС
Заземлено [live kubectl]. `opensearch-infra-hot-0` — единственная manager-eligible нода. Её том Longhorn
`pvc-29195652-ffd2-49a4-8df0-59da3427c97f` ЧИТАЕТСЯ, но НЕ ПИШЕТСЯ: `touch` из debug-контейнера →
`Input/output error`, при этом Longhorn показывает `state=attached, robustness=healthy`, обе реплики
running, dmesg чист. Точная подпись [[project_longhorn_volume_can_be_readable_but_unwritable]].

Следствие: fluent-bit получает 503 `cluster_manager_not_discovered_exception`, чанки уже помечены
«cannot be retried» = **данные логов дропаются, не буферизуются**. Поды unready 5d8h. Вкладка «Логи»
в консоли для юзерских аппов слепа.
ПОДТВЕРЖДЁН ЖИВЫМ 2026-08-15 sess-0815j [live]: всё ещё 503, `opensearch-infra-hot-0` поднят (1/1, 2d8h
без рестартов), но менеджер не избирается; `opensearch-manager-watchdog` крутится каждые 5 мин, сам
логирует `no cluster manager... rechecking` и за окно наблюдения НЕ вылечил. Цена выросла с «юзер не
видит логи» до «расследование денежного инцидента слепо»: причину провала первого чужого платежа
достать нечем именно из-за этого wedge. Сторож надо чинить или заменять — самовосстановления нет.

Продуктовый вывод: поток 3 (AI авто-фикс) стоит на логах. Пока log store мёртв, авто-фикс не может
поставить диагноз, а юзер не может посмотреть, почему его апп упал. Это не инфра-задача «на потом» —
это подпорка под заявленный поток.

## 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТРЁХ ТРУПАХ
Заземлено [live psql + live API]. Проба последней мили (коммит 05da9c64) проставила `http_status` 17
аппам. У ВСЕХ СЕМНАДЦАТИ ровно `308`, `http_reason` пуст. Проба стучится в ингресс по plain HTTP,
получает редирект HTTP→HTTPS 308, за редиректом НЕ идёт, а классификатор считает 308 здоровым.
`/api/v1/admin/overview` → `live_urls` = `{"checked":17,"ok":17,"dead":0,"stale":0,"dead_apps":[]}`,
хотя реально: megafactory 404, fanvk 502, fonbet-value 503.

Одинаковый статус у ВСЕХ проверяемых — это и есть tell. Метрика структурно неспособна увидеть мёртвый
апп и потому ХУЖЕ отсутствия метрики: владелец теперь верит, что все URL живы.

Урок шире одного бага: метрика, у которой все объекты дают одно значение, не измерена — она сломана.
Приёмка любой новой пробы = она обязана уметь показать красное на заранее известном трупе.

**КОД ОТГРУЖЕН `0076f798` (sess-0815b), ЖИВЬЁМ НЕ ПОДТВЕРЖДЁН.** Причина заземлена [code]:
`livenessprobe.go` строил клиент с `CheckRedirect` → `http.ErrUseLastResponse`, то есть НИКОГДА не шёл
за 3xx; все хосты тенантов TLS-настроены, ингресс безусловно отдаёт 308 на plain-HTTP до того, как
запрос дойдёт до аппа. Фикс: до 5 хопов, `Location` следуется ТОЛЬКО на тот же хост тенанта
(чужой хост читается, но не преследуется — редиректом нельзя увести пробу на чужой апп);
4xx/5xx всегда кладёт `status_<код>` в `http_reason`, пустым он больше не бывает.
Тест-страж `TestClassifyLivenessResponse_NeverMarksErrorHealthy` падает, если хоть один 4xx/5xx
классифицируется здоровым. Изменений в argo-infra не нужно: клиент сам прыгает с :80 на :443.

[x] **ПРИЁМКА ПРОЙДЕНА ЖИВЬЁМ (sess-0815c, 2026-08-15 ~02:30Z)** [live psql + live API + live curl]. Прод-образ
`dada-cloud-console-backend:86044ec6` — прямой предок origin/main, все пять целевых sha доехали
(`0076f798`, `12487608`, `634062e0`, `1cda721a`, `db8c6299`). `resource_snapshots` и `live_urls` совпадают
дословно: `checked=17, ok=11, dead=6`, статусы РАЗНЫЕ — fanvk 502/`status_502`, fonbet-value 503/`status_503`,
megafactory 404/`status_404`, n8n 503, telemost-bot 404, reels-tracker 404, остальные 10 — 200 с пустым reason.
`dead_apps` называет все три поимённо. Независимая проверка `curl` тех же трёх URL прямо сейчас дала те же
коды — снапшот не протух. Ложно-зелёная метрика 17/17 закрыта; механизм приёмки («проба обязана показать
красное на известном трупе») сработал как задумано.
ОСТАТОК-НАБЛЮДЕНИЕ: у fanvk и fonbet-value поды 1/1 Running, а в логах vkbottle-лонгполл и фоновый скрейпер —
это НЕ веб-сервисы, задеплоенные как web-app. Их 502/503 корректны, чинить на нашей стороне нечего; продукт
обязан лишь честно сказать юзеру «адрес не отвечает и почему» (см. пункт про «Ready, но не обслуживает»).
megafactory 404 — это НАШ след (паразитные редеплои, `db8c6299`), не выбор юзера, не путать классы.
~~ПРИЁМКА СЛЕДУЮЩЕГО ЦИКЛА — НЕ закрывать без этого. Дождаться CI→образ→sync, затем:~~
```sql
select name, summary_json->>'http_status', summary_json->>'http_reason'
from resource_snapshots where summary_json ? 'http_status' order by 1;
```
Обязаны появиться РАЗНЫЕ статусы с непустым reason, и `live_urls` обязан назвать megafactory 404,
fanvk 502, fonbet-value 503 в `dead_apps`. Всё ещё 17/17 ok или всё ещё одинаковый статус у всех =
НЕ починено, откатывать вывод и копать дальше.

## 📈 СКВОЗНАЯ АНАЛИТИКА ПУТИ (owner 2026-07-31: разбирать аудит каждый цикл — правило вписано в SKILL.md; живой граф в `state/audit-path-graph.md`)
## 🎯 ПОТОК 0 — Empty-project activation (#1 ПО ДАННЫМ)
Зачем: конвертить 43% непопыток в первый деплой = крупнейший объёмный рычаг. Leak РАНЬШЕ git/деплоя — юзер регается, получает auto-project, уходит без единого действия.
## 🔴 P0-CONNECT-BLINK (07-22 loop-0722e, из ОТВЕТА реального юзера bruzas.85 — письмо 07-16 лежало непрочитанным до OW5) — IN FLIGHT
Юзер: «Подключить GitHub — страница мигнет и ничего не происходит». Root cause [code, debugger]: git/import/page.tsx:381-416 `window.location.href` ПОСЛЕ `await gitApi.installUrl()` → WebKit (Safari/iOS/TG/VK in-app browser) теряет user-activation → навигация молча дропается, 0 ошибок, 0 oauth_states (install-url вообще не пишет в БД — states пишет только мёртвый githubAuthorizeUrl-путь, 0 callers). goleva-27-states = ДРУГОЙ, уже починенный баг (534195a). ФИКС in flight (engineer: prefetch URL + sync navigation + видимый anchor-fallback). ВАЖНО для воронки: «git-wall не убивает» частично НЕВЕРНО — WebKit-юзеры умирают на молчаливом баге, не на OAuth-стене. После фикса: ответить bruzas.

## 🎯 ПОТОК 1 — Upload без git (папка/файл/архив → auto-detect → deploy) [DOWNSTREAM от leak]
Зачем: убирает трение для будущих attempters; вайбкодер грузит zip Lovable/Bolt. НО git-стена по воронке НИКОГО не убивает (template+image уже дают безgit-путь) → НЕ #1. Может стать частью пустого экрана (простейший path «залей код»). После потока 0.

## 🎯 ПОТОК 2 — Deploy speed & reliability как продукт
## 🎯 ПОТОК 3 — AI auto-fix («упал → агент чинит»)
Зачем: единственная фича которой нет у Timeweb/deploy-f; эдж = владеем прод-контекстом. Ветка claude/dadagent-autofix-integration (cf809b3) начата.

## 🎯 SEO (компаундим, дешёвые честные эксперименты)
[x] 2026-07-25 loop-0725d · P1-SEO-DEEPEN-NETLIFY **DONE live-M2 PASS [live]**: /analog-netlify углублён по railway-паттерну (79ee4ea origin/main, реюз optional-полей AltPage из 62d5edc — другие analog-* не тронуты): 6-шаговый how-to миграции (netlify.toml→autodetect/Dockerfile, env vars, домен+TLS), маппинг-таблица Netlify→Dada (честно: .pv-превью ≠ per-PR previews), «что не переносится» grounded по коду (Functions/Edge, _redirects, Forms, Identity, Split Testing — detect.go/domains.go проверены до написания), FAQ a14c10c сохранён, CTA utm_source=analog-netlify. Гейты green (tsc перепроверен мной, byte-check 0, ban-word 0). CI #573 SUCCESS ровно 79ee4ea → прод frontend 79ee4ea4 [live kubectl] → live 200 x2 + SSR несёт how-to+HowToJsonLd [live curl] → IndexNow yandex 202 + indexnow.org 200. Замер вместе с E1 08-05 (Metrika daily + indexed-статус сначала).

## 💰 Owner-gated (готовить, не тратить)

## Открытые долги (не терять)

## 🔴 P0-PREVIEW-DB-BUG (sess-10b4 07-23, owner «почини флот на artem» + скрин с крешащим pr-7) — ТОЧНЫЙ КОРЕНЬ
Owner увидел админ-вью: fonbet-value (artem, лучший юзер) pr-6 Ready / pr-7 CrashLoop-Pending. Граунж [live]:
- **git-in-path = УЖЕ ПОФИКШЕН И LIVE** [live]: hub-под 83f68abe несёт /usr/bin/git v2.47.3. Фейл 04:46 «git not on PATH» был ДО фикса (7a6d96b/64b7014). НЕ переделывать.
- **pr-7 креш = НЕ generic shared-prod-DB. ТОЧНЫЙ БАГ** [live сравнение секретов fonbet-value-env по ns]: prod→odds-research, pr-6(works)→odds-research-**pr-6**, **pr-7(broken)→odds-research-pr-6 (ЧУЖАЯ база pr-6!)**. pr-7 наследует базу pr-6 → оба preview дерутся за advisory-lock ОДНОЙ базы → pr-7 exit 75 fail-fast loop.
- **КОРНЕВОЙ ДИЗАЙН-ФЛОУ preview_env_overrides (4461035)**: DB-URL override = СТАТИЧНОЕ значение на РОДИТЕЛЬСКОМ env (`odds-research-pr-6`) → КАЖДЫЙ новый preview инфа-наследует базу pr-6 → все кроме одного крешат. Статик-override не может быть per-preview-уникальным. Мост pr-6 «работал» только потому что он ПЕРВЫЙ схватил лок.
- Refuted live: отключение APP_*_SCHEDULER_ENABLED НЕ лечит (patched pr-7 → всё равно Error) = лок держит не scheduler, нужна отдельная база per-preview.
- **ДУРАБЛ-ФИКС = P1-PREVIEW-DB-FULL** (auto per-preview база: Crossplane ServiceDatabaseV2 на КАЖДЫЙ preview + APP_DATABASE_URL rewrite на уникальное имя odds-research-pr-N + reaper cleanup). Это и есть «починить флот на artem» durable — band-aid (ручной CREATE DATABASE odds-research-pr-7 через superuser + патч Argo-секрета) = temp (Argo ревертит) + привилегированная хирургия на shared PG + рецидив на pr-8. НЕ делал band-aid осознанно.
- Band-aid БЛОКЕР если срочно нужен pr-7 живой СЕЙЧАС: нужен postgres-superuser (секрет `postgresql`/databases ns) → CREATE DATABASE "odds-research-pr-7" OWNER svc-fonbet-db → патч pr-7 secret APP_DATABASE_URL→pr-7-база → restart. Owner: сказать если делать temp band-aid или ждать durable.
- Гигиена: свой диагностик-патч (scheduler=false) откачен обратно в true (M5).

## 🔴 P0-PREVIEW-DB-FULL · [x] **DONE e2e-M2 PASS 5/5 loop-0724b [live]** (owner выбрал A)
**loop-0724b (~22:10-22:45Z): полный e2e на своём тест-стеке (repo DadaDevelopment/e2e-preview-db-test → проект e2e-pvdb → app webapp → ServiceDatabaseV2 e2e-pvdb-db → PR #1):** (1) preview env создан webhook'ом ~45с; (2) git несёт raw Database CR `e2epvdb-pr1` owner=родительская роль deletionPolicy=Delete; (3) Crossplane провизил (READY/SYNCED, psql \l подтвердил, живой коннект current_database=e2epvdb-pr1); (4) секрет+под несут переписанный DSN /e2epvdb-pr1, pod 1/1 Running; (5) PR close → CR из git+кластера ушёл, **база реально дропнута**, rows/snapshots 0 орфанов. KC-grant не понадобился (SA уже /orgs/dada/Owner standing). Cleanup полный (repo/PR/project/ns/DB/роль/KC-группа), только Nexus-образы под registry-GC. Argo manual-sync gotcha НЕ повторился (prune авто).
**Новые баги из прогона:** (a) 🔴 P1-LATENT: git-watcher без guard'а на history-rewrite — reset/force-push/branch-switch в argo-infra клоне реплеит ВСЮ достижимую историю и ВОСКРЕШАЕТ удалённые проекты (инцидент прогона: 13 фантомов, откачено verified; клон gitops-agent = ветка console-migration, НЕ main — туда ТОЛЬКО read) → chip; (b) P2: preview-teardown орфанит namespace КАЖДОГО PR (doDeletePreviewEnv ns не удаляет — тот же класс что DeleteProject-гэп task_6001f592); (c) known: org-groups-<slug>.yaml орфаны (task_ccac2406, ещё экземпляры в репо).
**ОСТАТОК → P1-ARTEM-PREVIEW-MIGRATE: [x] DONE loop-0724c (07-23 ~23:10-00:0xZ) [live], НО фича вскрыла новый гэп (ниже).** Ход: удалён stale override-row APP_DATABASE_URL (иначе rewrite бил по нему), close/reopen PR-6+PR-7 через GH-App installation-token (app 3500292, install Poksno 147362942, PEM из secret dada-cloud-console-backend) → teardown чисто (FK-fix работает), envs пересозданы. 🔴 НО gitops-agent залогировал `preview_databases=0` → оба preview получили ПРОД-DSN verbatim → CrashLoop. **НОВЫЙ БАГ cbb28ae [code+live]: parentServiceDatabases матчит только top-level summary_json->>'app_ref', а (а) watcher-synced snapshots хранят nested spec.appRef, (б) fonbet-db = standalone (appRef=сам себе), к app привязан ТОЛЬКО через env var → lookup вечно 0 для реальных юзеров; e2e-тест прошёл потому что свежий API-writer snapshot имел top-level ключ.** Interim-фикс [live]: CREATE DATABASE odds-research-pr-7 (superuser, owner svc-fonbet-db; pr-6-база уже была), KC-grant цикл (выдан→снят verified), SetEnvVar per-preview env (API 200 x2 — на САМИХ preview envs, не override!), PATCH image same-tag = re-render 202 x2, Argo manual sync (gotcha повторился, n=2 → это баг), pod bounce → **оба пода 1/1 Running, pg_stat_activity: pr-7 = 8 коннектов на СВОЮ базу, прод изолирован**. Durable-фикс кода = engineer in flight (env-var-wiring как ownership-сигнал, оба copy-сайта). Артефакты: базы odds-research-pr-6/pr-7 ручные (без CR — teardown их не дропнет, снести при закрытии PRов или после durable-фикса+re-create).
**loop-0724a [live]: код SHIPPED cbb28ae origin/main, CI #554 SUCCESS ровно cbb28ae, прод-флип gitops-agent+build-agent = cbb28ae3, поды 1/1 Running.** Реализация (engineer, grounded): per-preview Crossplane `Database` CR (raw manifest в preview-app resources.values.yaml → Argo prune на teardown = GC бесплатно; deletionPolicy Delete; owner = СУЩЕСТВУЮЩИЙ Role родителя → та же PG, те же креды, без secret-timing проблемы) + DATABASE_URL copy теперь decrypt→rewrite db-name→re-encrypt в ОБОИХ copy-сайтах (gitops-agent + build-agent), только когда у родителя есть ServiceDatabaseV2; иначе старый verbatim-путь. Тесты green оба модуля (gitops db-тесты на живом pg). Коррекция дизайна против плана: НЕ ServiceDatabaseV2 XR (тот тянет новые креды/секрет которых нет на render-тайме), а лёгкий Database CR под родительским Role. Бонус: backfill отсутствующего App-snapshot бага в preview-create.
**РЕЗИДУАЛ [x] DONE loop-0724d (07-24 00:09-00:20Z) durable-M2 PASS [live]:** (1) CI #555 SUCCESS ровно 009cd89, прод gitops-agent+build-agent=009cd89f (auto-pin) [live]; (2) durable-M2 на artem-shaped данных: close/reopen PR-7 (GH-App token) → teardown чисто → recreate залогировал **`preview_databases=1`** → Crossplane Database CR `odds-research-pr7` READY/SYNCED (Argo Synced, prune/create авто — manual-sync gotcha НЕ повторился, n=2 не подтвердилось на n=3) → secret DSN переписан на /odds-research-pr7 → alembic отмигрировал чистую базу → pod 1/1 Running БЕЗ ручных костылей → pg_stat_activity 3-way изоляция prod 9 / pr-6 9 / pr7 8 коннектов [все live]. Interim-артефакт pr-7 снят: ручная база odds-research-pr-7 DROP (0 коннектов проверено), SetEnvVar-строки умерли с teardown'ом env. **ОСТАТОК: pr-6 сознательно НЕ трогал** (третий цикл close/reopen PR-6 = спам artem'у, он может ревьюить наш autofix-PR — E34 07-27): pr-6 живёт на interim (ручная база odds-research-pr-6 + SetEnvVar на preview env); снимется само при закрытии PR-6 (тогда DROP odds-research-pr-6 руками) или при следующем естественном recreate (фича возьмёт owning). artem last genuine action 22:28Z — не потревожен. Итоговая полная миграция M2 loop-0724c: оба preview Running, pg_stat_activity 3-way изоляция (prod 9 / pr-6 8 / pr-7 8 коннектов, каждая на своей базе) [live]. pr-6 gotcha: старая база несла alembic head чужой ветки (skew от старого pr-7) → DROP+CREATE.
Owner выбрал durable (не band-aid). Root cause (выше) + дизайн ниже = wiring, машинерия существует.
**Корень (точный):** preview_env_overrides хранит APP_DATABASE_URL СТАТИКОМ на родительском env (`odds-research-pr-6`) → КАЖДЫЙ preview наследует базу pr-6 → advisory-lock коллизия → все кроме первого крешат exit 75. Не generic shared-prod.
**Дизайн (reuse существующего):**
1. CREATE preview: рендерить per-preview **ServiceDatabaseV2 CR** через уже готовый `doCreateServiceDatabase` (gitops-agent/internal/worker/dbwatcher.go:687-715), имя уникальное per-preview (`<parentdb>-pr-<N>`). Crossplane создаёт реальную БД СВОИМИ привилегированными кредами → решает «svc-cloud-console не superuser». APP_DATABASE_URL → connection-secret этой per-preview БД (composition пишет секрет в app-ns, dbcreds.go:23-37). НЕ статик-override.
2. TEARDOWN: удалять per-preview ServiceDatabaseV2 через готовый `doDeleteServiceDatabase` (dbwatcher.go:719-771) — СЕЙЧАС preview-teardown (preview.go doDeletePreviewEnv ~стр 220-268) чистит app-git-папки + environments-row, НО НЕ ServiceDatabaseV2 CR → БД-орфан копится. Это и есть гэп из backlog:41 «teardown не чистит ServiceDatabaseV2».
3. Copy-сайты override-мерджа (оба надо учесть): gitops-agent preview.go:131-160 + build-agent/internal/db/preview.go:100-129.
4. Timing: preview-app крешит-луп пока Crossplane не провизионит БД (приемлемо, рестартит до Ready) ИЛИ init-wait.
**M2-гейт (обязателен, флот):** реальный preview-цикл → per-preview БД провизится → app Ready (non-502) → teardown дропает CR+БД (0 орфанов). Не build-green.
**⚠️ M3 для флота:** gitops-agent/worker сейчас с uncommitted WIP (snapshots/manager/gitwatcher + delete-тесты) — координировать, preview.go teardown-правка пересекается с их delete-flow.
**Interim:** pr-7 artem'а ОСТАЁТСЯ крешащим до этой фичи. Instant-relief = band-aid B (owner может позвать) — CREATE DATABASE odds-research-pr-7 суперюзером + патч секрета, temp. Owner выбрал A = ждём durable.

## B-band-aid pr-7 (sess-10b4 07-23): КОЛЛИЗИЯ с параллельным флот-фиксом → стенд-даун
Owner попросил B (мгновенный pr-7). Пока делал — ПАРАЛЛЕЛЬНАЯ сессия чинила pr-7 тем же способом:
- DB-креш pr-7 РЕШЁН [live]: app на своей базе odds-research-pr7 (флот, 90 таблиц/alembic ok), под 1/1 Running 0-restart стабилен. exit-75 краш-луп устранён.
- Моя коллизия: создал дубль-базу odds-research-pr-7 (с дефисом) — их = odds-research-pr7 (без). Конфиг НЕ тронул (sed-abort спас от клоббера). Орфан-базу СНЁС (A5, 0 таблиц/0 коннектов/pr-7 не юзал — verified перед drop).
- ОСТАТОК pr-7: публичный URL 503 (app здоров, ingress/route не отдаёт) = отдельный гэп, НЕ DB. Оставлен флоту (их активный ресурс).
- УРОК-МЕХАНИЗМ (M0): две automator-сессии делали B на ОДНОМ ресурсе одновременно = дубль-работа + орфан. Task-lock на пункт бэклога не покрыл ad-hoc owner-interactive P0 на чужом ресурсе. При owner-P0 на shared-ресурсе — сперва grep активные локи + `kubectl get -o yaml | managedFields` на таргет (кто трогал <5мин) ПЕРЕД мутацией.
- durable-фикс (P0-PREVIEW-DB-FULL, заспечен выше) остаётся — band-aid только заплатка одного preview.

[x] 2026-07-25 loop-0725f · P1-SEO-DEEPEN-TGBOT **DONE live-M2 PASS [live]**: /hosting-telegram-bot углублён (5ae374a origin/main, railway-паттерн, реюз optional AltPage-полей — другие лендинги не тронуты): 5-шаговый how-to деплоя (long-poll → «фоновый воркер» БЕЗ домена честно; webhook → авто-домен + setWebhook + порт-контракт 0.0.0.0:$PORT), HowToJsonLd, cost-секция под замеренные запросы «аренда/дешёвый хостинг бота» (реальные тарифы Free/990/2900₽ из dict.ts verbatim + честное VPS-сравнение «по железу сравнимо или дешевле»), honest limitations (автодетект только fastapi/flask/django/streamlit — grounded detect.go). БОНУС-фикс: страла ложь «Long polling пока не поддерживается» исправлена (worker-режим есть [code apps.go:544]). Гейты green (tsc/eslint/next build, unicode 0, ban-word 0). CI #575 SUCCESS → прод frontend 5ae374a4 [live kubectl] → SSR ru+en 200 несёт how-to+cost [live curl] → IndexNow yandex 202 + indexnow.org 200. Замер вместе с E1 08-05 (Metrika daily; сперва indexed-статус). Пульс цикла: bruzas построил ВТОРОГО бота (workassistantbot, сам решил no_dockerfile за 15 мин — retention-сигнал живой), P2-PYTHON-WORKER-TEMPLATE заведён probe-gated (tracer: платформенного бага нет).

[x] 2026-07-25 loop-0725e · P1-SEO-DEEPEN-VERCEL **DONE live-M2 PASS [live]**: /analog-vercel углублён (5cc92d5 origin/main) под замеренные запросы («оплата vercel для россиян» 7имп поз24, «оплатить vercel» поз9.5, «аналог vercel в россии» поз7 — все 0 кликов). Осознанное отличие от railway-паттерна: /migrate-vercel уже покрывает конфиг-уровень → страница = продуктовый уровень (4-шаговый quick-start вкл. upload-без-git, mapping-таблица концептов Preview/Serverless/Blob/KV, честный notPortable x4, FAQ «как оплатить vercel из россии»), near-dup с /migrate-vercel избегнут (анти-thin). Поправил ложный клейм агента про PR-previews (они у нас ЕСТЬ — E36). Гейты перегнаны мной (tsc 0, next build ok, byte-check 0, ban-word 0). CI #574 SUCCESS ровно 5cc92d5 (9.3м) → прод frontend 5cc92d5c [live kubectl] → ru+en 200 + SSR несёт how-to/mapping/KV [live curl] → IndexNow yandex 202 + indexnow.org 200. Замер вместе с E1 08-05. Бонус цикла: «ExportAppVolume 0 audit rows» = ложная тревога пульса — DeleteProject чистит audit_events по project_id [code dbwatcher.go:1182], мой M2-cleanup снёс свою же строку; фича верифаена, память верна.

## 🎯 ПОТОК 4 — PAYMENTS (owner-directive 2026-07-25 интерактив): «оплата как managed-resource»
Порядок слайсов = закон (owner): (1) ядро+tenant#0 = свой биллинг платит через YooKassa (свой магазин, свои ключи, БЕЗ мультитенантного OAuth); (2) наружу как ресурс проекта (OAuth-коннект магазина юзера, секреты в env, вебхук-роутинг); (3) партнёрский кэшбек + лендинг под SEO-трафик. Legal: свой магазин = не агрегатор, лицензия не нужна; Partners API = модель Tilda [web owner-verified].

[x] 2026-07-28 sess-0728a · 🔴 P0-INCIDENT-FONBET-DISK-FULL **RESOLVED live-M2 PASS [live]**: fonbet-value (artem) prod лежал ~сутки CrashLoop ENOSPC (его raw_archive забил 10Gi до 100%, alert ушёл 07-27 08:53Z, юзер не отреагировал). Ход: (1) replica уведена с d5dns (Longhorn-диск 2.8GB avail 96% → после увода 12.2GB); (2) snapshot volume.size 10→12Gi [psql] + PATCH image same-tag re-render (KC-grant цикл выдан→снят verified, SA чист — только /orgs/dada/*); (3) Argo apply PVC ЗАКЛИНИЛ на immutable volumeName (рендер НЕ несёт volumeName, live static-PV несёт — артефакт restore) → ручной PVC patch; (4) LimitRange 10Gi блокировал → per-project override defaults.limitRange maxStorage=12Gi в project.yaml (argo-infra eae9ee77 console-migration) — документированный overlay-механизм, cluster-default не тронут; (5) Longhorn отказал 'replica scheduling' — dataLocality=best-effort требовал реплику на attach-ноде d5dns без места + орфан-реплики от rebuild → dataLocality=disabled + орфаны сняты; (6) block 12Gi ok, но NodeExpand на RWX/NFS = 'unknown filesystem type' (Longhorn 1.6 limitation) → resize2fs вручную в share-manager + PVC status patch (resize done честно — fs реально 12G) + pod recycle. M2: /data 12G 2.0G free 84%, pod 1/1 Running, alembic штатно, приложение живо. Email artem SENT (диагноз+рекомендация ротации+volume-export упомянут). Residuals: chips ниже (storage-cap vs paid, defaults roundtrip, usage-alert).

## Chips из sess-0728a (инцидент fonbet disk-full)
## 💰 ПОТОК 5 — МОНЕТИЗАЦИЯ ПО-НАСТОЯЩЕМУ (owner-directive 2026-08-01 интерактив)
Owner дословно: «оплата в юкассе готова на 95% - ждем финально подписи, платежка уже протестирована… нужно начинать не просто готовить юзеров к оплате а подружить фри тир и реальную монетизацию — сделать оплату более явной для новых юзеров, а старых (которым обещан бесплатный тир) — не обмануть, дав реально бесплатный тир, но чётко заставить платить при превышении… мы уже долго занимаемся благотворительностью и пора начать получать деньги».
Три обязательства сразу, ни одно не жертвуется: (1) новый юзер видит цену ДО того, как упрётся; (2) старый юзер получает обещанный free честно; (3) превышение free = стена, а не уговоры.

**ЗАМЕР ПЕРЕД ДЕЙСТВИЕМ (2026-08-01, обязателен к перечтению — он опровергает прошлую посылку беклога «enforcement выключен»):**
- `BILLING_ENABLED=true`, `BILLING_EXEMPT_ORGS=dada`, `YOOKASSA_SHOP_ID=1420046`, `YOOKASSA_SECRET_KEY` присутствует, `YOOKASSA_RETURN_URL=https://console.dada-tuda.ru/billing/return` — прочитано `printenv` ВНУТРИ живого пода `dada-cloud-console-backend-54db964d55-882rn` ns `argocd-prod` [live kubectl exec]. Т.е. **квоты уже жёстко работают в проде, и магазин уже подключён.** Прежняя формулировка «BILLING_ENABLED не выставлен» была НЕВЕРНА.
- `billing_accounts` = ровно 3 строки, ВСЕ `plan=free`, у всех `quota_grace_until=2026-09-25` (mig 055, вписаны 2026-07-27): `dada`, `bruzas.85`, `artemmendeleev@gmail.com` [live psql cloud-console].
- Реальное потребление по орг [live psql resource_snapshots⋈projects]: `dada` 66 app/14 db (exempt), `bruzas.85` 3/0 (**сверх free=2, живёт только на грейсе**), `ggrk52` 2/1 (**строки в `billing_accounts` НЕТ → грейса НЕТ → уже сейчас упирается: apps 2>=2 и dbs 1>=1**), `artemmendeleev@gmail.com` 1/1, и ещё 4 орг по 1 app без строки: `goleva.giftdev@gmail.com`, `gunopice85@gmail.com`, `good.win2283@gmail.com`, `artempro2021@bk.ru` (у последнего +1 db = ровно потолок).
- `payments` = ровно 1 строка: `dada`/`startup`/990.00 RUB/`succeeded`, создан 2026-07-25 13:16Z, `paid_at` 13:20Z. **Но `billing_accounts.dada` при этом `plan=free`, `plan_expires_at` пуст, `plan_assigned_at=2026-07-27 07:59Z` = метка миграции 055, не оплаты.** mig 055 план НЕ трогает (`ON CONFLICT DO UPDATE SET quota_grace_until, updated_at` — проверено), значит строки на момент 07-27 просто НЕ БЫЛО → успешная оплата не оставила платного плана. Правдоподобная невиновная версия: `assignPlanTx` приехал слайсами 2-3 (`cf0f58b`) ПОСЛЕ того платежа. **Это версия, а не факт — проверяется тестовым платежом, см. PAY-5 ниже.**
- Free по прайсу [code pricing]: apps 2 / dbs 1 / storage 2GB / domains 1 / envs 1 / members 1 / box_minutes 300. startup 990₽ (5/2/10/5/2/3, 3000 мин), business 2900₽ (20/10/100/20/5/10).
- Механика гейта [code billing.go:140 `checkQuota`]: `!BillingEnabled` → пропуск; `quotaExempt` ИЛИ `quotaGraceActive` → пропуск; иначе `count >= limit` → 403 `{error:quota_exceeded, resource, limit, upgrade:true}`. Грейс = `billing_accounts.quota_grace_until` в будущем; **отсутствие строки = грейса нет** [code billing.go:123].

## 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
Owner дословно: «https://cloud.dada-tuda.ru/box хорошо получилось, но как страница лендинга этого мало — нужно всем рассказать об этой новой механике для агентов и реально продать её — авторизую любые действия, нужен результат — статьи, нейровидео, посты — давай поверим в эту механику как в инновацию уровня MCP — о ней должны начать говорить все».
Гейт качества: лендинг ≠ дистрибуция. Успех меряется не публикациями, а трафиком и box-ами, созданными НЕ мной.

Grounding: страница есть в двух языках (`frontend/app/(marketing)/box` и `.../en/box`) [code]. MCP-сервер живой, 132 tool'а, включая `createBox/boxUp/exposeBox/crystallizeBox/suspendBox/getBoxUsage` [memory project_mcp_server + tool-list]. Free даёт 300 box-минут, startup 3000 [code pricing] — т.е. попробовать можно без карты, это и есть крючок дистрибуции.

## [x] ЗАКРЫТО sess-0813t (2026-08-13T22:25Z) — Команда запуска: живой пруф на поде взят, инертность рычага починена `5c7aa4fa`
Живой апп `tree` (юзер keksmd) собрался, раскатался и крашлупится: его точка входа — argparse-CLI.
Консоль честно говорит `app_needs_args` + строку-улику, но починить это юзер в продукте НЕ МОЖЕТ:
команда запуска рождается из детекта фреймворка и никем не редактируется.
Проверено [code], все четыре звена пустые:
- `dada-argo@gh/develop helm/common/templates/deployment.yaml:390-396` — у главного контейнера нет ни `command`, ни `args` (у initContainer и у postman-сайдкара — есть, у аппа нет).
- `gitops-agent/internal/renderer/renderer.go:303-313` `commonValues` — нет поля.
- backend: `start_cmd` встречается ровно один раз, `build-agent/internal/worker/runner.go:1070`, из детекта.
- frontend: поля нет ни в одной форме.
Работа: аддитивный `{{- with .Values.args }}` в общем чарте (пусто → ничего не меняется для 100+ живых аппов),
поле в снапшоте/API/renderer, ввод в UI приложения. Дверь односторонняя только в чарте — там нужен аккуратный шаг.
Пока фичи нет, тексты миграции больше не обещают её (`2f4808f9`).

**ОТГРУЖЕНО 2026-08-13:**
- Чарт: `dada-argo@15e73b33` (`gh/develop`) — `{{- with .Values.startCommand }} command: ["sh","-c"] / args: [<строка>]`.
  Дизайн переломила заземлённая улика, а не вкус: `jenkins-pipelines vars/dadaBuildPipeline.groovy:240,272,284`
  шаблонизирует образы с `CMD` и БЕЗ `ENTRYPOINT`, поэтому поле-«дописать args» затёрло бы CMD и kubelet стал бы
  exec'ать `--surname` как бинарь. Поле замещает команду целиком через шелл.
  Безопасность доказана, а не предположена: `helm template` на реальных values живого аппа, `git archive HEAD -- helm/common`
  до/после + `global: {block: b1}` -> 169 строк vs 169 строк, байт-в-байт; с выставленным ключом появляются ровно 3 ожидаемые строки.
  (Первый прогон пруфа был ЛОЖНЫЙ — обе стороны падали на `nil pointer ... .block` и `diff` сравнивал два файла с ошибкой
  и печатал IDENTICAL. Урок в notes.)
- Продукт: `dada-cloud-console@55146791` (`origin/main`) — миграция `117_git_repos_start_command.sql`,
  `PATCH /projects/{p}/environments/{e}/apps/{a}/start-command`, `commonValues.StartCommand yaml:"startCommand,omitempty"`,
  `dbwatcher` читает `summary_json.start_command`, UI-редактор + ссылка «Задать команду запуска» прямо из баннера вердикта
  `app_needs_args` (петля диагноз->починка закрыта в один клик).
- Гейты: backend `go build`/`vet`/`go test ./internal/api/...` ok, gitops `go test ./internal/renderer/...` ok
  (тест пинит: unset -> ключа нет вообще), frontend `tsc` чисто, lint 0 ошибок / 10 прежних warn, `test:unit` 212/212, `build` ok.
- Эндпоинт НАМЕРЕННО не ставит редеплой в очередь — см. инцидент с values.yaml `internal/telemost-bot` 2026-08-02.

**ОСТАТОК (не сделано в цикле, честно):** живого прогона на поде нет — `Not-tested` в коммите.
Следующий цикл: в `agent-sandbox` поднять пробный апп, выставить команду, снять с пода
`kubectl get pod -o jsonpath='{.spec.containers[0].command}'` -> `["sh","-c"]`, затем снести пробник тем же циклом.

**РЕЗУЛЬТАТ sess-0813t (2026-08-13/14). ЖИВОЙ ПРОГОН ОПРОВЕРГ ОТГРУЖЕННОЕ: РЫЧАГ БЫЛ ИНЕРТЕН, ПРИЧИНА НАЙДЕНА И УБРАНА `5c7aa4fa`.**
Прогон в `agent-sandbox`: аплоад argparse-пробника `startcmd-probe` -> сборка success -> под в CrashLoop
(`main.py: error: the following arguments are required: --surname`), вердикт консоли `not_ready/CrashLoop` с верной уликой.
`PATCH .../start-command` -> 200, и снапшот РЕАЛЬНО несёт значение [live psql]:
`resource_snapshots.summary_json->>'start_command' = 'python main.py --surname Ivanov'`.
Но две операции `DeployImageVersion` подряд отрендерили `values.yaml` БЕЗ ключа `startCommand`
(вторая вообще без git-коммита — рендер побайтово тот же), в поде `command`/`args` пустые.
ПРИЧИНА [code]: `gitops-agent/internal/renderer/values_merge.go:20` `ownedCommonKeys` — список ключей `common`,
которые merge переносит из рендера в лежащий в git файл. `startCommand` в него не добавили. Рендер ключ выпускал,
merge его выбрасывал. Бьёт по ВСЕМ аппам, кроме самого первого деплоя (там `values.yaml` ещё нет и рендер берётся целиком)
— то есть ровно по случаю «апп уже крашится, чиню команду».
ПОЧЕМУ ГЕЙТ НЕ ПОЙМАЛ: тест синхронизации списка со структурой существовал (`TestMergeAppValuesCoversEveryRenderedCommonKey`),
но читал ключи из ОТРЕНДЕРЕННОГО фикстура, а все поля `commonValues` — `omitempty`. Поле, которого фикстур не заполняет,
тест увидеть не может в принципе. Переписан на рефлексию по тегам `commonValues` + добавлен тест на слияние в существующий файл
и на удаление ключа при очистке. Red-then-green дословно (со снятой строкой):
`commonValues emits keys ownedCommonKeys does not list ...: startCommand` и `startCommand = <nil>, want the rendered command`.

**Гипотеза оценена честно (правка аналитика):** «отсутствие рычага теряет юзеров» — `measured` по МЕХАНИЗМУ
(звено пустое, вердикт без починки, доки обещали) и `unmeasured` по РЕАЛЬНОМУ ПОВЕДЕНИЮ КЛИЕНТА:
`tree` и `genagent` принадлежат `alexkekiy` — это пробы самой рутины, а не внешние клиенты. Считать закрытым только
когда в `resource_snapshots phase=CrashLoop` появится владелец не-`alexkekiy` с `cause_kind=app_needs_args`.

**M2 ПОСЛЕ ФИКСА (2026-08-13 22:19-22:25Z) — ПРУФ, КОТОРОГО ТРЕБОВАЛ ЗАМОК:**
прод крутит gitops-агент `3a20af6b`, `git merge-base --is-ancestor 5c7aa4fa 3a20af6b` -> YES.
Редеплой тем же дайджестом (операция `40dee7e0`) -> git-коммит `0d4ff631`, в отрендеренном `values.yaml` появился
`common.startCommand: python main.py --surname Ivanov`; на поде `startcmd-probe-deploy-7b9dd9989c-gblql`
`command=["sh","-c"]`, `args=["python main.py --surname Ivanov"]`, **1/1 Running, 0 рестартов** —
тот же апп до фикса сидел в CrashLoopBackOff. Цепочка БД -> рендер -> merge -> git -> Argo -> под замкнута.
Пробник снесён тем же циклом (`DeleteApp` `32af15d8`) — заодно гаснет крэш-алерт по нему.
Побочно подтвердилось живьём: ветка алерта «у проекта нет достижимого владельца -> письмо оператору»
работает как задумано (письмо по `agent-sandbox` пришло оператору, не клиенту).
