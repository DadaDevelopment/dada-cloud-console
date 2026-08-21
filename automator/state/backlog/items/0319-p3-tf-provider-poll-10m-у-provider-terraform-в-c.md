---
id: 0319
status: open
prio: P3
stream: 6
title: P3-TF-PROVIDER-POLL-10M · У provider-terraform в crossplane-system НЕ задан --poll ни в args Deployment, ни в DeploymentRuntimeCon
sess: sess-0803d
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🟢 P3-TF-PROVIDER-POLL-10M (sess-0803d, побочно из расследования 72 минут) · У `provider-terraform` в `crossplane-system` НЕ задан `--poll` ни в args Deployment, ни в `DeploymentRuntimeConfig default` (`spec: {}`) [live] — работает дефолт (~10 мин). Значит после КАЖДОЙ правки спеки бакета реакция ждёт до 10 минут. В инциденте 08-02 этот вклад утонул в ручной диагностике и отдельно не вычленяется (логи провайдера пустые — debug выключен, k8s events протухли), поэтому цифры «сколько это стоит юзеру» у меня нет. Трогать только если появится замер, что ожидание провижининга реально упирается в поллинг, а не в наши баги. Не як-приоритет.
