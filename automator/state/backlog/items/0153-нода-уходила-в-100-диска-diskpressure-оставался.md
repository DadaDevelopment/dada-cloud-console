---
id: 0153
status: open
prio: P2
title: НОДА УХОДИЛА В 100% ДИСКА, DiskPressure ОСТАВАЛСЯ False, ВЫЛЕЧИЛОСЬ САМО
created: 2026-08-12
sess: sess-0812b
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 НОДА УХОДИЛА В 100% ДИСКА, `DiskPressure` ОСТАВАЛСЯ `False`, ВЫЛЕЧИЛОСЬ САМО (sess-0812b, 2026-08-12, [live dmesg+df на привилегированном поде]) — на `d5c373-client-b2a7cd-7qwnw-rt7fr` (нода `postgresql-0`) ядро крутило `systemd-journald: Failed to open system journal: No space left on device`, дропая до ~89 тыс. одинаковых сообщений за 60 с, а `runc` падал с `write /tmp/runc-processNNN: no space left on device` на liveness-пробах `postgresql-0`, на `instance-manager`/`engine-image` Longhorn и на `mkdir /var/lib/kubelet/pods/...`. При этом `DiskPressure=False` на всех 4 нодах, т.е. защита kubelet (вытеснение/бэкофф) НЕ включалась ни разу. Отпустило само после `Rotating system journal` — на момент проверки `/dev/vda1` 82%, 12.9G свободно, инодов 5%, тестовая запись 8 МБ в `/tmp` и `/var/log` проходит за 0.02 с. Т.е. это НЕ хроническая нехватка, а пики до нуля, которые никто не видит: единственный след — kernel ring buffer, который сам же и не пишется. Класс дорогой: тот же тип отказа держал легаси-шард (и апп artem'а) 4.5 суток. Нужен алерт по факту `FreeDiskSpaceFailed`/ENOSPC в событиях, не по порогу `DiskPressure`, который в этой аварии молчал. Соседний факт для того же пункта: zh58h реально тесная — 93%, 6.9Gi свободно (Longhorn node CR).
