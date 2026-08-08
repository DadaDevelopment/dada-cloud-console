# Рецепты MCP: разобранные сценарии

## Зачем это нужно

Точные последовательности вызовов за тем, что люди действительно просят агента сделать в DADA
Cloud: выкатить образ, подключить репозиторий, получить песочницу с базой внутри, разобраться с
падающим приложением. В каждом рецепте есть фраза, которую можно сказать ассистенту, и вызовы,
которые он должен сделать, — чтобы вы видели, делает он правильное или импровизирует.

Сначала настройка: [управление DADA Cloud из AI-агента (MCP)](mcp-ai-agents.md).
Полные списки аргументов: [справочник инструментов MCP](mcp-tool-reference.md).

## Два вызова, с которых начинается всё

Почти каждый рецепт ниже открывается одинаково, потому что `projectId` и `envId` — это UUID, и
больше их взять негде:

```
listProjects                    → выбрать проект, взять его id
getProject(projectId)           → взять id окружения
```

Агент, который это пропустит и передаст slug проекта, увиденный в URL, получит 404 на каждом
следующем вызове. Если ваш так делает, скажите ему: *«id проектов — это UUID, сперва вызови
listProjects»*.

## Рецепт: выкатить контейнерный образ

> «Выкати `ghcr.io/acme/api:1.4.0` в мой проект как `api` на порт 8080, с `LOG_LEVEL=info` и
> секретным `DATABASE_URL`.»

```
listProjects
getProject(projectId)
createApp(projectId, envId, name="api",
          image="ghcr.io/acme/api:1.4.0", port=8080)   → id операции
getOperation(projectId, operationId)                   → опрашивать до Committed
setEnvVar(projectId, envId, appName="api",
          key="LOG_LEVEL", value="info")
setEnvVar(projectId, envId, appName="api",
          key="DATABASE_URL", value="…", is_secret=true)
listApps(projectId, envId)                             → опрашивать до фазы Healthy
```

Здесь на практике ломаются две вещи. **Committed — это не Healthy**: агент, остановившийся на
операции, доложит об успешном деплое, пока контейнер крутится в crash-loop. И **инструмента
перезапуска нет**, потому что перезапускать нечего: изменения переменных и образа примиряются
сами.

Если фаза так и не становится Healthy, идите в рецепт диагностики.

## Рецепт: выкатить новую версию того, что уже работает

> «Переведи `api` на `1.4.1`.»

```
listProjects
getProject(projectId)
updateAppImage(projectId, envId, appName="api",
               image="ghcr.io/acme/api:1.4.1")         → id операции
getOperation(projectId, operationId)                   → опрашивать до Committed
listApps(projectId, envId)                             → опрашивать до фазы Healthy
```

## Рецепт: деплой из репозитория GitHub

> «Подключи `acme/api` и собери его.»

```
listProjects
getProject(projectId)
listGitInstallations(projectId)
```

Если список пуст, агент **не сможет** это починить: установка GitHub App — человеческое действие
на экране согласия самого GitHub. Он должен дать вам ссылку:

```
getGitInstallUrl(projectId, provider="github")   → откройте сами, потом продолжайте
```

Когда установка на месте:

```
listInstallationRepos(projectId, installationId)
connectGitRepo(projectId, envId, installation_id=…,
               repo_full_name="acme/api",
               production_branch="main", auto_deploy=true,
               port=8080)
triggerBuild(projectId, envId, appName="api")    → возвращает сборку, а не операцию
getBuild(projectId, buildId)                     → опрашивать именно её
listApps(projectId, envId)                       → опрашивать до фазы Healthy
```

Обратите внимание на асимметрию: `triggerBuild` императивен и отдаёт **id сборки**, поэтому
`getOperation` для наблюдения за ней не подходит. Всё остальное в этом документе наблюдается через
`getOperation`.

Подробнее: [деплой приложения из GitHub](applications-deploy-from-github.md).

## Рецепт: добавить управляемый Postgres

> «Дай `api` базу и подключи его к ней.»

```
listProjects
getProject(projectId)
createDatabase(projectId, envId, name="api-db", app_ref="api",
               backup_enabled=true)              → id операции
getOperation(projectId, operationId)             → опрашивать до Committed
listDatabases(projectId, envId)                  → опрашивать, пока фаза не станет готовой
getDatabaseCredentials(projectId, envId,
                       name="api-db", reveal=true)
setEnvVar(projectId, envId, appName="api",
          key="DATABASE_URL", value="…", is_secret=true)
```

`getDatabaseCredentials` отдаёт **404, пока база ещё создаётся** — это «ещё нет», а не «не
найдено». Агент, для которого 404 фатален, сдастся на один опрос раньше времени; скажите ему
сначала опрашивать `listDatabases`.

Каждое раскрытие пишется в аудит, так что ждите эти вызовы в своём аудит-логе с личностью агента —
то есть вашей.

Подробнее: [управляемые базы Postgres](databases-postgres.md).

## Рецепт: получить песочницу для работы

Этот поток агент должен выбирать, когда ему нужно место, где что-то *запускать*, а не куда
деплоить.

> «Дай мне box с Postgres и открой порт 3000.»

```
getBoxCatalog                                    → настоящие имена образов и профилей
listProjects
boxUp(projectId, name="scratch", image=…, profile=…,
      ttl_seconds=…, spend_cap_rub=…,
      ssh_public_key="ssh-ed25519 AAAA…")
```

`boxUp` синхронный и возвращается только после того, как внутри box действительно выполнилась
команда. В ответе — координаты подключения, одноразовый токен `dadabox_` и готовый к вставке
сниппет `mcpServers`.

**Добавьте этот сниппет в свой клиент вторым MCP-сервером.** В этом весь замысел: команды
выполняет собственный эндпоинт box, поэтому ни ваш код, ни ваши модельные ключи не идут через наш
control plane. Инструмента «выполни команду» на этом сервере нет и не будет.

Дальше:

```
createDatabase(projectId, envId, name="db")        → управляемый Postgres, снаружи box
exposeBox(projectId, boxName="scratch", port=3000) → выданное платформой имя хоста
```

`attachBoxDatabase` на этой инсталляции отвечает **501 Not Implemented** и не начнёт работать при
повторе: у здешнего рантайма box вообще нет пути attach. Создайте базу через `createDatabase` и
пропишите её строку подключения в окружение box сами — на один шаг больше, результат тот же.

База создаётся *снаружи* box, поэтому удаление box никогда не
уничтожает ваши данные. Выбрать имя открытого хоста нельзя — это возможность кристаллизации, а не
эфемерной песочницы.

Уборка, которую агент должен делать без напоминаний:

```
getBoxUsage(projectId, boxName="scratch")   → во что это уже обошлось
suspendBox(projectId, boxName="scratch")    → остановить оплату вычислений, оставить диск
resumeBox(projectId, boxName="scratch")     → тот же box, тот же диск, те же учётки
deleteBox(projectId, boxName="scratch")     → разрушающая операция, освобождает мощности
```

`ttl_seconds` **усыпляет** box, а не уничтожает его. Достигнутый `spend_cap_rub` тоже
приостанавливает его — по той же причине: побег должен стоить вам денег, но никогда не данных.

## Рецепт: превратить песочницу в настоящую VM

> «Это работает — сделай постоянным на `api.acme.com`.»

```
crystallizeBox(projectId, boxName="scratch",
               app_server_name="api-prod",
               domain="api.acme.com", probe_path="/healthz",
               ack_monthly_charge=true)
listBoxCrystallizations(projectId, boxName="scratch")   → отчёт о проверке
listAppServers(projectId)
```

`ack_monthly_charge` — это согласие, а не флажок: без него вызов вернёт 409. Повышение превращает
поминутную оплату в месячный счёт за VM, поэтому агент не может подписать вас на это случайно.

Читайте `verified` в отчёте отдельно от `status`. Попытка может завершиться и всё равно не пройти
проверку: должны сойтись манифест файлов, набор слушающих сокетов, сравнение хешей переменных по
ключам и сквозная HTTP-проба.

## Рецепт: диагностика падающего приложения

> «`api` нездоров, что не так?»

```
listProjects
getProject(projectId)
listApps(projectId, envId)                        → прочитать фазу
searchLogs(projectId, app="api", since="1h", size=200)
searchLogs(projectId, app="api", q="error", since="6h")
getBuild(projectId, buildId)                      → если похоже на проблему сборки
```

Для приложения на VM или Compose передавайте `vm="<имя сервера>"` вместо `app=`. Одно из двух
обязательно — именно эта проверка не даёт одному арендатору залезть в чужие логи.

Встроенный prompt `diagnose-app` делает ровно эту последовательность и имеет прямой запрет
что-либо менять, поэтому его безопасно давать агенту, который вот-вот начнёт гадать.

Подробнее: [мониторинг: метрики, логи и алерты](monitoring-metrics-logs-alerts.md).

## Рецепт: подключить свой домен

```
addDomainAuthorization(projectId, apex_domain="acme.com")  → TXT-челлендж
# TXT-запись у своего DNS-провайдера публикуете вы сами
verifyDomainAuthorization(projectId, id=…)
```

Авторизация апекса покрывает его поддомены. **Привязка проверенного хоста к конкретному
приложению на поверхности агента отсутствует** — этот шаг доделайте в консоли.

Подробнее: [свой домен и HTTPS](domains-and-https.md).

## Когда агент застрял

Четыре режима отказа покрывают почти всё:

1. **Передал slug там, где ждали UUID.** Симптом: 404 на каждый вызов. Лечение: `listProjects`,
   потом `getProject`.
2. **Остановился на Committed.** Симптом: «успешно выкачено» для приложения в crash-loop. Лечение:
   заставить опрашивать фазу через `listApps`.
3. **Ищет инструмент, которого нет.** Симптом: предлагает перезапустить, удалить или выполнить
   команду в шелле. Этого нет намеренно; отправьте его в
   [справочник инструментов](mcp-tool-reference.md).
4. **Сдался на 404, который значил «ещё нет».** Симптом: бросает базу сразу после создания.
   Лечение: опрашивать list-инструмент, пока фаза не станет готовой.
