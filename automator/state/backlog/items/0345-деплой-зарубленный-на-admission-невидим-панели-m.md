---
id: 0345
status: open
prio: P0
stream: 6
title: Деплой, зарубленный на admission, НЕВИДИМ панели: ml-prod/whisper
created: 2026-08-10
sess: sess-0810m
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🔴 Деплой, зарубленный на admission, НЕВИДИМ панели: `ml-prod/whisper` (sess-0810m, 2026-08-10, [live kubectl]) — knative `validation.webhook.serving.knative.dev` отбивает `InternalError`: «maximum memory usage per Container is 2Gi, but limit is 3Gi». Цикл повторов каждые ~17 минут (09:23, 09:40, 09:56, 10:13), под НЕ создаётся вовсе. В `/api/v1/admin/overview` этого нет НИ В ОДНОМ массиве. Механика один в один та, что держала апп юзера лежачим двое суток (`87e7f37e`/`454b9f60`): спека против лимита неймспейса. Сейчас проект внутренний (`ml`), поэтому юзера не задело — но панель структурно не умеет показывать этот класс, и в следующий раз это будет юзер. Правка: тянуть `FailedCreate`/admission-отказы в отдельный массив панели; `readyReplicas=0 при spec.replicas>0` их не ловит, потому что объекта Pod не существует.
