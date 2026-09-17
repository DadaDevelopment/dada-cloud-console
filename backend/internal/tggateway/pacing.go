package tggateway

import (
	"math"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

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

// Plan 5, the hole the existing pacing does not cover: a tail. The live
// operator answers after more than ten minutes in 29.6% of turns, while
// MaxQuiet cuts everything off at 90 seconds, so every bot reply lands inside
// one narrow corridor no jitter can widen.
//
// The night window (hold a 23-08 message until the morning) is deliberately
// NOT here. It needs a delay of up to ten hours, and the only place this
// gateway can hold a reply is a goroutine's memory: a restart during the wait
// loses the reply while the runtime transcript already contains it. That
// needs persistent scheduled delivery, which is its own change.
//
// TailShare defaults to 0, so ExtraDelay returns 0 on every turn until it is
// set, and the gateway behaves exactly as it does today.
type TailDelayConfig struct {
	// TailShare is TG_GATEWAY_PACING_TAIL_SHARE, clamped to [0, 1]: the share
	// of eligible turns that wait an extra pause drawn uniformly from
	// [TailMin, TailMax].
	TailShare float64
	TailMin   time.Duration
	TailMax   time.Duration
	// Loc is TG_GATEWAY_TZ (default UTC). The gateway had no zone of its own
	// before this: everything it did was zone-independent.
	Loc *time.Location

	mu   sync.Mutex
	rand *rand.Rand
}

const (
	tailDelayMinDefault = 5 * time.Minute
	// tailDelayMaxCap is a hard ceiling, not just a default: a reply held in
	// a goroutine is lost on restart, and fifteen minutes is the longest this
	// gateway may risk without persistent delivery.
	tailDelayMaxCap = 15 * time.Minute
	// tailHourFrom/To bound the working day: a five-to-fifteen minute silence
	// reads as a busy person at noon and as a broken bot at four in the
	// morning.
	tailHourFrom = 10
	tailHourTo   = 22
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

func SeriesGapFromEnv() *PacingConfig {
	return &PacingConfig{
		GapMin: envDurationMS("TG_GATEWAY_PACING_GAP_MIN_MS", pacingGapMinDefault),
		GapMax: envDurationMS("TG_GATEWAY_PACING_GAP_MAX_MS", pacingGapMaxDefault),
	}
}

// TailDelayFromEnv is always non-nil: with the share at zero it is a config
// that never adds a delay, which is what the gateway does today.
func TailDelayFromEnv() *TailDelayConfig {
	c := &TailDelayConfig{
		TailShare: envShare("TG_GATEWAY_PACING_TAIL_SHARE", 0),
		TailMin:   envDurationMS("TG_GATEWAY_PACING_TAIL_MIN_MS", tailDelayMinDefault),
		TailMax:   envDurationMS("TG_GATEWAY_PACING_TAIL_MAX_MS", tailDelayMaxCap),
		Loc:       GatewayLocation(),
	}
	if c.TailMax > tailDelayMaxCap {
		log.Warn().Dur("requested", c.TailMax).Dur("cap", tailDelayMaxCap).
			Msg("tggateway: tail delay capped; a longer hold needs persistent delivery")
		c.TailMax = tailDelayMaxCap
	}
	if c.TailMin > c.TailMax {
		c.TailMin = c.TailMax
	}
	return c
}

// envShare reads a probability and clamps it to [0, 1]: a share of 7 in a
// manifest means somebody typed a percentage, and it must not turn into
// "always".
func envShare(name string, fallback float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil {
		return fallback
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func (c *TailDelayConfig) draw() float64 {
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
func (c *TailDelayConfig) ExtraDelay(now time.Time, firstOfDialogue bool) time.Duration {
	if c == nil || c.TailShare <= 0 || firstOfDialogue {
		return 0
	}
	loc := c.Loc
	if loc == nil {
		loc = time.UTC
	}
	hour := now.In(loc).Hour()
	if hour < tailHourFrom || hour >= tailHourTo {
		return 0
	}
	if c.draw() >= c.TailShare {
		return 0
	}
	min, max := c.TailMin, c.TailMax
	if min <= 0 {
		min = tailDelayMinDefault
	}
	if max > tailDelayMaxCap {
		max = tailDelayMaxCap
	}
	if max <= min {
		return min
	}
	return min + time.Duration(c.draw()*float64(max-min))
}

// seenChats answers one question, "is this the first batch this poller has
// handled for that chat", without growing forever: a long-lived poller sees
// tens of thousands of chats, and a plain map of every key ever seen is a
// leak with no upper bound.
//
// Forgetting a chat only means the next message counts as "first", which
// turns the tail delay OFF for it -- the safe direction.
type seenChats struct {
	mu   sync.Mutex
	ttl  time.Duration
	max  int
	seen map[string]time.Time
}

const (
	// seenChatsTTL is far longer than any dialogue's own rhythm and far
	// shorter than a poller's lifetime.
	seenChatsTTL = 24 * time.Hour
	seenChatsMax = 10000
)

func newSeenChats(ttl time.Duration, max int) *seenChats {
	return &seenChats{ttl: ttl, max: max, seen: map[string]time.Time{}}
}

// firstTime reports whether convKey is new and records it as seen.
func (s *seenChats) firstTime(convKey string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	last, known := s.seen[convKey]
	first := !known || now.Sub(last) > s.ttl
	s.seen[convKey] = now
	if len(s.seen) > s.max {
		s.evictLocked(now)
	}
	return first
}

// evictLocked drops everything past the TTL, and if that was not enough (a
// burst of new chats inside one TTL window) everything but the newest half.
func (s *seenChats) evictLocked(now time.Time) {
	for key, at := range s.seen {
		if now.Sub(at) > s.ttl {
			delete(s.seen, key)
		}
	}
	if len(s.seen) <= s.max {
		return
	}
	times := make([]time.Time, 0, len(s.seen))
	for _, at := range s.seen {
		times = append(times, at)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	cutoff := times[len(times)/2]
	for key, at := range s.seen {
		if at.Before(cutoff) {
			delete(s.seen, key)
		}
	}
}
