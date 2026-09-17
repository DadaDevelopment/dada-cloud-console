package tggateway

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func deterministicPacing() *PacingConfig {
	return &PacingConfig{
		BaseQuiet:      10 * time.Second,
		MaxQuiet:       30 * time.Second,
		CharsPerMinute: 300,
		MinTyping:      2 * time.Second,
		MaxTyping:      20 * time.Second,
		JitterSigma:    0,
	}
}

func TestStimulusFactor_ClassifiesLikeCorpusPipeline(t *testing.T) {
	long := strings.Repeat("а ", 45) + "как это работает?"
	cases := []struct {
		name string
		u    TelegramUpdate
		want float64
	}{
		{"short reply", TelegramUpdate{Text: "да"}, stimulusShortReply},
		{"plain statement", TelegramUpdate{Text: "я вчера пополнил счёт на триста долларов"}, stimulusPlain},
		{"short question", TelegramUpdate{Text: "а сколько минимум?"}, stimulusShortQuestion},
		{"long question", TelegramUpdate{Text: long}, stimulusLongQuestion},
		{"technical", TelegramUpdate{Text: "не могу войти, ошибка при входе"}, stimulusTechnical},
		{"technical beats question", TelegramUpdate{Text: "почему не работает?"}, stimulusTechnical},
		{"objection reads as plain", TelegramUpdate{Text: "это какой-то развод"}, stimulusPlain},
		{"objection question stays plain", TelegramUpdate{Text: "а это не скам?"}, stimulusPlain},
		{"attachment", TelegramUpdate{Attachment: &TelegramAttachment{Kind: "photo"}}, stimulusAttachment},
		{"location", TelegramUpdate{HasLocation: true}, stimulusAttachment},
		{"empty", TelegramUpdate{}, stimulusPlain},
	}
	for _, c := range cases {
		if got := StimulusFactor(c.u); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestQuietFor_ScalesBaseByLastMessageAndClamps(t *testing.T) {
	p := deterministicPacing()
	if got := p.QuietFor(nil); got != 10*time.Second {
		t.Fatalf("empty batch: got %v want base", got)
	}
	batch := []TelegramUpdate{{Text: "не могу войти, ошибка"}, {Text: "да"}}
	if got := p.QuietFor(batch); got != 8*time.Second {
		t.Fatalf("last message decides: got %v want 8s", got)
	}
	batch = append(batch, TelegramUpdate{Text: strings.Repeat("x", 100) + "?"})
	if got := p.QuietFor(batch); got != 30*time.Second {
		t.Fatalf("long question capped at MaxQuiet: got %v want 30s", got)
	}
	p.BaseQuiet = 500 * time.Millisecond
	if got := p.QuietFor([]TelegramUpdate{{Text: "ок"}}); got != time.Second {
		t.Fatalf("floor: got %v want 1s", got)
	}
}

func TestTypingFor_ProportionalToLengthWithinClamps(t *testing.T) {
	p := deterministicPacing()
	if got := p.TypingFor("ок"); got != 2*time.Second {
		t.Fatalf("short reply: got %v want MinTyping", got)
	}
	if got := p.TypingFor(strings.Repeat("б", 50)); got != 10*time.Second {
		t.Fatalf("50 chars at 300 cpm: got %v want 10s", got)
	}
	if got := p.TypingFor(strings.Repeat("б", 5000)); got != 20*time.Second {
		t.Fatalf("long reply: got %v want MaxTyping", got)
	}
	p.CharsPerMinute = 0
	if got := p.TypingFor(strings.Repeat("б", 250)); got != 20*time.Second {
		t.Fatalf("zero cpm falls back to default 250: got %v want 20s (max)", got)
	}
}

func TestJitter_StaysInBandAndIsSafeConcurrently(t *testing.T) {
	p := deterministicPacing()
	p.JitterSigma = pacingJitterSigmaDefault
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[float64]bool{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				f := p.jitter()
				if f < pacingJitterFloor || f > pacingJitterCeiling {
					t.Errorf("jitter %v outside band", f)
				}
				mu.Lock()
				seen[f] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) < 100 {
		t.Fatalf("jitter looks constant: %d distinct values", len(seen))
	}
}

func TestPacingFromEnv_OffByDefaultAndOverridable(t *testing.T) {
	t.Setenv("TG_GATEWAY_PACING", "")
	if PacingFromEnv() != nil {
		t.Fatal("pacing must be off without TG_GATEWAY_PACING=1")
	}
	t.Setenv("TG_GATEWAY_PACING", "1")
	p := PacingFromEnv()
	if p == nil || p.BaseQuiet != pacingBaseQuietDefault || p.MaxQuiet != pacingMaxQuietDefault || p.CharsPerMinute != pacingCharsPerMinDefault || p.MaxTyping != pacingMaxTypingDefault {
		t.Fatalf("defaults: %+v", p)
	}
	t.Setenv("TG_GATEWAY_PACING_BASE_MS", "20000")
	t.Setenv("TG_GATEWAY_PACING_MAX_QUIET_MS", "45000")
	t.Setenv("TG_GATEWAY_PACING_MAX_TYPING_MS", "15000")
	t.Setenv("TG_GATEWAY_PACING_CPM", "180")
	p = PacingFromEnv()
	if p.BaseQuiet != 20*time.Second || p.MaxQuiet != 45*time.Second || p.MaxTyping != 15*time.Second || p.CharsPerMinute != 180 {
		t.Fatalf("overrides: %+v", p)
	}
}

func TestDebouncer_PacingDrivesQuietWindowAndLiftsMaxWindow(t *testing.T) {
	pacing := &PacingConfig{
		BaseQuiet:   time.Second,
		MaxQuiet:    1500 * time.Millisecond,
		JitterSigma: 0,
	}
	var mu sync.Mutex
	var dispatched [][]TelegramUpdate
	deb := NewDebouncer(DebounceConfig{QuietWindow: 5 * time.Second, MaxWindow: 10 * time.Millisecond, Pacing: pacing}, func(key string, batch []TelegramUpdate) {
		mu.Lock()
		defer mu.Unlock()
		dispatched = append(dispatched, batch)
	})
	defer deb.Close()
	if deb.cfg.MaxWindow != pacing.MaxQuiet {
		t.Fatalf("MaxWindow must be lifted to MaxQuiet, got %v", deb.cfg.MaxWindow)
	}
	if got := deb.quietFor([]TelegramUpdate{{Text: "да"}}); got != time.Second {
		t.Fatalf("paced quiet with 1s floor: got %v", got)
	}
	if got := deb.quietFor([]TelegramUpdate{{Text: strings.Repeat("x", 100) + "?"}}); got != pacing.MaxQuiet {
		t.Fatalf("paced quiet capped at MaxWindow: got %v", got)
	}

	start := time.Now()
	deb.Enqueue("agent=a chat=1", TelegramUpdate{UpdateID: 1, ChatID: 1, MessageID: 1, Text: "ок"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(dispatched)
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	elapsed := time.Since(start)
	mu.Lock()
	defer mu.Unlock()
	if len(dispatched) != 1 {
		t.Fatalf("expected 1 dispatch, got %d", len(dispatched))
	}
	if elapsed < time.Second {
		t.Fatalf("dispatched after %v, before the paced quiet window", elapsed)
	}
}

// Plan 5: the reply tail. The share defaults to zero, and a zero share must
// never add a delay -- that is the whole "unset == today" promise.
func TestTailDelay_DefaultsAddNothing(t *testing.T) {
	c := TailDelayFromEnv()
	if c.TailShare != 0 {
		t.Fatalf("default share must be zero, got %v", c.TailShare)
	}
	if c.Loc != time.UTC {
		t.Fatalf("default zone must be UTC, got %v", c.Loc)
	}
	for hour := 0; hour < 24; hour++ {
		now := time.Date(2026, 9, 16, hour, 30, 0, 0, time.UTC)
		for _, first := range []bool{true, false} {
			if d := c.ExtraDelay(now, first); d != 0 {
				t.Fatalf("hour %d first=%v: delay %v, want 0", hour, first, d)
			}
		}
	}
	var nilCfg *TailDelayConfig
	if d := nilCfg.ExtraDelay(time.Now(), false); d != 0 {
		t.Fatalf("nil config must be silent, got %v", d)
	}
}

func TestGatewayLocation_UnknownZoneFallsBackToUTC(t *testing.T) {
	t.Setenv("TG_GATEWAY_TZ", "Mars/Olympus")
	if loc := GatewayLocation(); loc != time.UTC {
		t.Fatalf("unknown zone must fall back to UTC, got %v", loc)
	}
	t.Setenv("TG_GATEWAY_TZ", "Europe/Moscow")
	if loc := GatewayLocation(); loc.String() != "Europe/Moscow" {
		t.Fatalf("zone = %v, want Europe/Moscow", loc)
	}
}

// A held reply lives in a goroutine, so the hold has a hard ceiling no
// manifest can raise.
func TestTailDelay_MaxIsCappedAtFifteenMinutes(t *testing.T) {
	t.Setenv("TG_GATEWAY_PACING_TAIL_SHARE", "1")
	t.Setenv("TG_GATEWAY_PACING_TAIL_MAX_MS", "3600000")
	c := TailDelayFromEnv()
	if c.TailMax != tailDelayMaxCap {
		t.Fatalf("TailMax = %v, want the %v cap", c.TailMax, tailDelayMaxCap)
	}
	noon := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 200; i++ {
		if d := c.ExtraDelay(noon, false); d > tailDelayMaxCap {
			t.Fatalf("delay %v exceeds the cap %v", d, tailDelayMaxCap)
		}
	}
}

func TestTailDelay_ShareIsClampedToAProbability(t *testing.T) {
	t.Setenv("TG_GATEWAY_PACING_TAIL_SHARE", "30")
	if got := TailDelayFromEnv().TailShare; got != 1 {
		t.Fatalf("share = %v, want it clamped to 1", got)
	}
	t.Setenv("TG_GATEWAY_PACING_TAIL_SHARE", "-2")
	if got := TailDelayFromEnv().TailShare; got != 0 {
		t.Fatalf("share = %v, want it clamped to 0", got)
	}
	t.Setenv("TG_GATEWAY_PACING_TAIL_SHARE", "не число")
	if got := TailDelayFromEnv().TailShare; got != 0 {
		t.Fatalf("share = %v, want the default", got)
	}
}

func TestTailDelay_OnlyInTheWorkingDayAndNeverOnTheFirstMessage(t *testing.T) {
	c := &TailDelayConfig{TailShare: 1, TailMin: tailDelayMinDefault, TailMax: tailDelayMaxCap, Loc: time.UTC}

	noon := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	if d := c.ExtraDelay(noon, false); d < tailDelayMinDefault || d > tailDelayMaxCap {
		t.Fatalf("midday delay %v outside [5m, 15m]", d)
	}
	if d := c.ExtraDelay(noon, true); d != 0 {
		t.Fatalf("the first message of a dialogue must never wait, got %v", d)
	}
	for _, hour := range []int{3, 9, 22, 23} {
		at := time.Date(2026, 9, 16, hour, 0, 0, 0, time.UTC)
		if d := c.ExtraDelay(at, false); d != 0 {
			t.Fatalf("hour %d is outside the 10-22 window, got %v", hour, d)
		}
	}
}

func TestTailDelay_ShareIsRespectedAcrossManyTurns(t *testing.T) {
	c := &TailDelayConfig{TailShare: 0.3, TailMin: tailDelayMinDefault, TailMax: tailDelayMaxCap, Loc: time.UTC}
	noon := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	delayed := 0
	const n = 2000
	for i := 0; i < n; i++ {
		if c.ExtraDelay(noon, false) > 0 {
			delayed++
		}
	}
	share := float64(delayed) / n
	if share < 0.24 || share > 0.36 {
		t.Fatalf("delayed share %.3f, want about 0.3", share)
	}
}

// Plan 6.3: the bare-acknowledgement class. It is read off the text, so a
// one-message batch is not enough to be an ack and three words of real
// content are not one either.
func TestIsBareAck_ReadsTheTextNotTheBatch(t *testing.T) {
	cases := []struct {
		name string
		u    TelegramUpdate
		want bool
	}{
		{"ок", TelegramUpdate{Text: "ок"}, true},
		{"case and bracket", TelegramUpdate{Text: "Ок)"}, true},
		{"trailing dot", TelegramUpdate{Text: "окей."}, true},
		{"thumbs up", TelegramUpdate{Text: "👍"}, true},
		{"two ack words", TelegramUpdate{Text: "да, понял"}, true},
		{"ack with emoji", TelegramUpdate{Text: "спасибо 🙏"}, true},
		{"spaces around", TelegramUpdate{Text: "  готово  "}, true},
		{"question is never an ack", TelegramUpdate{Text: "ок?"}, false},
		{"three words of content", TelegramUpdate{Text: "уже пополнил счёт"}, false},
		{"ack plus content", TelegramUpdate{Text: "спасибо большое"}, false},
		{"four words", TelegramUpdate{Text: "да да да да"}, false},
		{"bare punctuation is not an emoji", TelegramUpdate{Text: "..."}, false},
		{"empty", TelegramUpdate{}, false},
		{"attachment", TelegramUpdate{Text: "ок", Attachment: &TelegramAttachment{Kind: "photo"}}, false},
		{"location", TelegramUpdate{Text: "ок", HasLocation: true}, false},
	}
	for _, c := range cases {
		if got := IsBareAck(c.u); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// The unset variable is the whole compatibility promise: every class keeps
// today's window byte for byte. Jitter is zeroed so the two configs are
// comparable at all.
func TestQuietFor_UnsetAckVariableChangesNothing(t *testing.T) {
	batches := [][]TelegramUpdate{
		nil,
		{{Text: "ок"}},
		{{Text: "👍"}},
		{{Text: "да"}},
		{{Text: "я вчера пополнил счёт на триста долларов"}},
		{{Text: "а сколько минимум?"}},
		{{Text: strings.Repeat("а ", 45) + "как это работает?"}},
		{{Text: "не могу войти, ошибка при входе"}},
		{{Attachment: &TelegramAttachment{Kind: "photo"}}},
		{{Text: "не могу войти"}, {Text: "ок"}},
	}
	base, withAck := deterministicPacing(), deterministicPacing()
	withAck.AckQuiet = 0
	for i, batch := range batches {
		if got, want := withAck.QuietFor(batch), base.QuietFor(batch); got != want {
			t.Errorf("batch %d: got %v want %v", i, got, want)
		}
	}
}

func TestQuietFor_AckWindowOnlyShortensTheAckClass(t *testing.T) {
	base, withAck := deterministicPacing(), deterministicPacing()
	withAck.AckQuiet = 6 * time.Second

	// The ack class: 10s x 0.8 = 8s today, 6s with the variable.
	ack := []TelegramUpdate{{Text: "ок"}}
	if got := base.QuietFor(ack); got != 8*time.Second {
		t.Fatalf("today's ack window: got %v want 8s", got)
	}
	if got := withAck.QuietFor(ack); got != 6*time.Second {
		t.Fatalf("capped ack window: got %v want 6s", got)
	}

	// Every other class is untouched, including "да", which shares the short
	// reply factor but is followed by a question here.
	for _, batch := range [][]TelegramUpdate{
		{{Text: "ок?"}},
		{{Text: "я вчера пополнил счёт на триста долларов"}},
		{{Text: "не могу войти, ошибка при входе"}},
		{{Text: strings.Repeat("а ", 45) + "как это работает?"}},
		{{Attachment: &TelegramAttachment{Kind: "photo"}}},
	} {
		if got, want := withAck.QuietFor(batch), base.QuietFor(batch); got != want {
			t.Errorf("%q: got %v want %v", batch[0].Text, got, want)
		}
	}
}

// Two clamps in one: the variable may never lengthen a wait, and it may never
// take the window below the five-second floor the debounce needs.
func TestQuietFor_AckWindowNeverLengthensAndKeepsTheFloor(t *testing.T) {
	p := deterministicPacing()
	ack := []TelegramUpdate{{Text: "👍"}}

	p.AckQuiet = 20 * time.Second
	if got := p.QuietFor(ack); got != 8*time.Second {
		t.Fatalf("a larger ack window must not lengthen the wait: got %v want 8s", got)
	}

	p.AckQuiet = time.Second
	if got := p.QuietFor(ack); got != ackQuietFloor {
		t.Fatalf("below the floor: got %v want %v", got, ackQuietFloor)
	}

	// The floor is a ceiling on the ack window, not a new minimum for the
	// class: a base that already pays less than five seconds keeps paying it.
	p.BaseQuiet = 2 * time.Second
	if got := p.QuietFor(ack); got != 1600*time.Millisecond {
		t.Fatalf("short base must stay short: got %v want 1.6s", got)
	}
}

func TestPacingFromEnv_AckWindowIsOffUnlessSet(t *testing.T) {
	t.Setenv("TG_GATEWAY_PACING", "1")
	if got := PacingFromEnv().AckQuiet; got != 0 {
		t.Fatalf("unset variable must leave AckQuiet zero, got %v", got)
	}
	t.Setenv("TG_GATEWAY_PACING_ACK_MS", "5000")
	if got := PacingFromEnv().AckQuiet; got != 5*time.Second {
		t.Fatalf("AckQuiet = %v, want 5s", got)
	}
}

// The reason the floor exists: the quiet window is also the debounce window.
// The client writes "ок", then a second message four seconds later, and both
// must still arrive as one turn.
func TestDebouncer_AckWindowStillGluesAFourSecondGap(t *testing.T) {
	pacing := deterministicPacing()
	pacing.AckQuiet = time.Second // lifted to the 5s floor
	var mu sync.Mutex
	var dispatched [][]TelegramUpdate
	deb := NewDebouncer(DebounceConfig{QuietWindow: 2 * time.Second, MaxWindow: time.Minute, Pacing: pacing}, func(key string, batch []TelegramUpdate) {
		mu.Lock()
		defer mu.Unlock()
		dispatched = append(dispatched, batch)
	})
	defer deb.Close()
	if got := deb.quietFor([]TelegramUpdate{{Text: "ок"}}); got != ackQuietFloor {
		t.Fatalf("paced ack window: got %v want %v", got, ackQuietFloor)
	}

	const key = "agent=a chat=1"
	deb.Enqueue(key, TelegramUpdate{UpdateID: 1, ChatID: 1, MessageID: 1, Text: "ок"})
	time.Sleep(4 * time.Second)
	mu.Lock()
	early := len(dispatched)
	mu.Unlock()
	if early != 0 {
		t.Fatalf("the batch flushed inside the four-second gap: %d dispatches", early)
	}
	deb.Enqueue(key, TelegramUpdate{UpdateID: 2, ChatID: 1, MessageID: 2, Text: "и что дальше делать"})
	deb.flush(key)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(dispatched)
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(dispatched) != 1 {
		t.Fatalf("expected 1 dispatch, got %d", len(dispatched))
	}
	if len(dispatched[0]) != 2 {
		t.Fatalf("both messages must land in one batch, got %d", len(dispatched[0]))
	}
}
