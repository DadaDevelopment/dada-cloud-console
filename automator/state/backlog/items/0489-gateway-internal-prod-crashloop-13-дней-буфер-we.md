---
id: 0489
status: open
prio: P0
stream: 2
title: gateway internal-prod CrashLoop 13 дней: буфер WebClient 256KB vs список PublicApi 276KB
created: 2026-08-31
sess: sess-0831a
---
[platform-truth] Живой фикс отправлен в DadaDevelopment/spring.gateway 0ab09d9
(develop, push 16:30Z): kubeClient WebClient maxInMemorySize 2MB + глобальный
spring.codec.max-in-memory-size 2MB.

КОРЕНЬ [live kubectl logs internal-prod/gateway-deploy]: список PublicApi CR
вырос до 276443 байт (замер 08-31) > 256KiB дефолта Spring WebFlux ->
DataBufferLimitException на ApplicationReadyEvent; в пустом/error-пути
refresh() сортировал immutable List.of() -> UnsupportedOperationException.
Это УЖЕ чинилось владельцем 3679454 (08-28, копия списка перед sort), но без
поднятия буфера фикс не помог: 709 рестартов за 3д5ч, 13 дней даунтайма
api-prod.dada-tuda.ru. App health alert впервые 08-31 15:57 (запоздание детектора).

ВАЖНО: образ с 0ab09d9 НЕ соберётся пока жив баг 0488 (springPipeline CPS) —
пункт 0488 блокирует доставку этого фикса. Альтернативный стопгап:
javaToolOptionsAppend: -Dspring.codec.max-in-memory-size=2MB в
argo-infra clusters/beget-prod/projects/internal/environments/prod/apps/gateway/values.yaml
(чарт поддерживает, helm/common/templates/deployment.yaml:214) — но там чужой
незакоммиченный diff, править отдельным циклом после чистки дерева.

Verify-бар: pod gateway-deploy Running и не рестартует 30+ мин, ИЛИ
/health 200, ИЛИ список not_ready в /admin/overview похудел с 2 до 1.
