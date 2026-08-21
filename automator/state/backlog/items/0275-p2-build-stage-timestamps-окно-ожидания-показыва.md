---
id: 0275
status: open
prio: P3
stream: 2
title: P2-BUILD-STAGE-TIMESTAMPS · Окно ожидания показывает статус-бейдж (queued/detecting/building/pushing), но не показывает, СКОЛЬКО и
section: 🎯 ПОТОК 2 — Deploy speed & reliability как продукт
---
- [ ] P2-BUILD-STAGE-TIMESTAMPS · Окно ожидания показывает статус-бейдж (queued/detecting/building/pushing), но не показывает, СКОЛЬКО идёт текущая стадия и что происходит после success (ArgoCD sync → Ready юзеру невидим). Нужны stage transition timestamps в модели билда (`backend/internal/api/builds.go:15-34`) + отображение «Detecting 1:42» и отдельная фаза «раскатывается» после success. Мерить только после того, как поедут пассивные audit-события (ViewBuildLogs), иначе вслепую.
