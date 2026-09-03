# Plan: Agent Harness v2, Step 2 — Inbound Debounce

Owner's spec: человек пишет 4 сообщения подряд ("привет / слушай / у меня вопрос /
по регистрации") — сейчас это 4 запуска kagent и 4 ответа. Нужно: агрегировать в
ОДИН turn, но сохранить как 4 отдельных Message. В контексте агента они видны
по-отдельности со своими timestamps.

## Where the debouncer lives

tg-gateway, между getUpdates и dispatch (runtime/A2A). Аргументы:
- оно уже single-replica по жёсткому требованию Telegram (getUpdates race) —
  in-memory буфер не надо разделять между репликами;
- batching решений про КАНАЛ (как быстро пришли сообщения), не про reasoning —
  это transport-забота, не runtime-забота;
- Step 3 (interrupt) будет рулить active-run состоянием, которое живёт там же.

## Semantics (owner's numbers as defaults)

- `quiet_window` 2.5s: сообщение пришло -> таймер сброшен; dispatch когда 2.5s
  тишины. Каждое новое сообщение в batch сбрасывает таймер.
- `max_window` 8s: от первого сообщения batch'а — жёсткий потолок, dispatch
  несмотря на тишину (анти-зависание при непрерывном потоке).
- Offset getUpdates'а двигается сразу (как сегодня) — crash-safety не меняется:
  упавший под теряет batch так же, как сегодня теряет одиночное сообщение.
- По chat: batch ключ = (agentName, chatID). Разные чаты не смешиваются.
- Per-chat последовательность dispatch: пока batch чата X обрабатывается
  (agent call до 90s), следующий batch X ждёт. Это сохраняет сегодняшнюю
  семантику "строго по очереди на чат" и не плодит параллельные runs —
  параллельность/отмену целиком заберёт Step 3.

## Interface changes

### tggateway (new file debounce.go)

```go
type DebounceConfig struct{ QuietWindow, MaxWindow time.Duration }
type Debouncer struct{ ... }
func NewDebouncer(cfg DebounceConfig, dispatch func(key string, batch []TelegramUpdate)) *Debouncer
func (d *Debouncer) Enqueue(key string, u TelegramUpdate)
```

- Enqueue: append, reset quiet timer; если batch новый — ставится max-таймер.
- flush по любому таймеру: убрать batch, вызвать dispatch (в горутине, чтобы
  медленный агент не блокировал таймеры других чатов).
- runPoller сигнатура: + `deb *Debouncer` (nil = старое поведение немедленной
  обработки — все существующие тесты остаются с nil).
- Manager получает DebounceConfig (env: TG_GATEWAY_DEBOUNCE_QUIET_MS/MAX_MS,
  дефолты 2500/8000), создаёт debouncer на binding в startLocked.
- Fallback-путь (прямой A2A при недоступном runtime): identity-строка от
  последнего update + тексты batch'а построчно.

### runtime contract (batch вместо одиночного content)

RuntimeMessageRequest / server messageRequest / runtime MessageRequest:
`Messages []InboundMessage` (Content, ChannelMessageID, ThreadID, SourceSentAt,
ReplyToChannelMessageID). Одиночные поля Content/* остаются как fallback для
ручного curl-теста (оборачиваются в Messages из 1 элемента).

runtime.ProcessMessage:
- каждый message batch'а сохраняется ОТДЕЛЬНОЙ строкой (channel identity у
  каждого свой) — ровно как owner просил;
- message.received hooks — на каждое сообщение;
- A2A — ОДИН вызов на batch.

### Temporal rendering в A2A context (a2a.go)

Заодно закрывает кусок temporal awareness: user-сообщения с SourceSentAt
рендерятся как `[sent 22:41 UTC, 3m ago] текст`, а не голый текст. timestamps
сообщений batch'а поэтому видны агенту по-отдельности — то, ради чего всё
затевалось.

## Verification

1. Debouncer unit-тесты: batching 4 сообщений -> 1 dispatch; max_window
   срабатывает при непрерывном потоке; чаты не смешиваются; quiet reset работает.
2. Poller-интеграция: 3 update'а одного чата + fast debouncer -> 1 runtime
   вызов, 1 ответ пользователю.
3. runtime: Messages[] сохраняется построчно (DB-тест), один A2A вызов.
4. Полный go build / go vet / оба test-сюита на rig (go-build + dada-pg).
5. Регресс: 18 tggateway тестов с nil-debouncer не изменились.

## Риски

- Два batch'а одного чата не могут идти параллельно (per-chat mutex в dispatch) —
  иначе kagent contextId получил бы interleaving. Step 3 заменит это на
  честный run-tracking с отменой.
- Двойная агрегация album (media_group_id) и debounce — album'ы пока приходят
  как отдельные сообщения и склеятся debounce'ом естественно; честный
  media-group aggregator — в media-шаге (Step 6), не здесь.
