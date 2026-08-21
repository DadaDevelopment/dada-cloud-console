---
id: 0207
status: open
prio: P2
title: НЕТ УДАЛЕНИЯ S3-БАКЕТА — в роутере только GET list, POST create, GET credentials (backend/internal/api/router.go:362-364)
sess: sess-0806q
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 НЕТ УДАЛЕНИЯ S3-БАКЕТА (sess-0806q, [code]) — в роутере только `GET list`, `POST create`, `GET credentials` (`backend/internal/api/router.go:362-364`). Застрявший на провижининге бакет юзер убрать не может НИКАК, а создать заново под тем же именем мешает 409 `name_taken` (`s3buckets.go:269`). Из-за этого подсказка про зависший провижининг вынужденно заканчивается «напишите нам». Работа: DELETE-эндпоинт + кнопка, с тем же гейтом прав, что и create.
