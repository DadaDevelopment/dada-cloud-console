# Гипотезы DADA Cloud

Живой реестр. v2 — merge двух backfill-проходов (critic-1: сессия 2026-08-12; critic-2: исторический backfill май–август).
Правила: гипотеза без теста и kill-критерия — мнение. Стратегические решения фаундера маркируются отдельно и не притворяются customer-гипотезами. Internal dogfooding ≠ customer validation.
Статусы: `supported` / `strongly-supported` / `plausible` / `untested` / `blocked` / `killed` / `superseded` / `strategic-call` / `bet`.

---

## 1. Факты (не гипотезы)

### Продукт
- F1 (май): рабочий GitOps control-plane loop: UI → Backend → DB → Worker → Git desired state → ArgoCD → K8s CRD. Реальный ServiceDatabase в проде.
- F2 (июнь): customer-facing позиционирование — «backend из GitHub в прод: сборка, БД, HTTPS/домен, без DevOps, из РФ за рубли». ICP: solo founders / малые команды / агентства.
- F3: продукт шире фронтенда: долгоживущие workloads, managed Postgres, Redis, домены/HTTPS, VM, git-driven deploy, logs/metrics, позже AI-примитивы.
- F4 (июль): Bring Your VPS построен на ~60–70%: manual VM connect, VM runtime, Compose deploy from Git, compose/.env editor, live container state, logs, rollback-примитивы, operation queue/RBAC. Не хватало discovery/import UX.
- F5: сознательное решение НЕ строить Coolify/Portainer feature parity (см. killed-список в backlog.md).

### Трекшн (июльский срез + август)
- ~10 projects; 3–4 реальных внешних юзера; 1 production tenant с 6 Ready apps.
- ≥3 регистрации с нулём деплоев — **главная улика activation cliff**.
- Первые платежи были; recurring нет.
- Органика при открытой двери: ~5 сайнапов/нед (боты, стартапы, сайты). Источники не размечены.
- Регистрация закрыта: нет оплаты (ждём регулятора) + абуз free VM фармерами.
- Churned-юзер (2 бота, 2 недели, запрос на удаление): автофикс открыл PR — не принят; 1 касание (2026-08-11) без ответа.
- Автофикс: внутренние мержи есть, внешних 0. Box: 3 774 мин, 100% internal → **untested, не falsified**.

### Из automator state (2026-08-12, авторитетные psql/Метрика-замеры)
- Воронка инструментирована: 187 uniques/30д на лендинге → **−83% до клика «регистрация»**; verifyEmail съедает **27%** сайнапов; терминальное действие умирающих — «TriggerBuild → тишина» и «ViewApps».
- Активация ~45–54%; retention-раскол без исключений: **все вернувшиеся деплоили СВОЙ код, все template-юзеры ушли за день**.
- Платёжный тракт построен e2e (тест-магазин, 30-дневные планы), BILLING_ENABLED=false, боевой магазин ЮKassa ждёт ключей. Внешних платежей — 0 за всю историю.
- Живые юзеры поимённо: artem (3 аппа+2 БД), ggrk52 (2+1 БД), artempro2021; bruzas (4 аппа, 15 билдов/сутки) самоудалился 08-10 после 4 дней CrashLoop.
- Owner-решения (institutional memory, отсутствовавшая в обоих backfill): 07-21 — bot-identity отвергнута («лобовая Amvera»), fake-doors убиты, TG мёртв, MCP признан слабым, позиционирование = **«облако, которое запускает и само чинит твой апп»**, три потока: upload-без-git / скорость+надёжность / auto-fix; 07-30 — письма юзерам запрещены (канал = сам продукт); SEO «аналог X» near-dead, из pSEO выжил только `/hosting-telegram-bot`.

### Superseded assumptions (хранить как уроки, не как факты)
- ~~«Onreza = frontend-only»~~ — SUPERSEDED 2026-08-12: у них serverless (128MB/30s), KV, Postgres с ветвлением, SSR/Bun, PR previews, интеграции GitHub/GitVerse/SourceCraft. По их маркетингу; capability не проверена руками (→ NOW-3 в backlog).
- ~~«Dada выигрывает, потому что у Onreza нет backend»~~ — falsified как формулировка; вопрос сузился до H05.
- «Пользователи Onreza — потенциальные backend-клиенты Dada» — не факт, гипотеза при H05.
- Onreza pull-сигналы: TG пуст, форум — один энтузиаст с 0 ответов. Их «500+ деплоев» не верифицированы. Pull не виден ни у кого на RU-рынке.

---

## 2. Positioning tree (историческое, не схлопывать)

- **P1 — RU Backend PaaS**: «у меня backend, не хочу становиться DevOps». Ближе всех к реальным пользователям.
- **P2 — Full application cloud**: backend+DB+VM+storage в одном control plane. Риск: вырождается в feature checklist.
- **P3 — Bring Your VPS / migration**: «не мигрируй — подключи сервер, мы поймём workload и станем control plane над ним». Другой GTM-клин; структурно совместим с P4/P5.
- **P4 — Agent-operated cloud**: CLI, MCP, machine-readable operations, автофикс, Box.
- **P5 — Agent-native infrastructure**: примитивы, спроектированные для агентов. Company-level гипотеза, недоказана.

### Архитектуры бизнеса (открытый стратегический вопрос — НЕ решать эссе)
- A: RU-PaaS core, AI-native — фича.
- B: RU-PaaS — входной клин, agent-native — destination (PaaS = distribution для будущего продукта).
- C: control plane — ядро; managed apps / imported VPS / VM / agent / Box — адаптеры одного плана управления (→ H13).

Главный вопрос: **какой job даёт первый repeatable pull, и является ли единый control plane экономически полезным мостом от human-operated к agent-operated?** Решается данными из воронки, не документом.

---

## 3. Гипотезы

### H01 — Deployment pain exists (Problem)
Solo founders / малые backend-команды в РФ испытывают достаточную боль от ручного VPS-деплоя, чтобы выбрать managed-платформу.
За: внешние юзеры были, прод-workloads были, первые платежи были. Против: абсолютные числа малы; часть сайнапов не дошла до деплоя.
**Тест:** воронка после открытия дверей. **Статус:** `supported`, not validated at scale.

### H02 — First-deploy cliff (Activation) ★
Главное ограничение — между signup и успешным первым деплоем, а не отсутствие ещё одного примитива.
Улики: ≥3 zero-deploy аккаунта при работающей платформе.
**Статус:** `strongly-supported` + **воронка уже инструментирована automator'ом** (activation-funnel-v2.sql, Метрика+audit). Известные утечки в порядке величины: (1) лендинг→клик регистрации −83%; (2) verifyEmail −27% сайнапов (owner-политика, ждёт решения); (3) пост-деплойная тишина («TriggerBuild → тишина» как терминальное действие). Работа = чинить эти три + forensics zero-audit когорты (6 юзеров, стабильна 8 проходов). **Метрика цели:** median time-to-first-deploy ≤10 мин, first-deploy success ≥60%.
**Evidence 2026-08-14 (E141, sess-0814i):** один класс мёртвого сигнапа оказался неизмеримым by design — провал автосоздания дефолтного проекта не оставлял следа ни на бэкенде (три пути отказа `EnsureDefaultProject` отвечали 500/400 без аудита), ни на фронте (голый `catch{}` рисовал пустой обзор). За 30 дней ровно 1 сигнап без проекта (`f93095a3-...`, 2026-07-16) — причина потеряна навсегда. Отгружено `74f3b518`: аудит `outcome=failure` с машинным reason + экран с ретраем. Число появится не раньше 09-14; сама цифра «сколько мёртвых сигнапов = наш отказ, а не bounce» до сих пор НЕ измерена.
**Evidence 2026-08-13 (E97, sess-0813g/h):** за 12 чистых дней (ферма 08-08/09/10 вырезана) 57 человек видели лендинг → 3 открыли `/register` → **1 нажал CTA**. Утечка №1 внутри H02 — не форма входа Keycloak (39 юзеров, вторая по объёму) и не пост-деплойная тишина, а **лендинг→клик**: 1.8% против прежней оценки −83%. Замер сделан при намеренно закрытой регистрации, то есть верхняя часть воронки мерилась при нулевом знаменателе; с 2026-08-13 `SIGNUP_ENABLED=true` [live kubectl configmap `dada-cloud-console-config`], знаменатель вернулся — перемерить на живой реге.

**Evidence 2026-08-13 (E115, sess-0813i):** перемерили на открытой реге — знаменателя всё ещё нет. Путь незнакомца пройден живьём целиком (лендинг → `/register` → `passport.yandex.ru`, обрывов нет, мобильный CTA не спрятан). Metrika 110158915 за сутки 08-13: **9 уникальных посетителей на весь домен**, 4 на `/`, 1 на `/register` (проверяющий), 3 на `/login`, `/callback` после открытия двери — 0. Три независимых замера того же дня (атрибуция регистраций `72cfa3eb`, смертность перелёта на GitHub `4cab562b`, намерение deploy-CTA `86dc87f2`) — все с доставленным кодом и **нулевым знаменателем**. Гипотеза «хиро обещает GitHub → упор в `block-new-users`» ОПРОВЕРГНУТА: `github` IdP `enabled:false` целиком, экран недостижим [live kcadm]. H02 не двигается замерами, пока к двери не идут люди.

**Evidence 2026-08-14 (E133/E128, автоматор):** первая живая регистрация после реопена — `michaelharlam@yandex.ru`, 08-13 12:33Z. Атрибуция сработала (`signup_source='direct'`, 1/1=100%, `users` и `audit_events` совпали побайтово тем же стейтментом) — механизм H02-инструмента подтверждён на N=1, распределение каналов пока `unmeasured` (нужно ≥5-10). GitHub-перелёт за те же сутки — 0 внешних `StartGitAppInstall` (5 строк за 30д все внутренние: agent-sandbox зонд + владелец лично), значит стена GitHub как причина обрыва H02 остаётся `unmeasured`, не подтверждена и не опровергнута. Один внешний юзер дошёл до регистрации и — судя по нулю его в `StartGitAppInstall`/`git_repos` — до git-connect не дошёл вовсе; куда именно он делся между `/register` и деплоем, следующему циклу разбирать поимённо.

**Evidence 2026-08-14 (E142, sess-0814j):** класс «канонический `ssl: true` ложится на проверке hostname» взведён на три четверти, но НЕ закрыт. Отгружены все три недостающих конца: строфа `hostAliases` в общем workload-чарте (`dada-argo@develop` `8b7043f3`), заполнение `PgRouterHostAliasIP` дефолтом из env в рендерере gitops-agent (`e9a126fa`, намеренно мимо `dbwatcher.go` с чужим WIP), проброс `PG_ROUTER_CLUSTER_IP=10.96.139.238` (`argo-infra@console-migration` `87f82c4d`, подтверждён мной в живом деплойменте). Заземление отмело два более дешёвых пути: CoreDNS живёт в `beget-coredns`, Application без `spec.source` и с меткой `toleration.beget.com/ignore: true` — правка эскалируется владельцу/Beget; зона `pv.dada-tuda.ru.` в PowerDNS `kind: Native`, публично делегирована с wildcard `*.pv → 155.212.223.198`, так что указать `db.pv` на ClusterIP значит сломать всех внешних клиентов. Флаг `MANAGED_DB_TLS_DSN_ENABLED` остаётся выключенным: прод-образ gitops-agent на момент записи `b84fa872`, кода в проде нет, hostAliases ни в одном поде нет. Статус класса — по-прежнему ОТКРЫТ для следующего юзера с каноничным сниппетом; вердикт по гипотезе не двигаю.

### H03 — RU sovereignty wedge (Value/moat)
Для части RU-разработчиков локальная оплата и независимость от foreign SaaS — причина выбора платформы.
**Тест:** attribution: источник + 1 вопрос при сайнапе («почему мы?») + упоминание в интервью первых платящих. **Kill:** из 20 разговоров РФ-фактор ни разу не решающий. **Статус:** `plausible`, needs attribution.

### H04 — Full-stack breadth wins
Ширина (runtime+DB+VM+storage) существенно повышает willingness to choose против узких платформ.
Раньше считалась фактом; после Onreza — нет.
**Тест:** win/loss упоминания ширины как решающего фактора. **Статус:** `untested`.

### H05 — Long-running workload gap
Существует значимый RU-сегмент, которому нужны arbitrary long-running processes / Docker / Compose, хуже обслуживаемые serverless-центричными платформами.
**Тест, шаг 1 (capability):** матрица по Onreza/Amvera/Timeweb руками, не по лендингам: Docker-образ, python/Go long-running, worker, cron, websocket, Compose, DB, cache, sleep/cold-start, квоты, цены, GitVerse/SourceCraft, previews. **Шаг 2 (demand):** конверсия/retention сегмента (боты/воркеры vs сайты).
**Owner-решение 07-21 (constraint):** bot-hosting как ИДЕНТИЧНОСТЬ отвергнута («загон в нишу, лобовая Amvera»). Боты — дверь (единственный выживший pSEO — `/hosting-telegram-bot`), не позиционирование. Бывшая H-боты сведена к сегмент-замеру внутри H05, без права переписывать hero под ботов.
**Исходы:** gap есть → клин «долгоживущие процессы любого стека + Postgres рядом, за рубли»; gap нет → дифференциация = VM/BYO + агентский слой. **Статус:** `untested`, срочная.

### H06 — Existing-infra migration wedge (BYVPS) — REOPENED
Для владельца существующего VPS/Compose барьер входа существенно ниже при «подключи сервер, ничего не мигрируй» (discover → import → review → commit → deploy), чем при «пересоздай app у нас».
Построено ~60–70% (F4). Заброшена без customer validation — единственная гипотеза с job + ICP + отличимым оффером + sunk cost одновременно.
**Тест (дешёвый, acquisition-эксперимент):** два оффера одному трафику — «задеплой из GitHub» vs «подключи свой VPS, импортируем Compose» — сравнить CTR/сайнап/активацию. Плюс вопрос в zero-deploy forensics: «у тебя уже был VPS?»
**Kill:** оффер BYVPS стабильно хуже greenfield на том же трафике. **Статус:** `untested, partially built`.

### H07 — GitHub dependency is strategic risk — STRATEGIC CALL (Alex, 2026-08-12)
Не customer-гипотеза. РФ-продукт не должен иметь GitHub единственной SCM-зависимостью (сценарий бана как в Китае). Следствие: честные OAuth GitVerse + SourceCraft — committed backlog, generic git — дополнение, не замена.
**Учебный чекпойнт (не гейт):** замерить usage интеграций через 60 дней после релиза — для калибровки будущих strategic calls, не для отмены этого. **Статус:** `strategic-call`.

### H08 — Agent becomes primary operator (Market/Behaviour)
Значимая доля workflows перейдёт от «человек деплоит через UI» к «coding agent деплоит, наблюдает, чинит».
Prerequisite всей agent-native ветки. Internal dogfooding не считается.
**Ранние индикаторы:** агентные вызовы CLI/MCP внешними юзерами; доля agent-led деплоев. **Статус:** `bet`, unvalidated customer demand.

### H09 — Current clouds are bad agent substrates
Проблема agent-разработчиков не в отсутствии MCP, а в несоответствии interaction model облаков автономному lifecycle агента. (Замена слогану «designed for AI, not adapted» — слоган не является problem statement.)
**Тест:** problem-интервью с agent-heavy разработчиками: конкретные обломы их агентов об облако. **Статус:** `untested`.

### H10 — Box solves a distinct job
Разработчики будут использовать/платить за crystallizable execution environment, потому что агенту нужно собственное тело/состояние.
**Статус:** `untested` (не falsified). Externalization — по триггеру (см. backlog LATER). Известные блокеры: изоляция, abuse (урок фармеров), crystallize e2e не доказан (ADR-019).

### H11 — AI-generated apps create deployment demand

**Evidence 2026-08-13 (E116, sess-0813i):** тракт upload-без-git доказан живьём на проде, не сборкой: статика (Dockerfile) **222с** от загрузки до внешнего HTTP 200 на 6/6 узлах, Flask без Dockerfile (detect по `requirements.txt`) **205с**, 6/6. Билд 70-80с, деплой-фаза после билда 135-145с — самый дорогой отрезок. Детектор реальный (docker/next/vite/react/node/fastapi/flask/django/streamlit/python/maven/gradle/go), лимит 100MB. Разрыв обещания: «папка» и «одиночный файл» в коде отсутствуют — принимается только `.zip/.tar.gz/.tgz` (`frontend/components/deploy/upload-deploy.tsx:9-15,65-69`), то есть экспорт Lovable/Bolt/v0 распакованной папкой юзер обязан зазиповать сам.
Пользователи Lovable/Bolt/Lork после первой версии упираются в production backend/state/operations, которую закрывает Dada.
**Апгрейд из automator (07-21):** поток «upload-без-git» (папка/zip → auto-detect → deploy) строится ровно под этот job («экспорт Lovable/Bolt = zip, git не нужен») и является генерализацией единственного измеренно работающего активационного механизма (template-escape: 3/6 незнакомцев, git/OAuth-стена убивает ~100%). Плюс retention-раскол: возвращаются только деплоившие СВОЙ код — вайбкодер со своим сгенерённым кодом = правильный профиль.
**Тест:** конверсия/retention когорты upload-без-git после релиза потока 1. **Статус:** `plausible` (поднята с untested), первое косвенное evidence есть.
**Evidence 2026-08-13 (sess-0813f) — прямое, впервые:**
1. *Путь физически работает целиком* [live, прогон в `agent-sandbox`]: zip с `package.json` без Dockerfile → детект `vite:5173` → сборка 96с → апп `Ready` 2/2 → внешняя проверка с 6 нод отдала 200 с ожидаемым маркером. **zip → живой URL = ~3м30с, ни одного обрыва.**
2. *Путь используется и конвертит реальными людьми* [live psql `audit_events`]: `UploadSourceArchive` = 22 строки / 4 актора / 22 success / 0 failure. Из них ДВА внешних юзера (`artempro2021@bk.ru` → `fanvk`, `good.win2283@gmail.com` → `oxygen`) довели архив до живого `Ready`-аппа; `fanvk` — 14 успешных сборок 07-29→08-12. Медиана upload→первый успешный билд ≈ 2м43с.
3. *Контраст с git-дверью* [live psql]: конверсия `ViewApps → CreateApp/ConnectGitRepo` = 2.6% (4/156), четыре прохода подряд; `ViewApps` терминален у 33% юзеров.
Знаменатель мал (2 внешних юзера), поэтому статус остаётся `plausible`, а не `supported`. Но соотношение «2/2 доходят через upload против ~100% смертности на OAuth-стене» — первое прямое, а не косвенное, evidence. Продуктовое следствие уже отгружено: дефолт чузера деплоя для проекта без git-аппов переведён на upload (E93).

### H12 — AI Gateway as wedge
Архитектура развита (LiteLLM, BYOK, metering), но evidence, что это GTM-клин — нет. Не смешивать с основным позиционированием.
**Статус:** `bet`, demand evidence unknown. Отдельное решение о бюджете.

### H13 — Control-plane continuity (unifying candidate)
Ценность Dada — единый application control plane: сначала управляет Dada-managed workload или существующим VPS, затем становится естественным API для автономных агентов. Объясняет GitOps/VM/BYVPS/Compose/CLI/MCP/Box.
**Предупреждение критика: гипотеза, объясняющая все прошлые решения, неотличима от рационализации, пока не даёт различающих предсказаний.** Фальсифицируемые следствия:
1. imported-VPS юзеры со временем конвертятся в managed-ресурсы (funnel: import → attach managed DB/domain);
2. один control plane снижает churn против связки точечных инструментов;
3. агенты/CLI предпочитают единый API поверх обоих runtime (agent-led операции на imported и managed через одну поверхность).
Ни одно пока не наблюдалось. **Не hero. Не market truth.** **Статус:** `untested`, кандидат в ядро при сигнале из H06+H08.

---

## 4. Killed / superseded гипотезы

- **H-комьюнити-прослушка Onreza** — killed 2026-08-12: живого комьюнити нет. Джоба отменена.
- **Fake doors новые** — отклонено: ~20 дверей уже есть, проблема в разметке.
- **Паника «проиграть Onreza»** — снята: pull не виден ни у кого; проигрыш = оба не нашли спрос.
- **«RU-PaaS vs AI-native» как бинарный выбор** — superseded: преждевременная дихотомия; см. архитектуры A/B/C и H13.
- **Habr-корпблог как канал** — killed по цене (~268k₽/3мес); триггер возврата: MRR ≥ инфра-счёта.
