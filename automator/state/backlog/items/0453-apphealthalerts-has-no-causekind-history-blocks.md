---
id: 0453
status: open
prio: P2
stream: 2
title: app_health_alerts has no cause_kind history, blocks retro-verify
created: 2026-08-21
sess: sess-0821g
---
`app_health_alerts` хранит только ТЕКУЩЕЕ состояние на апп (cause_kind, last_sent_cause_kind) —
без истории переходов. Обнаружено при замере E165 (2026-08-21, sess-0821g): апп `fonbet-value`
за окно измерения сменил инцидент (db_read_only → platform_storage_inodes → resource_limit),
и конкретную историческую классификацию (ENOSPC-репро) нельзя ни подтвердить, ни опровергнуть
задним числом — строка перезаписана более новым инцидентом.

Это НЕ zero-denominator (событие было), а структурный пробел схемы: нет append-only лога
cause_kind-переходов на апп. Блокирует ретроспективный замер любого будущего E-эксперимента
про классификатор причин, если между фиксом и замером апп словил новый инцидент.

Предложение: писать cause_kind-транзишены в отдельную append-only таблицу
(app_health_cause_history или аналог) при каждом изменении, не только держать текущее значение.
