---
id: 0304
status: open
prio: P1
title: (закрыт выше, лок снят) P1-OWNERLESS-IDENTITY-ROOT-CAUSE прежняя формулировка sess-0729c
sess: sess-0729c
section: Chips из sess-0728a (инцидент fonbet disk-full)
---
- [~] (закрыт выше, лок снят) P1-OWNERLESS-IDENTITY-ROOT-CAUSE прежняя формулировка sess-0729c: 5 live-проектов с owner_id=NULL [live psql], среди них client-a-prod (michaelharlam) — ЕДИНСТВЕННЫЙ активно итерирующий юзер (27 agent-chat msgs 07-28, редеплой face-api 07-29). Следствия: (1) он не попадает НИ В ОДНУ funnel-SQL → недельные цифры активации/удержания ЗАНИЖЕНЫ, прошлые отчёты требуют пересчёта; (2) не биллится; (3) алерты после 294d546 уходят оператору, не ему. `insertProject` [code projects.go:265] ВСЕГДА пишет owner_id → строки приехали другим путём (adoption/import? agent-chat write-tools? legacy) — путь НЕИЗВЕСТЕН = баг открыт. M2: count(owner_id is null) 5→0 с резолвом реального email + воспроизведён путь-создатель на throwaway (новый проект приходит с owner_id NOT NULL) + живой алерт по client-a-prod уходит юзеру.

═══════════════════════════════════════════════════════════════════════
