package tggateway

import (
	"math"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// PacingConfig makes the bot's timing look like a person's instead of a
// server's. Without it the reply lands seconds after the message with
// "typing" shown from the first millisecond, which the owner's maintainer
// flagged as the loudest machine tell after reply length.
//
// Two knobs, both measured on the live operator line
// (call_center/docs/17-ритм-и-догоны.md §7):
//
//   - quiet: how long the chat stays silent after the client's last message
//     before anything visible happens. BaseQuiet times a stimulus factor
//     (a long question earns x3.5, "да" earns x0.8) times a log-normal
//     jitter, so no three-second corridor collects more than 15% of replies.
//     It is applied as the debounce quiet window, so a client still typing
//     keeps extending it, exactly like a person who has not looked yet.
//   - typing: how long "typing..." shows before the send, proportional to
//     the reply's length at CharsPerMinute. Generation time is not shown as
//     typing at all: a person does not type while thinking. A newer batch
//     superseding the run cuts this pause short instead of dropping the
//     reply: the runtime already saved it to the transcript.
//
// Read receipts are deliberately absent: the Bot API only marks messages
// read through readBusinessMessage on a business connection, a plain bot
// cannot control the check marks in its own chats.
type PacingConfig struct {
	BaseQuiet      time.Duration
	MaxQuiet       time.Duration
	CharsPerMinute int
	MinTyping      time.Duration
	MaxTyping      time.Duration
	JitterSigma    float64
	GapMin         time.Duration
	GapMax         time.Duration

	mu   sync.Mutex
	rand *rand.Rand
}

const (
	pacingBaseQuietDefault   = 12 * time.Second
	pacingMaxQuietDefault    = 90 * time.Second
	pacingCharsPerMinDefault = 250
	pacingMinTypingDefault   = 3 * time.Second
	pacingMaxTypingDefault   = 60 * time.Second
	pacingJitterSigmaDefault = 0.45
	pacingJitterFloor        = 0.4
	pacingJitterCeiling      = 2.5
	// pacingGapMin/Max bound the silence BETWEEN the messages of one split
	// turn (plan 3.1). It is not the quiet window: the client is not typing,
	// the bot is finishing a thought, and the live line's own within-series
	// gaps sit in this range.
	pacingGapMinDefault = 10 * time.Second
	pacingGapMaxDefault = 40 * time.Second
)

var (
	rePacingTechnical = regexp.MustCompile(`(?i)ошибк|не работает|не открыва|не приход|не могу|отклонил|не груз|vpn|впн|недоступ|не пускает|не заход|не получается войти|зависа`)
	rePacingObjection = regexp.MustCompile(`(?i)не доверя|обман|скам|развод|дорог|нет денег|нету денег|подумаю|не интересн|боюсь|риск|гарант|отзыв|лохотрон|мошен|сомнева|жалоб`)
)

const (
	stimulusLongQuestion  = 3.5
	stimulusTechnical     = 2.3
	stimulusAttachment    = 2.3
	stimulusShortQuestion = 1.3
	stimulusPlain         = 1.0
	stimulusShortReply    = 0.8
)

// PacingFromEnv reads TG_GATEWAY_PACING=1 plus the optional size overrides.
// nil means pacing is off and the gateway behaves as before.
func PacingFromEnv() *PacingConfig {
	if os.Getenv("TG_GATEWAY_PACING") != "1" {
		return nil
	}
	p := &PacingConfig{
		BaseQuiet:      envDurationMS("TG_GATEWAY_PACING_BASE_MS", pacingBaseQuietDefault),
		MaxQuiet:       envDurationMS("TG_GATEWAY_PACING_MAX_QUIET_MS", pacingMaxQuietDefault),
		CharsPerMinute: pacingCharsPerMinDefault,
		MinTyping:      pacingMinTypingDefault,
		MaxTyping:      envDurationMS("TG_GATEWAY_PACING_MAX_TYPING_MS", pacingMaxTypingDefault),
		JitterSigma:    pacingJitterSigmaDefault,
		GapMin:         envDurationMS("TG_GATEWAY_PACING_GAP_MIN_MS", pacingGapMinDefault),
		GapMax:         envDurationMS("TG_GATEWAY_PACING_GAP_MAX_MS", pacingGapMaxDefault),
	}
	if cpm, err := strconv.Atoi(os.Getenv("TG_GATEWAY_PACING_CPM")); err == nil && cpm > 0 {
		p.CharsPerMinute = cpm
	}
	return p
}

func envDurationMS(name string, fallback time.Duration) time.Duration {
	ms, err := strconv.Atoi(os.Getenv(name))
	if err != nil || ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

// StimulusFactor classifies the client's last message and returns the delay
// multiplier for that class. The classes, factors and regexes mirror
// classify_stimulus in call_center/pipeline/rhythm.py so the bot's rhythm can
// be measured with the same script as the operators'.
func StimulusFactor(u TelegramUpdate) float64 {
	text := strings.TrimSpace(u.Text)
	if text == "" {
		if u.Attachment != nil || u.HasLocation {
			return stimulusAttachment
		}
		return stimulusPlain
	}
	if rePacingTechnical.MatchString(text) {
		return stimulusTechnical
	}
	if rePacingObjection.MatchString(text) {
		return stimulusPlain
	}
	if strings.Contains(text, "?") {
		if len([]rune(text)) <= 80 {
			return stimulusShortQuestion
		}
		return stimulusLongQuestion
	}
	if len([]rune(text)) <= 20 {
		return stimulusShortReply
	}
	return stimulusPlain
}

// QuietFor is the silent window after the batch's last message. It is
// recomputed on every new message, so a series keeps the bot quiet for
// as long as the client keeps writing.
func (p *PacingConfig) QuietFor(batch []TelegramUpdate) time.Duration {
	if len(batch) == 0 {
		return p.BaseQuiet
	}
	factor := StimulusFactor(batch[len(batch)-1]) * p.jitter()
	quiet := time.Duration(float64(p.BaseQuiet) * factor)
	if quiet > p.MaxQuiet {
		quiet = p.MaxQuiet
	}
	if quiet < time.Second {
		quiet = time.Second
	}
	return quiet
}

// TypingFor is how long "typing..." shows before this reply is sent: the
// time a person needs to type it at CharsPerMinute, within the clamps.
func (p *PacingConfig) TypingFor(reply string) time.Duration {
	chars := len([]rune(strings.TrimSpace(reply)))
	perMinute := p.CharsPerMinute
	if perMinute <= 0 {
		perMinute = pacingCharsPerMinDefault
	}
	typing := time.Duration(float64(chars) / float64(perMinute) * float64(time.Minute))
	if typing < p.MinTyping {
		typing = p.MinTyping
	}
	if typing > p.MaxTyping {
		typing = p.MaxTyping
	}
	return typing
}

// GapFor is the pause between two messages of one split turn: uniform over
// [GapMin, GapMax]. Uniform rather than log-normal on purpose -- the point of
// the gap is that no two series look alike, and a heavy tail here would leave
// half a thought hanging for minutes.
func (p *PacingConfig) GapFor() time.Duration {
	min, max := p.GapMin, p.GapMax
	if min <= 0 {
		min = pacingGapMinDefault
	}
	if max <= min {
		max = min
	}
	if max == min {
		return min
	}
	p.mu.Lock()
	if p.rand == nil {
		p.rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	span := p.rand.Int63n(int64(max - min))
	p.mu.Unlock()
	return min + time.Duration(span)
}

// jitter draws a log-normal multiplier clamped to [pacingJitterFloor,
// pacingJitterCeiling]: the live line's spread is what keeps replies out of
// one predictable corridor, but a x10 outlier on a "да" would read as a
// broken bot, not a busy person. A zero JitterSigma makes pacing
// deterministic, which the tests rely on.
func (p *PacingConfig) jitter() float64 {
	sigma := p.JitterSigma
	if sigma <= 0 {
		return 1
	}
	p.mu.Lock()
	if p.rand == nil {
		p.rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	z := p.rand.NormFloat64()
	p.mu.Unlock()
	factor := math.Exp(sigma * z)
	if factor < pacingJitterFloor {
		return pacingJitterFloor
	}
	if factor > pacingJitterCeiling {
		return pacingJitterCeiling
	}
	return factor
}

// sendableParts cleans the runtime's series the same way a single reply is
// cleaned, and drops the parts that end up empty (a silence marker, a part
// that was only whitespace). Fewer than two usable parts means there is no
// series to send and the caller falls back to the whole turn.
func sendableParts(messages []string) []string {
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		text, _ := splitLocationButtonMarker(sanitizeModelReply(m))
		if isSilence(text) || strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

// Plan 5, the two holes the existing pacing does not cover.
//
//   - A tail. The live operator answers after more than ten minutes in 29.6%
//     of turns; MaxQuiet cuts the distribution off at 90 seconds, so the bot
//     has no tail at all and every reply lands inside one narrow corridor.
//   - A night. There is no 23-08 window anywhere in the gateway: a message at
//     three in the morning gets the same twelve seconds as one at noon, which
//     is the one timing tell no jitter can hide.
//
// Both are off by default (share 0, probability 0), and both are decided
// BEFORE the agent is called, so the number can travel with the turn as
// delay_s instead of being reconstructed from timestamps afterwards.
type TailNightConfig struct {
	// TailShare is TG_GATEWAY_PACING_TAIL_SHARE: the share of eligible turns
	// that get an extra pause drawn uniformly from [TailMin, TailMax].
	TailShare float64
	TailMin   time.Duration
	TailMax   time.Duration
	// NightMorningP is TG_GATEWAY_NIGHT_MORNING_P: the probability that a
	// message arriving in the night window is answered in the morning window
	// instead of now.
	NightMorningP float64
	// Loc is TG_GATEWAY_TZ (default UTC). The gateway had no zone of its own
	// before this: everything it did was zone-independent.
	Loc *time.Location

	mu   sync.Mutex
	rand *rand.Rand
}

const (
	tailDelayMinDefault = 5 * time.Minute
	tailDelayMaxDefault = 15 * time.Minute
	// tailHourFrom/To bound the working day: a five-to-fifteen minute silence
	// reads as a busy person at noon and as a broken bot at four in the
	// morning, where the night rule owns the timing anyway.
	tailHourFrom = 10
	tailHourTo   = 22
	// nightHourFrom/To is the window whose messages may be held; morning
	// Hour/Late is the window they are held until.
	nightHourFrom   = 23
	nightHourTo     = 8
	morningHour     = 9
	morningHourLate = 10
)

// GatewayLocation reads TG_GATEWAY_TZ. An unknown zone falls back to UTC with
// a warning rather than failing the poller: a mistyped zone must not take the
// bot off the air.
func GatewayLocation() *time.Location {
	name := strings.TrimSpace(os.Getenv("TG_GATEWAY_TZ"))
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Warn().Err(err).Str("tz", name).Msg("tggateway: TG_GATEWAY_TZ is not a known zone, using UTC")
		return time.UTC
	}
	return loc
}

// TailNightFromEnv is always non-nil: with both knobs at zero it is a config
// that never adds a delay, which is what the gateway does today.
func TailNightFromEnv() *TailNightConfig {
	return &TailNightConfig{
		TailShare:     envFloat("TG_GATEWAY_PACING_TAIL_SHARE", 0),
		TailMin:       envDurationMS("TG_GATEWAY_PACING_TAIL_MIN_MS", tailDelayMinDefault),
		TailMax:       envDurationMS("TG_GATEWAY_PACING_TAIL_MAX_MS", tailDelayMaxDefault),
		NightMorningP: envFloat("TG_GATEWAY_NIGHT_MORNING_P", 0),
		Loc:           GatewayLocation(),
	}
}

func envFloat(name string, fallback float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}

func (c *TailNightConfig) draw() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rand == nil {
		c.rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return c.rand.Float64()
}

// ExtraDelay is the whole decision: how much longer than the ordinary pacing
// this turn waits. Zero means "exactly as today".
//
// firstOfDialogue turns the tail off for the opening message: a lead who has
// just written for the first time and waits eleven minutes is a lead who
// leaves, and the plan excludes that case explicitly.
func (c *TailNightConfig) ExtraDelay(now time.Time, firstOfDialogue bool) time.Duration {
	if c == nil {
		return 0
	}
	loc := c.Loc
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	hour := local.Hour()

	if hour >= nightHourFrom || hour < nightHourTo {
		if c.NightMorningP <= 0 || c.draw() >= c.NightMorningP {
			return 0
		}
		morning := time.Date(local.Year(), local.Month(), local.Day(), morningHour, 0, 0, 0, loc)
		if hour >= nightHourFrom {
			morning = morning.AddDate(0, 0, 1)
		}
		morning = morning.Add(time.Duration(c.draw() * float64(morningHourLate-morningHour) * float64(time.Hour)))
		if d := morning.Sub(local); d > 0 {
			return d
		}
		return 0
	}

	if firstOfDialogue || hour < tailHourFrom || hour >= tailHourTo {
		return 0
	}
	if c.TailShare <= 0 || c.draw() >= c.TailShare {
		return 0
	}
	min, max := c.TailMin, c.TailMax
	if min <= 0 {
		min = tailDelayMinDefault
	}
	if max <= min {
		return min
	}
	return min + time.Duration(c.draw()*float64(max-min))
}
