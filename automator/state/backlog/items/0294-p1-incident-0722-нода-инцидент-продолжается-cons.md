---
id: 0294
status: open
prio: P0
title: P1-INCIDENT-0722 · **нода-инцидент ПРОДОЛЖАЕТСЯ, console recovered паллиативом **
created: 2026-07-22
section: Открытые долги (не терять)
---
- [~] P1-INCIDENT-0722 · **нода-инцидент ПРОДОЛЖАЕТСЯ, console recovered паллиативом [live 07-22 07:1xZ loop-inc-auto]**: (1) **console.dada-tuda.ru было HTTP 503 (DOWN)** — frontend+backend+gateway+gitops-agent Pending «Insufficient memory»: нода выпала → поды передавили на 2 живые ноды (kqk7z 80%/pklgc 61% mem, ПЕРЕПОДПИСКА реквестами), trhrn НЕ принимает. ВОССТАНОВЛЕНО [live 503→307, все console-поды Running]: force-delete 6 stuck-Terminating на trhrn + knative operator-webhook Terminating 50m на kqk7z → освободил реквесты → scheduler поднял Pending. (2) **trhrn kubelet = ЗОМБИ [live доказано пробой]**: Node Ready=True + свежий heartbeat, НО тест-под (явный nodeName+toleration) остался Pending, Events<none> — container runtime НЕ стартует новые поды. Taint `dada-incident=kubelet-dead:NoSchedule` СПРАВЕДЛИВ, НЕ снимать. (3) 🔴 **OWNER-GATED физически**: kubelet restart на trhrn (SSH/Beget) ИЛИ replace ноды — вернёт 3-ю ноду, снимет переподписку. Без этого capacity хрупкая = рецидив при любом рестарте подов. (4) диски все 3 ноды 70-85% (ImageGCFailed) — сопутствует. Герой-фикс e5ac7db деплой БЛОКИРОВАЛСЯ этим (build-agent/gitops были Pending) — теперь Running, деплой может продолжиться.
