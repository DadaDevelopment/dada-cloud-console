---
id: 0299
status: open
prio: P3
title: P3 · DeleteProject не чистит source-uploads/<projectID>/ в S3 (подтверждено live 07-30 sess-0730b)
created: 2026-07-30
sess: sess-0730b
section: Открытые долги (не терять)
---
- [ ] P3 · DeleteProject не чистит source-uploads/<projectID>/ в S3 (подтверждено live 07-30 sess-0730b). Объём копеечный, но это тихий рост мусора; чинить в тот же advisory-locked sweep-тик, что и volexports/. Осторожно: архив = единственная копия исходника юзера, удалять ТОЛЬКО вместе с проектом, не по TTL.
