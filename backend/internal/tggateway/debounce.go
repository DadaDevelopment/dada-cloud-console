package tggateway

import (
	"sync"
	"time"
)

// DebounceDefaults are the owner's numbers from the harness review: a human
// fires off "привет / слушай / у меня вопрос / по регистрации" a couple of
// seconds apart, and each of those used to become its own kagent run and its
// own reply.
const (
	DebounceQuietDefault = 2500 * time.Millisecond
	DebounceMaxDefault   = 8 * time.Second
)

// DebounceConfig sizes the two windows. QuietWindow is how long a batch waits
// after its LAST message before dispatching (every new message resets it);
// MaxWindow is the hard cap measured from the batch's FIRST message, so a
// continuous stream cannot hold a chat hostage forever.
type DebounceConfig struct {
	QuietWindow time.Duration
	MaxWindow   time.Duration
}

// debouncedBatch is one chat's open buffer: the messages so far plus the two
// timers. quietTimer is rebuilt on every Enqueue; maxTimer is armed once, at
// batch creation, and flushes regardless of quiet resets.
type debouncedBatch struct {
	mu         sync.Mutex
	updates    []TelegramUpdate
	quietTimer *time.Timer
	maxTimer   *time.Timer
}

// Debouncer aggregates rapid-fire updates per chat key and hands each finished
// batch to dispatch in its own goroutine, so one slow agent call never delays
// another chat's timers. A Debouncer is created per binding inside
// Manager.startLocked and inherits the poller's single-replica assumption: the
// in-memory buffer is authoritative because two pollers on one bot token are
// impossible by Telegram's getUpdates contract.
type Debouncer struct {
	cfg      DebounceConfig
	dispatch func(key string, batch []TelegramUpdate)

	mu      sync.Mutex
	batches map[string]*debouncedBatch
	closed  bool
}

// NewDebouncer wires an aggregation layer. Zero config values fall back to the
// defaults, and MaxWindow below QuietWindow is clamped up to QuietWindow.
func NewDebouncer(cfg DebounceConfig, dispatch func(key string, batch []TelegramUpdate)) *Debouncer {
	if cfg.QuietWindow <= 0 {
		cfg.QuietWindow = DebounceQuietDefault
	}
	if cfg.MaxWindow <= 0 {
		cfg.MaxWindow = DebounceMaxDefault
	}
	if cfg.MaxWindow < cfg.QuietWindow {
		cfg.MaxWindow = cfg.QuietWindow
	}
	return &Debouncer{
		cfg:      cfg,
		dispatch: dispatch,
		batches:  map[string]*debouncedBatch{},
	}
}

// Enqueue appends one update to its chat's batch and resets the quiet timer.
// The first update of a new batch also arms the max-window timer, which
// flushes regardless of further quiet resets.
func (d *Debouncer) Enqueue(key string, u TelegramUpdate) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	batch, ok := d.batches[key]
	if !ok {
		batch = &debouncedBatch{}
		d.batches[key] = batch
		batch.mu.Lock()
		batch.updates = append(batch.updates, u)
		batch.mu.Unlock()

		key := key
		batch.maxTimer = time.AfterFunc(d.cfg.MaxWindow, func() {
			d.flush(key)
		})
		batch.quietTimer = time.AfterFunc(d.cfg.QuietWindow, func() {
			d.flush(key)
		})
		d.mu.Unlock()
		return
	}

	batch.mu.Lock()
	batch.updates = append(batch.updates, u)
	if batch.quietTimer != nil {
		batch.quietTimer.Stop()
	}
	k := key
	batch.quietTimer = time.AfterFunc(d.cfg.QuietWindow, func() {
		d.flush(k)
	})
	batch.mu.Unlock()
	d.mu.Unlock()
}

// flush removes the batch under d.mu first, so a racing Enqueue either fully
// joins the batch before removal or opens a fresh batch afterwards -- a
// message can never be silently dropped between snapshot and dispatch. The
// stopped timers will fire into flush and find no batch, which is a no-op.
func (d *Debouncer) flush(key string) {
	d.mu.Lock()
	batch, ok := d.batches[key]
	if !ok {
		d.mu.Unlock()
		return
	}
	delete(d.batches, key)

	batch.mu.Lock()
	updates := batch.updates
	batch.updates = nil
	if batch.quietTimer != nil {
		batch.quietTimer.Stop()
	}
	if batch.maxTimer != nil {
		batch.maxTimer.Stop()
	}
	batch.mu.Unlock()
	d.mu.Unlock()

	if len(updates) == 0 {
		return
	}
	go d.dispatch(key, updates)
}

// Close stops every pending timer and drops open batches. In-flight dispatch
// goroutines are not waited on, matching the poller's own shutdown semantics.
func (d *Debouncer) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	for key, batch := range d.batches {
		batch.mu.Lock()
		if batch.quietTimer != nil {
			batch.quietTimer.Stop()
		}
		if batch.maxTimer != nil {
			batch.maxTimer.Stop()
		}
		batch.mu.Unlock()
		delete(d.batches, key)
	}
}
