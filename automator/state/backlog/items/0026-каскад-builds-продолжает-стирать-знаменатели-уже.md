---
id: 0026
status: open
prio: P3
title: Каскад builds продолжает стирать знаменатели уже идущих замеров
created: 2026-08-16
sess: sess-0816f
section: Backlog (execution-bet)
---
- [ ] Каскад `builds` продолжает стирать знаменатели уже идущих замеров (sess-0816f, 2026-08-16, [live]): базовая линия E71 за 07-24→08-02 (13 строк) на момент закрытия замера физически не существует. Замер, чья база живёт в `builds`, обязан фиксировать её КОПИЮ в experiments.md в момент старта, иначе он незакрываем по построению.
  **Три дыры подряд, все три в цепочке H08.** (1) КЛАССИФИКАТОР НЕ ЗНАЕТ ЭТОЙ ПРИЧИНЫ: ни `platformCrashSignatures`, ни остальные таблицы (`backend/internal/notify/notify.go:405-476`) не содержат ни одного паттерна про read-only транзакцию; строка `app_health_alerts` у аппа имеет `cause`/`cause_line`/`cause_kind` ПУСТЫМИ. Хуже: лог несёт заголовок `Traceback (most recent call last)`, поэтому при любом раскладе, где до питоновской таблицы дойдёт очередь, платформа скажет юзеру «это ошибка в коде приложения (Python)» — прямая ложь про поломку, которую устроили мы сами. (2) ПИСЬМО УШЛО ЧЕРЕЗ 18ч56м И БЕЗ ПРИЧИНЫ: `last_sent_at=2026-08-16 15:38:20Z`, `last_send_ok=t`, `last_recipient=artemmendeleev@gmail.com`, `last_sent_cause_kind` пуст. (3) РЫЧАГА НЕТ: `crashCauseKey` (`frontend/components/deploy/app-alerts-banner.tsx:52-68`) на пустом `cause_kind` возвращает `null`, ветка `cause_line` тоже пуста → юзер видит общий текст «контейнер упал» + «Диагностика» + логи. Действующие рычаги есть только у `bad_connection_string`/`ssl_not_supported`/`missing_env_var`/`app_needs_args` (:574-:665) — ни один не про квоту.
  Правка: новый `cause_kind` для отказа записи в БД, проверяемый ДО питоновской таблицы (тот же приём и та же причина, что у `platformCrashSignatures`), + ветка баннера с рычагами «база» и «тариф». Пруф двухполюсный: лог с `read-only transaction` даёт новый kind и НЕ даёт `app_code`; обычный питоновский `Traceback` по-прежнему даёт `app_code`.
