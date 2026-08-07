# Рынок AI-native / insight-driven managed Postgres — конкурентная разведка

Дата: 2026-08-06. Цель: понять, кто уже даёт тенанту «платформа сама говорит,
что не так с базой» + агент с руками на базе, и где дыра, которую может занять
Dada Cloud.

## 1. Профили игроков

### Neon
- Serverless Postgres, copy-on-write branching (ветка создаётся за миллисекунды
  независимо от размера базы) — эталон UX для веток.
- Официальный MCP-сервер (`mcp-server-neon`): агент создаёт БД/ветки, гоняет
  SQL, управляет проектами на естественном языке.
- Июль 2026: в MCP добавлены read-only observability-тулы — `query_logs`
  (фильтр логов по сервису/severity/времени) — агент может расследовать сбой,
  не выходя из редактора.
- Что НЕ нашёл: собственного проактивного «advisor» с рекомендациями по
  индексам/bloat в консоли — акцент именно на branching + MCP-доступ, а не на
  insight-дашборде.
- Куплен Databricks (2026) — сигнал, что serverless-Postgres-инфра + agent
  tooling считается стратегическим активом.
- Источники: [Neon MCP guide](https://skywork.ai/skypage/en/neon-postgres-ai-engineer-guide-agentic-databases/1977632381509627904), [Changelog 2026-07-24](https://neon.com/docs/changelog/2026-07-24), [Changelog 2026-05-22](https://neon.com/docs/changelog/2026-05-22), [Neon API for AI Agents](https://api-docs.neon.tech/reference/ai-agents), [Databricks acquires Neon](https://databricks.com/company/newsroom/press-releases/databricks-agrees-acquire-neon-help-developers-deliver-ai-systems)

### Supabase
- **Security Advisor + Performance Advisor** в Studio, построены на
  открытых тулах `splinter` (линтер) и `index_advisor`. Статические
  правила-запросы к каталогу, не ML: таблицы без RLS, слишком широкие policy,
  открытые sensitive-колонки, недостающие/неиспользуемые индексы.
- **Query Performance tool** — топ медленных запросов + index advisor рядом.
- **AI Assistant в дашборде**: увидел алерт Security Advisor → можно попросить
  ассистента сгенерировать и применить RLS-политику текстовым промптом. Это
  ближе всего к «one-click remediation через чат», но именно с DDL/policy, не
  с EXPLAIN/vacuum-тюнингом.
- Advisors работают автоматически + ручной re-run кнопкой.
- Источники: [Security & Performance Advisor](https://supabase.com/blog/security-performance-advisor), [Docs: database-advisors](https://supabase.com/docs/guides/database/database-advisors), [Features page](https://supabase.com/features/security-and-performance-advisor)

### PlanetScale (Postgres + MySQL продукт)
- **Insights**: детектирует аномальную задержку запроса относительно
  ожидаемого baseline (не просто «топ медленных»), с привязкой к CPU/IOPS в
  момент аномалии. ML-эвристика отличает «плановый» медленный запрос от
  неожиданной деградации → меньше false positive, чем у тупого threshold.
- **Schema recommendations** — из телеметрии продакшн-трафика генерирует
  готовые DDL: добавить/убрать индекс, снести неиспользуемую таблицу,
  предотвратить исчерпание PK. Применяется через ветку схемы → деплой (не
  один клик в проде, а через их git-like branch workflow).
- **Boost** — ускоряет паттерн, найденный в Insights (кэш/материализация).
- Источники: [Insights feature](https://planetscale.com/features/insights), [Schema recommendations blog](https://planetscale.com/blog/introducing-schema-recommendations), [Query Insights docs (Postgres)](https://planetscale.com/docs/postgres/monitoring/query-insights)

### AWS RDS/Aurora — Performance Insights + DevOps Guru for RDS
- Performance Insights собирает DB load (Average Active Sessions) как базовую
  метрику.
- **DevOps Guru for RDS** поверх неё — ML/статистическая аномалия-детекция:
  при отклонении DB load триггерит углублённый анализ wait-events и топ-SQL,
  выдаёт рекомендацию («расследуй конкретный high-load statement» или
  «масштабируй ресурсы»). Это САМЫЙ близкий к «нарратив вместо графика» кейс
  среди больших облаков — но всё ещё рекомендация текстом, не авто-фикс.
- Требует отдельного включения (доп. стоимость), работает для Aurora и RDS
  Postgres.
- Источники: [DevOps Guru for RDS under the hood](https://aws.amazon.com/blogs/database/amazon-devops-guru-for-rds-under-the-hood/), [Working with anomalies](https://docs.aws.amazon.com/devops-guru/latest/userguide/working-with-rds.html), [AWS News Blog launch](https://aws.amazon.com/blogs/aws/new-amazon-devops-guru-for-rds-to-detect-diagnose-and-resolve-amazon-aurora-related-issues-using-ml)

### Azure Database for PostgreSQL
- **Query Performance Insight** (в разделе «Intelligent Performance» портала):
  топ ресурсоёмких/долгих запросов, тренд во времени, wait-типы, разбивка по
  calls/data-usage/IOPS/temp-файлам. Требует Query Store (пара часов на
  накопление данных).
- Ярлык «Intelligent» в названии раздела — маркетинг, по факту это
  дашборд поверх Query Store, не ML-рекомендации по индексам (в найденных
  источниках отдельного index-advisor для Postgres Flexible Server не
  обнаружено, в отличие от Azure SQL).
- Источники: [Query Performance Insight docs](https://learn.microsoft.com/en-us/azure/postgresql/flexible-server/concepts-query-performance-insight)

### GCP Cloud SQL / AlloyDB
- **Query Insights** — дашборд по запросам (аналог remaining).
- **AlloyDB Index Advisor** — встроен в Query Insights, работает БЕЗ
  включения AI-режима (то есть это классический advisor, не Gemini).
- **Gemini Cloud Assist / Database Center**: единая панель по флоту БД
  (Cloud SQL, AlloyDB, Spanner, Bigtable, Memorystore, Firestore) — чат
  на естественном языке для troubleshooting, fleet-level performance/
  inventory insights, cost-рекомендации. Premium-фича (гейтится отдельной
  Gemini Cloud Assist подпиской).
- Это ближайший аналог того, что мы хотим: связка «advisor (structured) +
  AI chat поверх того же контекста», но у Google это разнесено на
  отдельный платный слой (Database Center), а не встроено в базовую консоль
  инстанса.
- Источники: [Monitor/troubleshoot with AI (AlloyDB)](https://docs.cloud.google.com/alloydb/docs/monitor-troubleshoot-with-ai), [Index advisor + query insights](https://docs.cloud.google.com/alloydb/docs/use-index-advisor-with-query-insights), [Database Center improvements Next'26](https://cloud.google.com/blog/products/databases/database-center-improvements-from-next26), [AI-powered database agents deep dive](https://cloud.google.com/blog/products/databases/deep-dive-on-new-ai-powered-database-agents)

### OtterTune — мёртв, урок
- CMU spin-off (2020), ML-автотюнинг конфигов RDS MySQL/Postgres. Закрылся в
  2024: сорвалась сделка по продаже «postgres-focused PE фирме», команду
  распустили целиком.
- **Урок для нас**: чистый «ML autotuning как отдельный продукт» плохо
  монетизируется как standalone SaaS — клиенты не готовы платить премию за
  тюнинг конфигов отдельно от хостинга самой базы. Встроить в PaaS
  (бесплатно как часть тарифа) выгоднее, чем продавать отдельно.
- Источники: [OtterTune is Dead](https://ottertune.com/), [dbtune retro](https://www.dbtune.com/blog/ottertune)

### pganalyze — золотой стандарт standalone-инструмента
- **Index Advisor**: перебирает СОТНИ комбинаций индексов через
  what-if-анализ по ВСЕЙ рабочей нагрузке (не по одному запросу), даёт
  оценку выигрыша по cost на таблицу.
- **VACUUM Advisor**: наблюдает за autovacuum по каждой таблице, рекомендует
  персональные настройки (bloat, freeze, vacuum performance) — этого нет ни
  у одного облачного провайдера из списка выше в явном виде.
- **EXPLAIN**: собирает планы медленных запросов автоматически, подсвечивает
  плохие scan'ы и отсутствующие индексы в визуализации плана.
- Разница с облаками: pganalyze анализирует ВЕСЬ workload кластерно и явно
  считает деньги/cost-выигрыш по рекомендации, у облаков — точечные
  рекомендации по отдельным запросам без cross-query оптимизации.
- Источники: [Index Advisor](https://pganalyze.com/postgres-index-advisor), [VACUUM Advisor](https://pganalyze.com/postgres-vacuum-advisor), [Docs](https://pganalyze.com/docs)

### Xata Agent — ближе всего к нашему видению
- Open-source AI-агент (не просто advisor): "AI agent expert in PostgreSQL" —
  проактивно мониторит базу, ищет root cause проблем, предлагает тюнинг
  конфигурации. Позиционируется как замена части работы DBA.
- Xata сама — branch-native Postgres платформа (тысячи изолированных веток из
  одной прод-базы для agent-driven разработки).
- Это единственный найденный игрок, который явно продаёт связку
  «мониторинг + диагноз root cause + агент», а не просто dashboard.
- Источники: [Xata Agent](https://xata.io/database-agent), [GitHub xataio/agent](https://github.com/xataio/agent), [DBA to DB Agent blog](https://xata.io/blog/dba-to-db-agent)

### Tembo, Nile
- Tembo — по свежим данным сместился в сторону "coding agents в облаке"
  (репо/тикеты/тулы), не чисто DB-insights продукт; DB-специфика не
  подтверждена свежим поиском.
- Nile — не нашлось релевантных источников в этом заходе (нужен отдельный
  прицельный поиск, если понадобится).

### Coolify / Dokploy / CapRover — подтверждённая дыра
- Coolify: Sentinel-мониторинг + S3-бэкапы + управление командой — но это
  generic infra-мониторинг (аптайм/логи), НЕ SQL-уровня insights (нет
  index advisor, нет vacuum advisor, нет query analytics).
- Dokploy: лёгкий (0.8% CPU/350MB RAM), чистый UI, но также без специфичных
  для БД advisor-ов.
- CapRover: вообще "no database tooling, limited monitoring" — прямым
  текстом в обзоре.
- **Вывод: весь self-host PaaS сегмент (наш прямой референс по UX) не имеет
  ВООБЩЕ никакого DB-insight слоя. Это буквально наш конкурентный зазор.**
- Источники: [Coolify vs Dokploy vs CapRover](https://massivegrid.com/blog/coolify-vs-dokploy-vs-caprover-choosing-paas/), [Kloudshift comparison](https://kloudshift.net/blog/comparing-self-hostable-paas-solutions-caprover-coolify-dokploy-reviewed/)

### Timescale / EDB / Crunchy Bridge — быстрый проход
- **Crunchy Bridge Postgres Insights**: автоматизированный мониторинг
  cache hit ratio, index hits, медленных запросов + CLI-тулы. Похоже на
  Azure QPI по глубине, без ML-нарратива.
- **EDB Postgres AI**: маркетинг вокруг производительности векторного поиска
  для agentic AI (50ms при 50M векторов), это не insight-продукт про
  bloat/индексы, а про AI-workload на самой базе.
- **TimescaleDB**: акцент на compression/hypertables для time-series, не на
  advisor-слое.
- Источники: [Crunchy Bridge Database Insights](https://www.crunchydata.com/blog/introducing-database-insights-effortless-postgres-management-with-crunchy-bridge), [EDB Postgres AI benchmarks](https://www.hpcwire.com/bigdatawire/this-just-in/edb-says-postgres-ai-tops-enterprise-ai-data-platform-benchmarks/)

### "Агент с руками на базе" — прямое сравнение
| Продукт | Может ли LLM реально выполнить действие на базе (не только предложить) |
|---|---|
| Supabase AI Assistant | Да — генерирует и ПРИМЕНЯЕТ RLS-политику по описанию проблемы из Security Advisor |
| Neon MCP server | Да — агент создаёт БД/ветки, гоняет SQL, читает логи через MCP-тулы (общего назначения, не «почини индекс» из коробки) |
| Google Gemini Cloud Assist (AlloyDB/Cloud SQL) | Частично — чат объясняет и советует, зафиксированного «нажми и агент сам создал индекс» в источниках не найдено |
| Xata Agent | Да, по позиционированию — «эксперт-DBA», проактивный root-cause + тюнинг (глубина авто-действий не до конца задокументирована в открытых источниках) |
| postgres.new / database.build | Да, но это sandbox в браузере (PGlite/WASM), не прод-управление — LLM создаёт таблицы/пишет SQL по CSV/промпту в изолированной песочнице |
| pganalyze, AWS DevOps Guru, Azure QPI | Нет — только рекомендация текстом, применяет человек |

## 2. Таблица «кто что делает»

| Продукт | Что собирает (сигнал) | Что показывает | Ремедиация | AI-чат/нарратив | Где гейтится по тарифу |
|---|---|---|---|---|---|
| Neon | логи, метрики через MCP | read-only query_logs в MCP | нет авто-фикса | MCP-агент общего назначения (не DB-специфичный совет) | MCP бесплатен, cам сервис по тарифам Neon |
| Supabase | RLS/security misconfig, query perf, индексы | Security/Performance Advisor списком | Assistant применяет RLS-policy по промпту | Да, целевой чат по конкретному алерту | Advisors доступны на всех тарифах (Studio) |
| PlanetScale | query-level телеметрия всего workload | аномалии латентности + baseline, schema DDL-рекомендации | DDL применяется через branch→deploy workflow (не 1 клик в проде) | нет чата, есть готовые DDL-сниппеты | Insights/Boost — платные тарифы |
| AWS DevOps Guru for RDS | DB load (AAS), wait events, top SQL | ML-аномалии + текстовая рекомендация | нет, только рекомендация | Нет (не conversational) | отдельный платный сервис поверх RDS |
| Azure QPI | Query Store (calls/IOPS/temp) | топ-запросы, тренды, wait-типы | нет | Нет | доступно, но требует Query Store включённым |
| GCP AlloyDB/Cloud SQL | query insights + index advisor | индекс-рекомендации без AI | нет одноклика | Gemini Cloud Assist — чат по флоту БД | Gemini-фичи = отдельная премиум-подписка |
| OtterTune (мёртв) | конфиги + метрики (ML) | авто-тюнинг параметров | было полу-авто | нет | закрылся — стандалон-модель не взлетела |
| pganalyze | весь workload, EXPLAIN-планы, vacuum | Index/VACUUM Advisor, визуализация планов | рекомендации, применяет человек | нет | платный SaaS, доступен на всех Postgres |
| Xata Agent | мониторинг + root-cause анализ | диагноз проблем, советы по тюнингу | заявлено как проактивный агент | Да, "expert DBA" агент | open-source, часть Xata платформы |
| database.build / postgres.new | промпт+CSV в браузерной песочнице | генерация схем/запросов/чартов | LLM сам пишет и выполняет SQL в sandbox | Да, полностью conversational | free, sandbox не прод |
| Coolify/Dokploy/CapRover | uptime/логи (generic) | базовый мониторинг | нет | нет | н/д — DB insights ОТСУТСТВУЮТ полностью |

## 3. Дыры, которые никто не закрывает хорошо

1. **Self-host PaaS UX + prod-grade DB insights = никто не совмещает.**
   Coolify/Dokploy/CapRover (наш прямой UX-референс, куда мигрируют RU
   разработчики из Heroku/Railway) имеют НОЛЬ специфичных для БД advisor'ов.
   Managed-облака (Neon/Supabase/PlanetScale) имеют advisors, но UX другой
   (не deploy-from-git консоль). Dada Cloud может стать первым, кто даёт
   Coolify-подобный флоу деплоя + pganalyze-уровня advisor из коробки,
   бесплатно на базовом тарифе (в отличие от AWS DevOps Guru/Gemini Cloud
   Assist, которые все платные надстройки).

2. **One-click remediation почти нигде не работает по-настоящему.**
   Только Supabase Assistant применяет фикс (и то — RLS-policy, не индекс).
   Никто не делает «AI создал индекс автоматически и откатил, если стало
   хуже» — это открытая ниша: агент с правом действовать НА ветке/staging
   с автооткатом, а не молча в проде.

3. **VACUUM/bloat-советы — только у pganalyze, ни у одного облака.**
   Growth forecast + автовакуум-тюнинг под конкретный workload — специфика,
   которую крупные облака не тащат (слишком нишево для их масштаба), а
   маленький PaaS с меньшим числом баз может себе позволить считать это
   дешёво и по умолчанию.

4. **Никто не объединяет «нарратив вместо графика» + «агент с руками» +
   «встроено бесплатно в PaaS-тариф».** GCP ближе всех (Database Center +
   Gemini), но это премиум-подписка поверх и без того enterprise-облака.
   Xata Agent концептуально ближе всего, но это отдельный open-source
   инструмент, не встроенный в deploy-платформу.

5. **RU-специфика: 0 локальных конкурентов с этим набором функций.**
   (Не проверял отдельно Yandex Cloud/VK Cloud/Selectel managed Postgres в
   этом заходе — рекомендую отдельный точечный поиск, но по общему
   ощущению рынка PaaS-уровня advisors там тоже не видно.)

## 4. UX-паттерны, которые стоит украсть один-в-один

- **Supabase**: "увидел алерт → кнопка 'спроси Assistant, как это чинить' →
  Assistant генерирует конкретный DDL/policy → применяешь одним кликом."
  Прямой шаблон для нашего «под каждым findings — кнопка спроси агента».
- **PlanetScale Insights**: аномалия = отклонение от ОЖИДАЕМОГО baseline
  запроса, а не абсолютный threshold — меньше шума в алертах.
- **pganalyze Index Advisor**: what-if по ВСЕМУ workload сразу, а не по
  одному запросу — рекомендации реально netto-полезны, не конфликтуют
  друг с другом.
- **GCP Database Center**: единая fleet-level панель по всем базам
  тенанта — полезно, если у нас будет много баз на аккаунт.
- **Neon MCP**: агенту дают READ-ONLY тулы для логов отдельно от WRITE-тулов
  (создание веток/SQL) — хороший паттерн разделения прав для нашего chat-агента
  с руками, чтобы не давать DROP TABLE по умолчанию.

