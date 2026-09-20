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
package langfusebudget

import (
	"context"
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
const (
	defaultDayLimit   = 1500
	defaultMonthLimit = 45000
	defaultPoll       = 10 * time.Minute
	retryPoll         = time.Minute
)

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
		Help: "1 while a window is over budget: no judge scores are posted and agents are told to mute their traces. Any new turn in that state is invisible in Langfuse by design.",
	})
	pollAge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dada_langfuse_budget_poll_age_seconds",
		Help: "Seconds since the guard last read the usage from Langfuse. Grows when the metrics API fails; the guard then keeps its last verdict.",
	})
)

// Counter is the slice of the Langfuse client the guard reads through.
type Counter interface {
	Count(ctx context.Context, view string, from, to time.Time, filters ...langfuse.MetricFilter) (int64, error)
}

// Guard polls one project's unit usage and answers Exceeded.
type Guard struct {
	counter    Counter
	dayLimit   int64
	monthLimit int64
	poll       time.Duration
	now        func() time.Time
	over       atomic.Bool
	known      atomic.Bool
	lastPoll   atomic.Int64
}

// New builds a guard over the client; a nil or unconfigured client yields
// nil, and a nil Guard never reports Exceeded.
func New(client *langfuse.Client) *Guard {
	if !client.Configured() {
		return nil
	}
	g := &Guard{counter: client, dayLimit: envInt(envDayLimit, defaultDayLimit), monthLimit: envInt(envMonthLimit, defaultMonthLimit), poll: defaultPoll, now: time.Now}
	if d, err := time.ParseDuration(os.Getenv(envPoll)); err == nil && d > 0 {
		g.poll = d
	}
	limits.WithLabelValues("day").Set(float64(g.dayLimit))
	limits.WithLabelValues("month").Set(float64(g.monthLimit))
	return g
}

func envInt(key string, def int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(key), 10, 64); err == nil && v > 0 {
		return v
	}
	return def
}

// Exceeded reports whether new traces and scores must be held back. A guard
// that has never managed to read the usage answers true: the quota was
// blown on 2026-09-20 by pods that restarted hourly, got 429 on their first
// poll and spent ten minutes believing the budget was fine. Until the first
// read lands the pod holds back, and Run retries every minute to shorten
// that hold.
func (g *Guard) Exceeded() bool {
	return g != nil && (!g.known.Load() || g.over.Load())
}

// Run polls until ctx ends. The first read happens before Run returns
// control to the timer, so a restart under an exceeded budget mutes at once;
// a failed read is retried after retryPoll instead of the full interval.
func (g *Guard) Run(ctx context.Context) {
	if g == nil {
		return
	}
	for {
		wait := g.poll
		if !g.Refresh(ctx) {
			wait = retryPoll
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Refresh reads today's and this month's units and updates the verdict. A
// failed read keeps the previous verdict, only ages the poll gauge and
// returns false.
func (g *Guard) Refresh(ctx context.Context) bool {
	now := g.now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	day, err := g.units(ctx, dayStart, now)
	if err != nil {
		log.Warn().Err(err).Msg("langfusebudget: day usage unreadable, keeping last verdict")
		g.age()
		return false
	}
	month, err := g.units(ctx, monthStart, now)
	if err != nil {
		log.Warn().Err(err).Msg("langfusebudget: month usage unreadable, keeping last verdict")
		g.age()
		return false
	}
	units.WithLabelValues("day").Set(float64(day))
	units.WithLabelValues("month").Set(float64(month))
	over := day > g.dayLimit || month > g.monthLimit
	if over != g.over.Load() {
		log.Warn().Int64("day", day).Int64("day_limit", g.dayLimit).Int64("month", month).Int64("month_limit", g.monthLimit).Bool("exceeded", over).Msg("langfusebudget: verdict changed")
	}
	g.over.Store(over)
	g.known.Store(true)
	if over {
		exceeded.Set(1)
	} else {
		exceeded.Set(0)
	}
	g.lastPoll.Store(now.Unix())
	g.age()
	return true
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
