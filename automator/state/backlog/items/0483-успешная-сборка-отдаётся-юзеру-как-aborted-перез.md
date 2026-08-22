---
id: 0483
status: open
prio: P0
stream: 2
title: Успешная сборка отдаётся юзеру как aborted: перезапуск build-agent пишет вердикт мимо Jenkins
created: 2026-08-22
sess: sess-0822i
---
Разбор провалов за 24ч, sess-0822i [live psql `builds` + `builds_logs`, сверено с Jenkins].

**4 из 7 неуспешных сборок окна — ЛОЖНЫЕ отказы.** Образ реально собран и запушен,
Jenkins-джоба терминально `<result>SUCCESS</result>`, а юзеру в консоли показан
`canceled` / `fail_reason='jenkins build aborted'`.

Улики, дословно:
- `3136daa4` → Jenkins `dada-build #451` = SUCCESS; в `builds_logs.line`:
  `build-agent restarting; jenkins build continues and will be reattached`.
  Строка в БД при этом `canceled`.
- `45d72a92` (env `megafactory`) → Jenkins `dada-build #466` = SUCCESS; в логе
  `Finished: SUCCESS` и `build & push complete for ...megafactory:upload-...`,
  строка `canceled`, `fail_reason='jenkins build aborted'`.
- Ещё две: `c328819a`, `5073bc4f` (env `fonbet-value`).
- `builds.error_message` = NULL на ВСЕХ этих строках — подтверждает известное свойство:
  текст живёт только в `builds_logs.line`.

**Корень (гипотеза, требует чтения кода):** путь reattach после рестарта пода build-agent
пишет терминальный вердикт, НЕ сверяясь с фактическим терминальным состоянием джобы Jenkins.
Это ровно наш повторяющийся класс «вердикт и его улика выбираются разными правилами»:
улика в логе говорит SUCCESS, вердикт в строке говорит aborted.

**Почему это P0, а не косметика.** Юзер видит, что его деплой провалился, хотя он доехал.
Это прямой удар по потоку 2 (надёжность деплоя КАК ПРОДУКТ) и по доверию к истории сборок:
зелёная история перестаёт что-либо значить в обе стороны. Плюс это ровно тот сигнал,
по которому юзер уходит чинить то, что не сломано.

Что сделать: перед записью `canceled`/`aborted` на пути reattach сверяться с терминальным
результатом джобы Jenkins; вердикт обязан ехать тем же стейтментом, что и факт.

M2: строка `builds` для сборки, пережившей рестарт build-agent, несёт `success`, и это
совпадает с `<result>` джобы Jenkins; на исторических 4 строках — пересчёт показывает,
сколько из них были ложными.
