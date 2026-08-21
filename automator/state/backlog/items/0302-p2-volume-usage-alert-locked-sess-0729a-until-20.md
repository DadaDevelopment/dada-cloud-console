---
id: 0302
status: open
prio: P3
title: P2-VOLUME-USAGE-ALERT LOCKED sess-0729a until 2026-07-29T04:30Z
created: 2026-07-29
sess: sess-0729a
section: Chips из sess-0728a (инцидент fonbet disk-full)
---
- [~] P2-VOLUME-USAGE-ALERT LOCKED sess-0729a until 2026-07-29T04:30Z: диск юзера заполняется молча до ENOSPC-краша (fonbet 100% → CrashLoop сутки). Watcher ловит только постфактум CrashLoop. Кандидат: volume usage метрика (kubelet_volume_stats) + алерт-письмо на 85% + индикатор в консоли Storage.
- note: fonbet-value-restored dataLocality=best-effort→disabled (sess-0728a, иначе требовал реплику на attach-ноде d5dns без места — блочил expansion; perf-nicety, durability не тронута).
