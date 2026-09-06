---
id: 0495
status: open
prio: P1
stream: 2
title: recovery-бюджет платформенных билдов не сбрасывается после восстановления платформы
created: 2026-09-06
---
 recovery-бюджет платформенных билдов не сбрасывается после восстановления платформы
RetryPlatformFailedBuilds отбирает только attempt < PlatformRecoveryMaxAttempts (6). Во время длинного outage (Jenkins 09-04/05) билды сожгли все 6 попыток; после восстановления sweep их молча пропускает - юзер остаётся с красным билдом и должен сам догадаться нажать retry. Доказано 09-06: fanvk/artem (attempt=6) потребовал ручного UPDATE. Фикс: при детекте 'platform снова здорова' (первый успешный jenkins trigger после outage-окна) сбрасывать attempt=1 у failed/platform_error билдов свежее N дней, либо вести attempt_outage отдельно от attempt_transient. Файл: build-agent/internal/db/builds.go RetryPlatformFailedBuilds + runner.go RetryPlatformFailures.
