# Agent Runtime Platform — Integration Guide

## Архитектура

```
┌──────────┐
│ Telegram │
└────┬─────┘
     │
     ▼
┌─────────────────┐      ┌──────────────────┐
│   tg-gateway    │─────▶│  agent-runtime   │
│   (long poll)   │      │  (conversation   │
│                 │      │   state + hooks) │
└─────────────────┘      └────────┬─────────┘
     │ fallback               │
     │ A2A                    │ A2A + history
     ▼                        ▼
     └──────────────┬─────────┘
                    ▼
            ┌──────────────┐
            │    kagent    │
            │    Agent     │
            └──────────────┘
                    ▼
            ┌──────────────┐
            │ External CRM │
            │  (via hooks) │
            └──────────────┘
```

## Компоненты

### 1. agent-runtime Service

**Задачи:**
- Управление conversation state (id, actor, metadata)
- Хранение message history
- Выполнение lifecycle hooks (HTTP, metadata, schedule)
- Предоставление domain instructions (будущее)

**API:**
- `POST /message` — ProcessMessage (основной вход)
- `GET /health` — health check

**Env vars:**
```bash
AGENT_RUNTIME_PORT=8083
GITOPS_BASE_PATH=/tmp/dada-state-repo  # для domain instructions
DB_URL=postgres://...
LOG_LEVEL=info
```

### 2. tg-gateway Integration

**Изменения:**
- `Manager.SetRuntimeClient(runtime)` — включает платформу
- Без `SetRuntimeClient()` — работает как раньше (direct A2A)
- С runtime — сначала `runtime.ProcessMessage()`, fallback на A2A при ошибке

**Backward compatibility:**
- Старые деплои без agent-runtime продолжают работать
- tg-gateway логирует `runtime unavailable, falling back to direct A2A`

### 3. Lifecycle Hooks

**Trigger events:**
- `conversation.created` — первый message в новом диалоге
- `message.received` — каждый user message
- `agent.run.completed` — после успешного ответа агента
- `conversation.idle` — срабатывает через N минут после последнего message

**Action types:**

#### HTTP Action
Вызывает внешний сервис, interpolate template, сохраняет response в metadata.

```json
{
  "method": "POST",
  "url": "http://twenty-crm:3000/api/persons",
  "headers": {
    "Authorization": "Bearer token"
  },
  "body": {
    "telegram_id": "{{ actor.external_id }}",
    "username": "{{ actor.username }}",
    "chat_id": "{{ conversation.external_id }}",
    "first_name": "{{ actor.metadata.first_name }}"
  },
  "store_response": {
    "conversation.metadata.crm_person_id": "{{ response.id }}"
  }
}
```

**Template variables:**
- `{{ conversation.id }}` — UUID
- `{{ conversation.external_id }}` — telegram chat_id
- `{{ conversation.agent_name }}` — agent name
- `{{ conversation.metadata.KEY }}` — any metadata field
- `{{ actor.external_id }}` — telegram user_id
- `{{ actor.username }}` — telegram username
- `{{ actor.metadata.KEY }}` — actor metadata (first_name, etc)
- `{{ response.KEY }}` — response field (только в store_response)

#### Metadata Action
Обновляет conversation.metadata без внешнего вызова.

```json
{
  "set": {
    "first_seen": "true",
    "priority": "high",
    "tag": "vip-client"
  }
}
```

#### Schedule Action
Stub для idle scheduler (Phase 5).

```json
{
  "agent_message": "следующие шаги по этому клиенту?"
}
```

## Примеры использования

### Создание CRM person при первом контакте

```sql
INSERT INTO lifecycle_hooks (
  agent_name, 
  name, 
  trigger_event, 
  action_type, 
  action_config, 
  enabled
)
VALUES (
  'tg-agent-tools',
  'create-crm-person',
  'conversation.created',
  'http',
  '{
    "method": "POST",
    "url": "http://twenty-crm:3000/api/persons",
    "body": {
      "telegram_id": "{{ actor.external_id }}",
      "username": "{{ actor.username }}",
      "chat_id": "{{ conversation.external_id }}"
    },
    "store_response": {
      "conversation.metadata.crm_person_id": "{{ response.id }}"
    }
  }'::jsonb,
  true
);
```

### Тегирование VIP клиентов

```sql
INSERT INTO lifecycle_hooks (
  agent_name, 
  name, 
  trigger_event, 
  trigger_config,
  action_type, 
  action_config, 
  enabled
)
VALUES (
  'tg-agent-tools',
  'tag-vip-clients',
  'message.received',
  '{}'::jsonb,
  'metadata',
  '{
    "set": {
      "message_count": "{{ actor.metadata.message_count + 1 }}",
      "last_activity": "{{ now }}"
    }
  }'::jsonb,
  true
);
```

### Follow-up через 30 минут idle

```sql
INSERT INTO lifecycle_hooks (
  agent_name, 
  name, 
  trigger_event, 
  trigger_config,
  action_type, 
  action_config, 
  enabled
)
VALUES (
  'tg-agent-tools',
  'follow-up-idle',
  'conversation.idle',
  '{"idle_minutes": 30}'::jsonb,
  'schedule',
  '{
    "agent_message": "есть ещё вопросы по регистрации?"
  }'::jsonb,
  true
);
```

## Deployment

### Docker Compose (local dev)

```yaml
services:
  agent-runtime:
    build:
      context: .
      dockerfile: Dockerfile.agent-runtime
    ports:
      - "8083:8083"
    environment:
      - DB_URL=postgres://dada:dada@postgres:5432/dada
      - GITOPS_BASE_PATH=/tmp/dada-state-repo
      - LOG_LEVEL=info
    depends_on:
      - postgres
    networks:
      - dada-network

  tg-gateway:
    # ... existing config
    environment:
      - TG_GATEWAY_RUNTIME_URL=http://agent-runtime:8083
    depends_on:
      - agent-runtime
```

### Kubernetes Helm

```yaml
# values.yaml для agent-runtime
replicaCount: 1  # MUST BE 1 (stateful conversation state)

image:
  repository: ghcr.io/dada-tuda/agent-runtime
  tag: "latest"

service:
  type: ClusterIP
  port: 8083

env:
  - name: DB_URL
    valueFrom:
      secretKeyRef:
        name: postgres-credentials
        key: url
  - name: GITOPS_BASE_PATH
    value: "/gitops"
  - name: LOG_LEVEL
    value: "info"

volumeMounts:
  - name: gitops-repo
    mountPath: /gitops
    readOnly: true

volumes:
  - name: gitops-repo
    persistentVolumeClaim:
      claimName: gitops-state-pvc
```

### Migrations

```bash
# Apply conversation state + lifecycle hooks schema
cd backend
go run ./cmd/migrate
```

Миграции автоматически применяются при старте `cmd/server`, но `agent-runtime` не запускает их сам — он только читает/пишет в таблицы.

## Мониторинг

### Metrics (будущее)

- `agentruntime_messages_total{agent, channel}` — всего сообщений
- `agentruntime_conversations_active{agent}` — активных диалогов
- `agentruntime_hook_executions_total{hook, status}` — выполнений hooks
- `agentruntime_a2a_latency_seconds` — задержка kagent

### Logs

```json
{
  "level": "info",
  "msg": "agentruntime: hook execution failed",
  "hook": "create-crm-person",
  "agent": "tg-agent-tools",
  "event": "conversation.created",
  "error": "http 500"
}
```

### Health Checks

```bash
curl http://agent-runtime:8083/health
# {"status":"ok"}
```

## Troubleshooting

### Hook не срабатывает

1. Проверить `enabled = true`:
   ```sql
   SELECT * FROM lifecycle_hooks WHERE agent_name = 'tg-agent-tools';
   ```

2. Проверить логи `hook_executions`:
   ```sql
   SELECT * FROM hook_executions 
   WHERE hook_id = '...' 
   ORDER BY executed_at DESC 
   LIMIT 10;
   ```

3. Проверить template interpolation — логи показывают request_data

### Runtime unavailable (fallback to A2A)

1. Проверить agent-runtime запущен:
   ```bash
   curl http://agent-runtime:8083/health
   ```

2. Проверить tg-gateway env var:
   ```bash
   echo $TG_GATEWAY_RUNTIME_URL
   # должен быть http://agent-runtime:8083
   ```

3. Проверить network connectivity (k8s Service)

### CRM integration не работает

1. Проверить URL доступен из agent-runtime pod:
   ```bash
   kubectl exec -it agent-runtime-xxx -- curl http://twenty-crm:3000/health
   ```

2. Проверить response schema — `store_response` требует JSON

3. Проверить `conversation.metadata` после hook:
   ```sql
   SELECT metadata FROM conversations WHERE agent_name = 'tg-agent-tools' ORDER BY created_at DESC LIMIT 1;
   ```

## Roadmap

- [x] Phase 1-3: Conversation state + lifecycle hooks + HTTP/metadata actions
- [ ] Phase 4: Domain instructions tool
- [ ] Phase 5: Idle scheduler goroutine
- [ ] Phase 6: Deployment to prod
- [ ] Phase 7: Dogfood with tg-agent-tools

См. `docs/STATUS-agent-runtime.md` для детального статуса.
