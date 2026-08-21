# Audit path graph — sess-0821g (2026-08-21, live)

Сеть ЗЕЛЁНАЯ весь цикл (`probe-prod-access.sh` = ЗЕЛЁНЫЙ: apiserver `/readyz=ok`, psql через
под `postgresql-0`, консоль 307). Окно: `now()=2026-08-21 12:1x UTC`.

## 0. Волюм [live psql]

| metric | 30d | 7d | 48h |
|---|---|---|---|
| new users | 29 | 2 | **0** |
| audit_events rows | 5786 | — | 649 |

**Новых регистраций за 48ч — ноль.** Последняя — `lifecoachrussia@yandex.ru` 08-19 09:37 UTC,
до неё `kkartov@yandex.ru` (08-17), `artempro2022@yandex.ru` (08-13). Это результат замера,
а не «нечего мерить»: верх воронки встал вторые сутки подряд.

## 1. Кто был активен за 48ч (раз новых нет — читаем существующих) [live psql]

Owner-домен `dada-tuda.ru` исключён (админ-браузинг, не сигнал). 7 не-owner акторов:

| email | events | last |
|---|---|---|
| michaelharlam@yandex.ru | 73 | 08-21 12:10 (в момент замера) |
| artempro2022@yandex.ru | 45 | 08-21 05:00 |
| bruzas.85@mail.ru | 34 | 08-20 13:59 |
| artempro2021@bk.ru | 28 | 08-21 04:53 |
| artemmendeleev@gmail.com | 12 | 08-21 10:22 |
| mytake@yandex.ru | 3 | — |
| kkartov@yandex.ru | 3 | — |

### Находка цикла — `michaelharlam@yandex.ru`

```
08-19 12:16  SessionStart -> ViewProject -> ViewApps  (× 5 за день)
08-19 22:46  ViewApp(profi) -> BuildAutoRetried -> BuildFinished SUCCESS -> DeployImageVersion SUCCESS
08-20 00:06  ещё один деплой SUCCESS
08-20 09:45  AgentChat, 4 отдельных чата, растущая вовлечённость
08-20 10:44:06.327  AgentChatActionApproved createS3Bucket
             args {"bucket_name":"dating-service-assets","public":true}   <- name ПУСТОЕ
08-20 10:44:06.338  CreateS3Bucket FAILURE {"reason":"missing_name","status":400}
08-20 12:05 .. 08-21 12:10  9 сессий подряд, только ViewProject/ViewApps,
             ни одного write-действия за 25.5 часов
```

Юзер ЖИВ (сессии идут в момент замера) и не сделал ни одной попытки после отказа.
Ноль retry на новой фиче — retry наблюдается только на build/deploy.

**Это не новый дефект.** Тот же `missing_name` стрелял 2026-08-04, но поймал его внутренний
тестовый аккаунт `michaelharlam@dada-tuda.ru`. 16 дней без починки, затем цена — живой юзер.
30-дневный счёт `CreateS3Bucket`: 8 success / 2 failure, оба failure — этот класс.

Корень [code]: `backend/internal/api/s3buckets.go` (hard-reject `missing_name` без фолбэка на
заполненный `bucket_name`) + `backend/internal/api/agent_chat.go:648-651` (карточка рендерит
`%q` от пустой строки, approve остаётся активен) + фронт не валидирует обязательные поля
карточки. Заведено **0432**, чинится этим циклом с двух сторон.

### `bruzas.85@mail.ru` — не пассивность, а 12 дней борьбы
`ConnectGitRepo repo_already_linked` ×6 (08-08/09) → пересоздание проекта → `StartGitAppInstall`
×9 → `TriggerAutofix` failure `failed to mint install token` 502 (08-18 22:54) → 3
`BuildFinished failure` c `fail_reason=platform_error` (08-19) → `DeployImageVersion pending`
08-20 13:59 без видимого исхода.

## 2. Zero-activity кросс-чек [live psql]

Новых юзеров в окне нет → новых «мёртвых сигнапов» нет. Instrumentation-gap (179 чат-сообщений
на 1 строку аудита, пункт 0430) без изменений.

**Поправка к методу прошлых циклов:** не считать провалом `outcome='pending'`. Проверка
тестового аккаунта дала 4 «провала» по счёту `outcome != 'success'`, все четыре оказались
парами `pending -> success` штатной async-операции. Считать провалом ТОЛЬКО `outcome='failure'`.

## 3. Граф переходов, 30д [live psql]

Терминальное действие, когорта 30д минус owner минус farm-волна (≤1 строки аудита):

| терминальное действие | юзеров | кто |
|---|---|---|
| SessionStart | 7 | остаток farm + michaelharlam (жив, не «сдался») |
| DeployImageVersion | 3 | lifecoachrussia, artempro2021, artempro2022 — довезли |
| ViewApps | 3 | mytake, good.win2283, kkartov — смотрят и не действуют |
| ViewProject | 1 | cryocrm |

Форма графа против прошлого разбора не изменилась (было 4/7 → сейчас 3/4 живых
non-deploy юзеров кончают на `ViewApps`).

## 4. Что изменилось против прошлого разбора

- Вывод «отвалившиеся кончают на `ViewApps`» **подтверждён и локализован**: у `kkartov`
  причина — `InstallSolution env_failed` 500 (0431), у `michaelharlam` — `CreateS3Bucket
  missing_name` 400 (0432). Оба — provider-side reject, после которого ноль retry.
- **Новое:** сигнап стоит на нуле 48ч.
- **Новое:** дефект, пойманный внутренним тестом 16 дней назад, укусил живого юзера. Класс
  «диагноз есть, рычага нет» повторился, теперь с ценой.

## 5. UX-вывод цикла

Чинить не класс ошибки, а момент одобрения: продукт обязан не давать подтвердить действие,
которое он сам через 11 мс отвергнет. Отгружается в 0432 (бэк-фолбэк + гард approve на фронте).
