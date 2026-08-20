---
id: 0338
status: open
prio: P3
stream: 6
title: P2-CRASHLOOP-МОЛЧИТ-ТРОЕ-СУТОК · client-a-prod/face-inference — 0/1 CrashLoopBackOff, **912 рестартов, 3д6ч**
created: 2026-08-10
sess: sess-0810j
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] P2-CRASHLOOP-МОЛЧИТ-ТРОЕ-СУТОК (sess-0810j, 2026-08-10) · `client-a-prod/face-inference` — `0/1 CrashLoopBackOff`, **912 рестартов, 3д6ч** [live kubectl 03:14Z]. Причина в контенте юзера, не в платформе: `RuntimeError: no objects found under 's3://25f4da9f5cfe-dada-tuda-s3/mlflow/1/c448c5cfc2c54eaea2c7d04f69c982e1/artifacts/models/buffalo_l' - check MODEL_S3_URI` (`/app/server.py:101`), артефакт модели из бакета исчез. Проект клиентский → read-only, чинить не наше. **Продуктовый вопрос наш:** апп трое суток в рестарт-петле с одной и той же строкой в логе, и платформа об этом молчит — ни поломки в панели, ни причины в консоли, ни авто-фикса. Это ровно поток №3 (AI auto-fix): детерминированный краш при старте с постоянным сообщением — самый дешёвый класс для «сказать юзеру причину человеческим языком». Проверить, попадает ли такой апп в `/api/v1/admin/overview` broken-панель, и если нет — почему.
