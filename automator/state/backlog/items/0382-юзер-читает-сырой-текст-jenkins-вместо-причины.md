---
id: 0382
status: open
prio: P2
title: ЮЗЕР ЧИТАЕТ СЫРОЙ ТЕКСТ JENKINS ВМЕСТО ПРИЧИНЫ
created: 2026-08-19
sess: sess-0819j
---
На странице сборки под переведённым ярлыком «platform_error» печатается сырой детейл вида `resolve build number: queue item no longer known to jenkins` (frontend/lib/build-failure.ts, frontend/app/(console)/projects/[projectId]/apps/[appName]/builds/[buildId]/page.tsx:386-444). Для юзера это шум: ни понять, ни починить. После 9ee1d4f6 такие сборки платформа перезапускает сама — текст обязан говорить именно это («поломка на нашей стороне, пересобираем сами»), а сырую строку прятать под раскрытие для поддержки.
