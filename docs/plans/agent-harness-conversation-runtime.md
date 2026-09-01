# Agent Harness: Conversation Runtime, Lifecycle Hooks, Domain Instructions

## Цель

Построить platform-owned conversation runtime между tg-gateway и kagent, который:
1. Хранит conversation state (история, metadata, external entities)
2. Выполняет lifecycle hooks (trigger → action) без участия LLM
3. Предоставляет агенту domain instructions по запросу
4. **Не превращает агента в state machine** — модель сама решает, что делать дальше

## Текущая архитектура

```
Telegram → tg-gateway (long poll) → A2A JSON-RPC → kagent Agent
                ↓
           tg_bindings (agent_name ↔ bot_token)
```

Проблемы:
- Нет conversation state — каждый message stateless
- Нет mapping telegram chat/user ↔ CRM person
- Нет lifecycle automation (conversation.created → create CRM person)
- Нет domain instructions — всё в одном system prompt
- История сообщений не хранится платформой

## Целевая архитектура

```
Telegram → tg-gateway → Agent Runtime → kagent Agent
                            ↓
                    Conversation State
                    Lifecycle Hooks
                    Domain Instructions
                    Message History
                            ↓
                    External Systems (CRM, etc)
```

## Компоненты

### 1. Conversation State (DB schema)

```sql
CREATE TABLE conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_name TEXT NOT NULL,
    channel TEXT NOT NULL,  -- 'telegram', 'slack', etc
    external_id TEXT NOT NULL,  -- telegram chat_id, slack channel_id
    actor_external_id TEXT,  -- telegram user_id
    actor_username TEXT,
    actor_metadata JSONB,
    metadata JSONB DEFAULT '{}',  -- CRM person_id, custom fields
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(agent_name, channel, external_id)
);

CREATE TABLE conversation_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL,  -- 'user', 'assistant', 'system'
    content TEXT NOT NULL,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_conversation_messages_conversation ON conversation_messages(conversation_id, created_at);
CREATE INDEX idx_conversations_agent_status ON conversations(agent_name, status);
CREATE INDEX idx_conversations_updated ON conversations(updated_at) WHERE status = 'active';
```

### 2. Lifecycle Hooks (DB schema + execution engine)

```sql
CREATE TABLE lifecycle_hooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_name TEXT NOT NULL,
    trigger_event TEXT NOT NULL,  -- 'conversation.created', 'message.received', 'agent.run.completed', 'conversation.idle'
    trigger_config JSONB DEFAULT '{}',  -- idle_minutes для conversation.idle
    action_type TEXT NOT NULL,  -- 'http', 'metadata', 'schedule'
    action_config JSONB NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_lifecycle_hooks_agent_event ON lifecycle_hooks(agent_name, trigger_event) WHERE enabled = true;
```

Hook config примеры:

```json
{
  "agent_name": "tg-agent-tools",
  "trigger_event": "conversation.created",
  "action_type": "http",
  "action_config": {
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
  }
}
```

```json
{
  "agent_name": "tg-agent-tools",
  "trigger_event": "conversation.idle",
  "trigger_config": {"idle_minutes": 30},
  "action_type": "schedule",
  "action_config": {
    "agent_message": "следующие шаги по этому клиенту?"
  }
}
```

### 3. Domain Instructions

Простое file-based хранилище в git, агент запрашивает через новый tool:

```
gitops-repo/agents/{agent-name}/domains/
  jurisdiction.md
  kyc.md
  registration.md
  objections.md
  handoff.md
```

Агент получает новый tool в system prompt:
```
get_domain_instruction(domain: str) -> str
```

Модель сама решает, когда вызвать. Platform просто отдаёт содержимое файла.

### 4. Agent Runtime Service

Новый Go-сервис `backend/internal/agentruntime`:

```go
type Runtime struct {
    store ConversationStore
    hooks HookExecutor
    a2a   A2AClient
    domains DomainProvider
}

func (r *Runtime) ProcessMessage(ctx context.Context, req MessageRequest) (MessageResponse, error) {
    // 1. Resolve or create conversation
    conv, created := r.store.GetOrCreateConversation(ctx, req.AgentName, req.Channel, req.ExternalID, req.Actor)
    
    // 2. Execute conversation.created hooks if created
    if created {
        r.hooks.Execute(ctx, "conversation.created", conv)
    }
    
    // 3. Execute message.received hooks
    r.hooks.Execute(ctx, "message.received", conv, req.Content)
    
    // 4. Save user message
    r.store.SaveMessage(ctx, conv.ID, "user", req.Content)
    
    // 5. Build context: history + system prompt with domain tool
    history := r.store.GetRecentMessages(ctx, conv.ID, 20)
    context := r.buildContext(history, conv.Metadata)
    
    // 6. Call kagent A2A
    reply, err := r.a2a.Send(ctx, req.AgentName, context)
    if err != nil {
        return MessageResponse{}, err
    }
    
    // 7. Save assistant message
    r.store.SaveMessage(ctx, conv.ID, "assistant", reply)
    
    // 8. Execute agent.run.completed hooks
    r.hooks.Execute(ctx, "agent.run.completed", conv)
    
    // 9. Update conversation.updated_at
    r.store.Touch(ctx, conv.ID)
    
    return MessageResponse{Text: reply}, nil
}
```

### 5. Hook Executor

```go
type HookExecutor interface {
    Execute(ctx context.Context, event string, conv Conversation, extra ...interface{}) error
}

type hookExecutor struct {
    db *pgxpool.Pool
    http *http.Client
}

func (h *hookExecutor) Execute(ctx context.Context, event string, conv Conversation, extra ...interface{}) error {
    hooks := h.listHooks(ctx, conv.AgentName, event)
    for _, hook := range hooks {
        switch hook.ActionType {
        case "http":
            h.executeHTTP(ctx, hook, conv)
        case "metadata":
            h.executeMetadata(ctx, hook, conv)
        case "schedule":
            h.executeSchedule(ctx, hook, conv)
        }
    }
    return nil
}

func (h *hookExecutor) executeHTTP(ctx context.Context, hook Hook, conv Conversation) error {
    // Template interpolation: {{ actor.username }}, {{ conversation.metadata.crm_person_id }}
    body := h.interpolate(hook.ActionConfig["body"], conv)
    
    req, _ := http.NewRequestWithContext(ctx, hook.ActionConfig["method"], hook.ActionConfig["url"], bytes.NewReader(body))
    resp, err := h.http.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    
    // Store response fields back into conversation.metadata
    if storeConfig, ok := hook.ActionConfig["store_response"].(map[string]string); ok {
        var respData map[string]interface{}
        json.NewDecoder(resp.Body).Decode(&respData)
        for metaKey, jsonPath := range storeConfig {
            value := h.extractJSONPath(respData, jsonPath)
            h.updateMetadata(ctx, conv.ID, metaKey, value)
        }
    }
    return nil
}
```

### 6. Idle Scheduler

Отдельная горутина, которая каждые N минут сканирует `conversations`:

```go
func (r *Runtime) RunIdleScheduler(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            r.checkIdle(ctx)
        }
    }
}

func (r *Runtime) checkIdle(ctx context.Context) {
    hooks := r.hooks.ListIdleHooks(ctx)
    for _, hook := range hooks {
        idleMinutes := hook.TriggerConfig["idle_minutes"].(int)
        threshold := time.Now().Add(-time.Duration(idleMinutes) * time.Minute)
        
        convs := r.store.ListIdleConversations(ctx, hook.AgentName, threshold)
        for _, conv := range convs {
            // Send follow-up message as agent
            msg := hook.ActionConfig["agent_message"].(string)
            reply, _ := r.a2a.Send(ctx, conv.AgentName, msg)
            r.store.SaveMessage(ctx, conv.ID, "assistant", reply)
            
            // Send to telegram
            r.sendToChannel(ctx, conv, reply)
        }
    }
}
```

## Интеграция с tg-gateway

Меняем `tg-gateway` так, чтобы он вызывал agent runtime вместо прямого A2A:

```diff
-reply, err := a2a.Send(ctx, b.AgentName, withTelegramIdentity(u))
+reply, err := runtime.ProcessMessage(ctx, MessageRequest{
+    AgentName: b.AgentName,
+    Channel: "telegram",
+    ExternalID: fmt.Sprintf("%d", u.ChatID),
+    Actor: Actor{
+        ExternalID: fmt.Sprintf("%d", u.UserID),
+        Username: u.Username,
+        FirstName: u.FirstName,
+    },
+    Content: u.Text,
+})
```

## Domain Instructions Tool

Добавляем в system prompt агента:

```markdown
You have access to domain-specific instructions via get_domain_instruction(domain: str).

Available domains:
- jurisdiction: jurisdictional requirements and regulations
- kyc: KYC/AML procedures
- registration: account registration flow
- objections: handling client objections
- handoff: escalation to human operator

Call get_domain_instruction when you need specialized knowledge for a specific area.
```

Реализация в agent runtime:

```go
func (r *Runtime) GetDomainInstruction(ctx context.Context, agentName, domain string) (string, error) {
    path := fmt.Sprintf("gitops-repo/agents/%s/domains/%s.md", agentName, domain)
    content, err := os.ReadFile(path)
    if err != nil {
        return "", fmt.Errorf("domain not found: %s", domain)
    }
    return string(content), nil
}
```

Этот tool expose через kagent MCP server или напрямую в A2A envelope.

## План реализации

### Phase 1: Conversation State (foundation)
1. Миграции для `conversations`, `conversation_messages`
2. `backend/internal/agentruntime/store.go` — ConversationStore interface + pgStore impl
3. Юнит-тесты для store
4. **Smoke test**: создать conversation, сохранить messages, прочитать историю

### Phase 2: Agent Runtime Service (core)
1. `backend/internal/agentruntime/runtime.go` — ProcessMessage без hooks пока
2. Интеграция с A2A client (переиспользовать из tggateway)
3. Юнит-тесты с fake store + fake A2A
4. **Integration test**: полный цикл message → conversation → A2A → save reply

### Phase 3: Lifecycle Hooks (automation)
1. Миграция для `lifecycle_hooks`
2. `backend/internal/agentruntime/hooks.go` — HookExecutor
3. Реализация HTTP action с template interpolation
4. Реализация metadata action
5. Юнит-тесты для каждого action type
6. **Dogfood test**: conversation.created → POST /crm/person → store response.id

### Phase 4: Domain Instructions (dynamic context)
1. File-based provider: `backend/internal/agentruntime/domains.go`
2. MCP tool registration для get_domain_instruction
3. Seed example domains в gitops-repo
4. **Manual test**: агент вызывает get_domain_instruction("kyc") и получает содержимое

### Phase 5: Idle Scheduler (follow-up automation)
1. `backend/internal/agentruntime/scheduler.go` — idle checker goroutine
2. Schedule action type в HookExecutor
3. Интеграция с channel gateway (отправка в Telegram)
4. **E2E test**: conversation idle 30min → follow-up message появляется в чате

### Phase 6: tg-gateway Integration (production path)
1. Добавить agent runtime client в tg-gateway
2. Заменить прямой A2A на runtime.ProcessMessage
3. Сохранить backward compatibility (fallback на A2A если runtime unavailable)
4. **Production test**: реальный Telegram бот использует новый runtime

### Phase 7: agent-sandbox Dogfood (real scenario)
1. Создать lifecycle hooks для tg-agent-tools
2. Создать domain instructions (jurisdiction, kyc, etc)
3. Обкатать на тестовом чате
4. **Acceptance**: новый пользователь → CRM person создаётся автоматически, агент запрашивает domain по необходимости

## Риски и вопросы

1. **A2A message format с историей** — как передать conversation history в kagent?
   - Вариант 1: расширить A2A params.message.history
   - Вариант 2: system message в начале с историей
   - Вариант 3: kagent уже поддерживает multi-turn через свой state?

2. **Domain instruction tool** — как expose через kagent?
   - Вариант 1: новый MCP server platform-tools с get_domain_instruction
   - Вариант 2: встроить в A2A envelope как metadata
   - Вариант 3: отдельный HTTP endpoint, который агент вызывает

3. **Idle scheduler scale** — сканирование всех conversations каждые 5 минут при росте?
   - Митигация: индекс по (updated_at, status='active')
   - Альтернатива: scheduled_tasks таблица с отдельными рядами на каждый follow-up

4. **Hook execution failures** — retry? dead letter queue?
   - Phase 1: best-effort, логируем ошибку
   - Phase 2: hook_executions таблица с retry logic

5. **Demo tg-agent-tools завтра** — не трогать production пока платформа не готова
   - Разработка в agent-sandbox project
   - Отдельный тестовый бот для обкатки
   - Production migration только после full acceptance

## Критерий успеха

TG-бот первой линии (tg-agent-tools):
- ✅ Новый пользователь пишет → conversation создаётся → CRM person создаётся через hook
- ✅ Telegram chat_id ↔ conversation ↔ CRM person_id mapping работает идемпотентно
- ✅ История сообщений хранится platform-side
- ✅ Агент может запросить domain instruction когда нужно (через tool call)
- ✅ 30 минут idle → follow-up message от агента
- ✅ Агент остаётся свободным agentic loop, не state machine
- ✅ Всё это работает без отдельного application backend/pod
