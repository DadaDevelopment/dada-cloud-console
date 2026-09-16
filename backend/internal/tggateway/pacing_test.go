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

// Plan 5: the tail and the night window. Both knobs default to zero, and a
// zero knob must never add a delay -- that is the whole "unset == today"
// promise of this change.
func TestTailNight_DefaultsAddNothing(t *testing.T) {
	c := TailNightFromEnv()
	if c.TailShare != 0 || c.NightMorningP != 0 {
		t.Fatalf("defaults must be zero, got tail=%v night=%v", c.TailShare, c.NightMorningP)
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
	var nilCfg *TailNightConfig
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

func TestTailNight_TailOnlyInTheWorkingDayAndNeverOnTheFirstMessage(t *testing.T) {
	c := &TailNightConfig{TailShare: 1, TailMin: tailDelayMinDefault, TailMax: tailDelayMaxDefault, Loc: time.UTC}

	noon := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	if d := c.ExtraDelay(noon, false); d < tailDelayMinDefault || d > tailDelayMaxDefault {
		t.Fatalf("midday delay %v outside [5m, 15m]", d)
	}
	if d := c.ExtraDelay(noon, true); d != 0 {
		t.Fatalf("the first message of a dialogue must never wait, got %v", d)
	}
	for _, hour := range []int{9, 22} {
		at := time.Date(2026, 9, 16, hour, 0, 0, 0, time.UTC)
		if d := c.ExtraDelay(at, false); d != 0 {
			t.Fatalf("hour %d is outside the 10-22 window, got %v", hour, d)
		}
	}
}

func TestTailNight_NightMessageIsHeldUntilTheMorningWindow(t *testing.T) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skipf("zone database unavailable: %v", err)
	}
	c := &TailNightConfig{NightMorningP: 1, TailShare: 1, Loc: msk}

	for _, tc := range []struct {
		name     string
		at       time.Time
		wantDate int
	}{
		{"after midnight waits for this morning", time.Date(2026, 9, 16, 3, 0, 0, 0, msk), 16},
		{"late evening waits for tomorrow", time.Date(2026, 9, 16, 23, 30, 0, 0, msk), 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := c.ExtraDelay(tc.at, false)
			if d <= 0 {
				t.Fatalf("night message must be held, got %v", d)
			}
			at := tc.at.Add(d).In(msk)
			if at.Day() != tc.wantDate || at.Hour() < morningHour || at.Hour() >= morningHourLate {
				t.Fatalf("reply lands at %v, want day %d between %d:00 and %d:00", at, tc.wantDate, morningHour, morningHourLate)
			}
		})
	}
}

func TestTailNight_ShareIsRespectedAcrossManyTurns(t *testing.T) {
	c := &TailNightConfig{TailShare: 0.3, TailMin: tailDelayMinDefault, TailMax: tailDelayMaxDefault, Loc: time.UTC}
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
