---
id: 0493
status: closed
prio: P1
stream: 2
title: Domain ownership verification checks wrong base domain for public suffixes (run.place etc)
created: 2026-09-04
closed_at: 2026-09-11
closed_commit: dec39f68
closed_note: Public-suffix часть оказалась уже пофикшенной (73aa9504 08-25, тест domain-authorization.test.ts пинит fanclub.run.place). Реальный живой леак другой и закрыт dec39f68: 145 неудачных verify против 5 успешных, два поллера без бэкоффа и сырая ошибка резолвера вместо подсказки. Теперь бэкофф 10с->5мин, стоп после 10 попыток, текст говорит что сделать.
---
Symptom [live feedback 2026-08-25, user saravananofficial13 cohort]: verifying fanclub.run.place triggers a check against run.place instead, so verification can never pass; user asked in feedback "где его указать?" for the missing Ingress rule (404 message).
Root cause guess: base-domain extraction uses last-two-labels heuristic -> public suffix (run.place) treated as registrable base; also the 404 ingress error message exposes internals without telling the user WHERE to point DNS.
Where: backend domain verification (PublicApi/domain verify path) + domain issues copy in frontend.
Next: use public-suffix-aware registrable-domain logic (or explicit zone apex from PowerDNS), fix copy to name the exact DNS record.
