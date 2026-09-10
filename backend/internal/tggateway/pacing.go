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
