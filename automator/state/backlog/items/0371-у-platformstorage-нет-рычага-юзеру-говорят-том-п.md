---
id: 0371
status: open
prio: P1
stream: 3
hypothesis: H08
title: У platform_storage нет рычага: юзеру говорят «том полон» и не дают его увеличить
created: 2026-08-19
sess: sess-0819e
---
**Заземление [code].** `frontend/components/deploy/app-alerts-banner.tsx` даёт рычаг для
`missing_env_var` (форма ввода), `bad_connection_string` и `ssl_not_supported` (кнопка «Исправить»),
`app_needs_args` (ссылка на Start Command), `db_read_only` (ссылки на Databases и Billing).
Для `platform_*` и `resource_limit` — только ссылки на логи.

**Почему это болит именно сейчас.** После 0369 краш `fonbet-value` начнёт показывать
`platform_storage` с текстом «нехватка места на томе нашей платформы». Верно и бесполезно:
том 20Gi забит на 100%, а увеличить его из баннера нельзя. Получается диагноз без рычага —
ровно паттерн памяти `project_diagnosis_without_a_lever_leaves_user_down`.

**Рычаг существует в продукте.** Есть `updateAppStorage` (MCP-тул и, судя по нему, ручка в API).
Задача — довести его до баннера: «том заполнен → увеличить до N ГБ» с честной ценой по тарифу,
либо, если тариф не позволяет, вести на апгрейд теми же словами, что и `db_read_only`.

**Не делать вслепую:** сначала проверить, что ресайз тома longhorn на живом PVC вообще проходит
без пересоздания аппа, иначе кнопка пообещает то, чего платформа не умеет.
