// Package langfusebudget keeps one Langfuse project under its plan's monthly
// unit quota.
//
// On 2026-09-18 the support-agent project crossed the Hobby plan's 50k
// units/month: 2.5k traces, 17k observations and 20k scores in one day, all
// of it synthetic QA and rollout traffic. Nothing said so until Langfuse asked
// for money. The guard is the meter that was missing: it asks Langfuse how
// many units the project used today and this month, publishes both as
// gauges, and flips Exceeded once a window is over its budget. Emitters ask
// Exceeded before sending: the judge stops posting scores and the runtime
// tells the agents to mute their traces, so the project loses new turns
// instead of the ability to see any history at all.
//
// The meter itself is rationed. Langfuse cloud answers at most 100 v2/metrics
// requests per 24 hours and one reading costs ten of them, so the guard reads
// every few hours, waits for the window to reopen after a 429 instead of
// retrying on its own clock, and keeps its last reading in Postgres so a
// restarted pod starts from it instead of spending the day's requests again.
// From 2026-09-20 to 2026-09-24 the old ten-minute poll with one-minute
// retries spent the day's requests within ninety minutes of the reset; every
// pod started after that never read the usage, held back, and not one turn
// reached Langfuse.
package langfusebudget

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/dada-tuda/console/backend/internal/langfuse"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
)

const (
	envDayLimit   = "LANGFUSE_BUDGET_UNITS_DAY"
	envMonthLimit = "LANGFUSE_BUDGET_UNITS_MONTH"
	envPoll       = "LANGFUSE_BUDGET_POLL"
)

// Defaults sized for the Hobby plan: 50k units/month leaves 1.5k/day with a
// margin for the days the judge retries and the months that have 31 days.
// Eight readings a day cost 80 of the 100 daily metrics requests and leave
// the rest to people reading the same API by hand.
const (
	defaultDayLimit   = 1500
	defaultMonthLimit = 45000
	defaultPoll       = 3 * time.Hour
	retryPoll         = 5 * time.Minute
	resetSlack        = 30 * time.Second
	maxRetryWait      = 25 * time.Hour
)

// maxSavedAge bounds how old a stored reading may be and still count as
// known on start. The metrics window is a day long, so a healthy guard never
// leaves a gap wider than that plus one poll; an older reading says too little
// about the month to trust.
const maxSavedAge = 30 * time.Hour

var (
	units = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_langfuse_units",
		Help: "Langfuse units (traces + observations + scores) the project used in the window, as Langfuse counts them for the plan quota. Read against dada_langfuse_units_limit.",
	}, []string{"window"})
	limits = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dada_langfuse_units_limit",
		Help: "Unit budget of the window. Above it the guard mutes every new trace and score until the window rolls over.",
	}, []string{"window"})
	exceeded = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_langfuse_budget_exceeded",
		Help: "1 while traces and scores are held back: a window is over budget, or the guard has no reading yet. Any new turn in that state is invisible in Langfuse by design.",
	})
	pollAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_langfuse_budget_poll_age_seconds",
		Help: "Seconds since the reading the guard holds was taken. Grows when the metrics API fails; the guard then keeps its last verdict.",
	})
)

// Counter is the slice of the Langfuse client the guard reads through.
type Counter interface {
	Count(ctx context.Context, view string, from, to time.Time, filters ...langfuse.MetricFilter) (int64, error)
}

// Reading is one usage read: units of the UTC day and month it was taken in.
type Reading struct {
	Day    int64
	Month  int64
	ReadAt time.Time
}

// Store keeps the last reading across restarts, keyed by the project.
type Store interface {
	Load(ctx context.Context, key string) (Reading, bool, error)
	Save(ctx context.Context, key string, r Reading) error
}

// Guard polls one project's unit usage and answers Exceeded.
type Guard struct {
	counter    Counter
	store      Store
	key        string
	dayLimit   int64
	monthLimit int64
	poll       time.Duration
	now        func() time.Time
	over       atomic.Bool
	known      atomic.Bool
	lastPoll   atomic.Int64
	resetAt    time.Time
}

// New builds a guard over the client; a nil or unconfigured client yields
// nil, and a nil Guard never reports Exceeded. A nil store keeps nothing
// across restarts.
func New(client *langfuse.Client, store Store) *Guard {
	if !client.Configured() {
		return nil
	}
	g := &Guard{counter: client, store: store, key: client.PublicKey, dayLimit: envInt(envDayLimit, defaultDayLimit), monthLimit: envInt(envMonthLimit, defaultMonthLimit), poll: defaultPoll, now: time.Now}
	if d, err := time.ParseDuration(os.Getenv(envPoll)); err == nil && d > 0 {
		g.poll = d
	}
	limits.WithLabelValues("day").Set(float64(g.dayLimit))
	limits.WithLabelValues("month").Set(float64(g.monthLimit))
	exceeded.Set(1)
	return g
}

func envInt(key string, def int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(key), 10, 64); err == nil && v > 0 {
		return v
	}
	return def
}

// Exceeded reports whether new traces and scores must be held back. A guard
// that has no reading, neither read nor restored, answers true: the quota was
// blown on 2026-09-20 by pods that restarted hourly, got 429 on their first
// poll and spent ten minutes believing the budget was fine.
func (g *Guard) Exceeded() bool {
	return g != nil && (!g.known.Load() || g.over.Load())
}

// Run restores the stored reading, then polls until ctx ends. A restored
// reading younger than the poll interval defers the first read to its
// schedule, so a restart costs no metrics requests; without one the first
// read happens at once and the pod holds back until it lands.
func (g *Guard) Run(ctx context.Context) {
	if g == nil {
		return
	}
	next := g.restore(ctx)
	for {
		if wait := next.Sub(g.now()); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		if ctx.Err() != nil {
			return
		}
		if g.Refresh(ctx) {
			next = g.now().Add(g.poll)
		} else {
			next = g.now().Add(g.retryWait())
		}
	}
}

func (g *Guard) restore(ctx context.Context) time.Time {
	now := g.now()
	if g.store == nil {
		return now
	}
	r, ok, err := g.store.Load(ctx, g.key)
	if err != nil {
		log.Warn().Err(err).Msg("langfusebudget: stored reading unreadable, reading Langfuse")
		return now
	}
	if !ok || now.Sub(r.ReadAt) > maxSavedAge || r.ReadAt.After(now.Add(time.Minute)) {
		return now
	}
	g.apply(r)
	log.Info().Int64("day", r.Day).Int64("month", r.Month).Time("read_at", r.ReadAt).Bool("exceeded", g.over.Load()).Msg("langfusebudget: restored stored reading")
	return r.ReadAt.Add(g.poll)
}

// retryWait is how long to wait after a failed read: until the rate-limit
// window reopens when Langfuse said when, retryPoll otherwise.
func (g *Guard) retryWait() time.Duration {
	if !g.resetAt.IsZero() {
		if d := g.resetAt.Sub(g.now()) + resetSlack; d > retryPoll {
			return min(d, maxRetryWait)
		}
	}
	return retryPoll
}

// Refresh reads today's and this month's units, updates the verdict and
// stores the reading. A failed read keeps the previous verdict, only ages
// the poll gauge and returns false.
func (g *Guard) Refresh(ctx context.Context) bool {
	now := g.now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	day, err := g.units(ctx, dayStart, now)
	if err != nil {
		g.fail(err, "day")
		return false
	}
	month, err := g.units(ctx, monthStart, now)
	if err != nil {
		g.fail(err, "month")
		return false
	}
	g.resetAt = time.Time{}
	r := Reading{Day: day, Month: month, ReadAt: now}
	g.apply(r)
	log.Info().Int64("day", day).Int64("month", month).Bool("exceeded", g.over.Load()).Msg("langfusebudget: usage read")
	if g.store != nil {
		if err := g.store.Save(ctx, g.key, r); err != nil {
			log.Warn().Err(err).Msg("langfusebudget: reading not stored, a restart will read Langfuse again")
		}
	}
	return true
}

func (g *Guard) fail(err error, window string) {
	g.resetAt = time.Time{}
	var limited *langfuse.RateLimitedError
	if errors.As(err, &limited) {
		g.resetAt = limited.ResetAt
	}
	log.Warn().Err(err).Str("window", window).Time("retry_at", g.now().Add(g.retryWait())).Bool("held_back", g.Exceeded()).Msg("langfusebudget: usage unreadable, keeping last verdict")
	g.age()
}

// apply turns a reading into the verdict. The day window only counts while
// the reading is from today and the month window while it is from this
// month, so a restored reading from before midnight cannot keep a new day
// muted.
func (g *Guard) apply(r Reading) {
	now := g.now().UTC()
	at := r.ReadAt.UTC()
	sameMonth := at.Year() == now.Year() && at.Month() == now.Month()
	sameDay := sameMonth && at.Day() == now.Day()
	over := (sameDay && r.Day > g.dayLimit) || (sameMonth && r.Month > g.monthLimit)
	units.WithLabelValues("day").Set(float64(r.Day))
	units.WithLabelValues("month").Set(float64(r.Month))
	if over != g.over.Load() || !g.known.Load() {
		log.Warn().Int64("day", r.Day).Int64("day_limit", g.dayLimit).Int64("month", r.Month).Int64("month_limit", g.monthLimit).Bool("exceeded", over).Msg("langfusebudget: verdict changed")
	}
	g.over.Store(over)
	g.known.Store(true)
	if over {
		exceeded.Set(1)
	} else {
		exceeded.Set(0)
	}
	g.lastPoll.Store(at.Unix())
	g.age()
}

func (g *Guard) age() {
	if last := g.lastPoll.Load(); last > 0 {
		pollAge.Set(float64(g.now().Unix() - last))
	}
}

// units sums what Langfuse bills for the window: every observation, every
// root observation again as its trace, and every score of the three types.
func (g *Guard) units(ctx context.Context, from, to time.Time) (int64, error) {
	var total int64
	reads := []struct {
		view    string
		filters []langfuse.MetricFilter
	}{
		{langfuse.ViewObservations, nil},
		{langfuse.ViewObservations, []langfuse.MetricFilter{langfuse.RootObservationsOnly}},
		{langfuse.ViewScoresNumeric, nil},
		{langfuse.ViewScoresCategorical, nil},
		{langfuse.ViewScoresBoolean, nil},
	}
	for _, r := range reads {
		n, err := g.counter.Count(ctx, r.view, from, to, r.filters...)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
