---
id: 0411
status: open
prio: P1
stream: reliability
title: Флаг в configmap консоли не доезжает до пода: нет checksum/config, envFrom не рестартит
created: 2026-08-20
sess: sess-0820f
---
Механизм, а не разовая неприятность (M0). Деплой `dada-cloud-console-backend` монтирует
`dada-cloud-console-config` через `envFrom` и несёт в `spec.template.metadata.annotations` только
`checksum/secret`. Значит правка ЛЮБОГО флага в `argo-infra`
(`clusters/beget-prod/projects/platform/environments/prod/apps/cloud-console/values.yaml`) доезжает
до configmap и НЕ доезжает до процесса: под продолжает жить со старым `env`.

Проверено живьём в этом цикле (0409): configmap показал `PLATFORM_SELFHEAL_ENABLED=true` через ~40
секунд после пуша, а `env` в поде — ничего, до `kubectl rollout restart`. Ловушка тихая: и git,
и Argo, и configmap выглядят зелёными, поэтому «флаг включён» читается как правда.

Правка: добавить в чарт консоли `checksum/config` по содержимому configmap рядом с существующим
`checksum/secret`. Тогда любой флип флага сам катит поды.

M2: изменить безобидный ключ в values.yaml, дождаться синка и увидеть новый под БЕЗ ручного
рестарта.
