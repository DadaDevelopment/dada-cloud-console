---
id: 0493
status: open
prio: P1
stream: 2
title: Domain ownership verification checks wrong base domain for public suffixes (run.place etc)
created: 2026-09-04
---
Symptom [live feedback 2026-08-25, user saravananofficial13 cohort]: verifying fanclub.run.place triggers a check against run.place instead, so verification can never pass; user asked in feedback "где его указать?" for the missing Ingress rule (404 message).
Root cause guess: base-domain extraction uses last-two-labels heuristic -> public suffix (run.place) treated as registrable base; also the 404 ingress error message exposes internals without telling the user WHERE to point DNS.
Where: backend domain verification (PublicApi/domain verify path) + domain issues copy in frontend.
Next: use public-suffix-aware registrable-domain logic (or explicit zone apex from PowerDNS), fix copy to name the exact DNS record.
