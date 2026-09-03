# Plan: Agent Harness v2, Step 3 — Interrupt / Cancel Stale Run

Owner's scenario: агент думает 15 секунд, пользователь через 5 секунд пишет
«а, стоп, я вообще из Казахстана». Старая модель: закончить мёртвый run и
прислать уже бессмысленный ответ. Нужен platform primitive:

```
message.received
    ↓
active agent run for this chat?
    ↓ yes → cancel / supersede / append (policy)
```

## Where it lives

tg-gateway, в processBatch. Run-tracking — transport-забота (кто отменяет
HTTP-вызов), reasoning остаётся в kagent. Per-chat mutex из Step 2 заменяется
per-chat run state: тот же замок, но с отменой вместо очереди.

## Semantics (одна политика в этом шаге: cancel_and_restart)

Один чат — один активный run. Приход нового сообщения чата, у которого есть
активный run:

1. Идём в per-chat lock (как в Step 2 — но вместо «ждать завершения» держим
   run state).
2. Если run активен — cancel ctx. Активный HTTP-вызов (runtime или A2A
   fallback) обрывается ошибкой ctx.Canceled.
3. Отменённый run: ответ НЕ отправляется (пользователю не прилетает мусор),
   в лог — одна debug-строка superseded.
4. Отменённые сообщения batch'а УЖЕ сохранены в conversation_messages
   рантаймом (side effect HTTP-вызова). Это осознанно: история — факт, ответ
   отменённого run'а — нет. interrupt policy позже может дописать stale-mark
   в metadata, сейчас — не нужно.
5. Новое сообщение начинает свой run с нуля (полный новый batch).

Edge: cancel приходит В ТОТ МОМЕНТ, когда HTTP-вызов уже вернулся и ответ
готов к отправке. Разгонка: отправка ответа и приём cancel под одним и тем же
per-chat state lock — либо ответ ушёл (run был done), либо run отменён до
отправки. Гонки «отправили после cancel» нет по построению.

Второй edge: cancel битого chat'а (run уже завершился) — no-op по построению
(см. state машину ниже).

## Interface changes

### tggateway (new file interrupt.go)

```go
type activeRun struct {
    cancel context.CancelFunc
    gen    int
}
type interruptState struct{ ... }  // per poller: map[chatID]*activeRun
func (s *interruptState) begin(chatID int64) (runCtx context.Context, done func(sent bool), superseded *activeRun)
func (s *interruptState) cancel(chatID int64) bool
```

- `begin`: под state-мьютексом. Если для чата есть активный run — cancel его
  ctx (superseded), заводим новый gen. Возвращаем:
  - runCtx — child of poller ctx + cancel;
  - done(sent bool) — обязательный вызов в defer processBatch; снимает active
    run только если gen совпадает (защита от снятия чужого run'а);
  - superseded — был ли прерван предыдущий (для лога).
- `cancel` вызывается в начале processBatch ДО begin: если активный run есть —
  cancel + ждать завершения (done-канал), чтобы новый batch не стартовал
  параллельно с умирающим HTTP-вызовом.

Упрощение против двухфазного begin/cancel: processBatch держит per-chat
мьютекс ВСЁ время run'а (как в Step 2), а interruptState нужен только чтобы
cancel предыдущего ctx. Тогда:
- new message → lock(chat) → cancel(prev.run) → prev.done ждёт release →
  begin новый run.

Минимальная версия: per-chat struct { mu, cancel, doneCh, gen }, begin под mu,
ожидание смерти предыдущего run'а на doneCh (с таймаутом — HTTP-вызов умирает
мгновенно по cancel, но на всякий случай 10s потолок).

### manager.go processBatch

- До startTyping: прервать предыдущий активный run этого чата (если есть) и
  дождаться его смерти.
- Свой run: ctx с cancel, defer done.
- Отправка ответа: только если run ещё жив (не отменён) — проверка под mu
  перед tg.SendMessage; отменённому run'у ответ не уходит.
- В processBatch ошибки: если err == context.Canceled — это НЕ failure streak
  (не дрогнуть failing/warned), это штатный supersede. Проверять ДО
  классификации ошибки.

### A2A fallback

Тот же ctx пробрасывается в a2a.SendWithContext — обрыв по cancel работает
одинаково для обоих путей.

## Verification

1. Unit: interruptState — begin/cancel/done gen-семантика, superseded flag.
2. Integration (processBatch уровень, fake runtime с блокировкой):
   - сообщение 1 в полёте (runtime блокирован) → сообщение 2 пришло →
     run 1 отменён (его ответ не отправлен), run 2 отработал, отправлен;
   - cancel ПОСЛЕ того как runtime вернулся, но до отправки → ответ не уходит;
   - отменённый run не портит failure streak (fallback message не прилетает).
3. Регресс: весь tggateway сюит зелёный.
4. build + vet.

## Риски

- Отменённый HTTP-вызов runtime оставляет в conversation_messages
  user-сообщение без assistant-ответа (рваная история). Осознанно для
  cancel_and_restart: kagent'у отдаётся полная история при следующем вызове,
  недостающий ответ — просто отсутствие поворота. Позже (по необходимости)
  можно помечать orphan'ы.
- Медленная смерть предыдущего run'а: cancel мгновенен для in-flight
  http.Client.Do (контекст проброшен), ожидание done — с потолком.
