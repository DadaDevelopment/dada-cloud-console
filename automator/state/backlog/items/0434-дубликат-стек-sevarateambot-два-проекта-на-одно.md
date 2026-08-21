---
id: 0434
status: open
prio: P2
stream: 2
title: Дубликат-стек sevarateambot: два проекта на одно приложение, в одном потерян TELEGRAM_API_TOKEN
created: 2026-08-21
sess: sess-0821g
---
Пульс sess-0821g [live kubectl].

`sevarateambot` живёт в ДВУХ проектах владельца bruzas.85@mail.ru:
- `tvkassistantbot-prod` -- `CrashLoopBackOff`, 263 рестарта за 22ч,
  лог: `Ошибка: Не найден токен TELEGRAM_API_TOKEN`. Это единственная строка
  в `not_ready` админ-панели.
- `sevarabot-prod` -- `Running 1/1`, 7 рестартов за 22ч, тот же бот.

Класс известен: `project_deploystack_app_name_mismatch_creates_duplicate_stack`.
Причина падения юзерская (нет env), но появление ВТОРОЙ копии того же приложения --
наше: продукт позволил развести два проекта на одно имя приложения и не сказал об этом.

Осторожно: bruzas -- churn-interview subject, проекты НЕ удалять (память
`project_bruzas_churn_interview.md`). Здесь нужен не снос, а ответ на вопрос,
как дубликат вообще возник, и видит ли юзер, что у него две копии.
