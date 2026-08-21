---
id: 0279
status: open
prio: P2
title: EnsureDefaultProject = 0 строк в audit_events ЗА ВСЮ ИСТОРИЮ, но это unmeasured, НЕ разъезд имени action
sess: sess-0814
section: Открытые долги (не терять)
---
- [ ] 🟡 `EnsureDefaultProject` = 0 строк в `audit_events` ЗА ВСЮ ИСТОРИЮ, но это `unmeasured`, НЕ разъезд имени action (sess-0814L, [code] `backend/internal/api/projects.go:441,453,488` — успех и все три отказа пишут одну и ту же строку `Action:"EnsureDefaultProject"`). Success-путь появился в `74f3b518`, в проде с 16:05Z 08-14; ни одной регистрации после раскатки не было. Мерить, когда придёт следующий сигнап; не заводить «фикс» на пустом месте.
