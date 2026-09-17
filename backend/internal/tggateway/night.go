package tggateway

import (
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NightConfig is the operator's sleep: between From and To o'clock in Loc
// the named agents do not fetch updates at all, so every message of the
// night is answered in the morning, in order, with the usual pacing. Not
// fetching is what makes it restart-safe: Telegram keeps unfetched updates
// for 24 hours, whereas a batch held in memory dies with the pod. The wake
// hour gets a uniform 0..WakeSpread offset drawn once per night, so the
// first morning reply does not land at 08:00:00 every day.
type NightConfig struct {
	From       int
	To         int
	Loc        *time.Location
	Agents     map[string]bool
	WakeSpread time.Duration

	mu   sync.Mutex
	rand *rand.Rand
}

const (
	nightFromDefault       = 23
	nightToDefault         = 8
	nightWakeSpreadDefault = time.Hour
	nightTZDefault         = "Europe/Moscow"
)

// NightFromEnv reads TG_GATEWAY_NIGHT_AGENTS (comma-separated agent names;
// empty means no night anywhere), TG_GATEWAY_NIGHT_FROM / TG_GATEWAY_NIGHT_TO
// (hours, default 23 and 8), TG_GATEWAY_NIGHT_WAKE_SPREAD_MS (default one
// hour) and TG_GATEWAY_TZ (default Europe/Moscow).
func NightFromEnv() *NightConfig {
	agents := map[string]bool{}
	for _, name := range strings.Split(os.Getenv("TG_GATEWAY_NIGHT_AGENTS"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			agents[name] = true
		}
	}
	if len(agents) == 0 {
		return nil
	}
	tz := os.Getenv("TG_GATEWAY_TZ")
	if tz == "" {
		tz = nightTZDefault
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.FixedZone("MSK", 3*3600)
	}
	return &NightConfig{
		From:       envHour("TG_GATEWAY_NIGHT_FROM", nightFromDefault),
		To:         envHour("TG_GATEWAY_NIGHT_TO", nightToDefault),
		Loc:        loc,
		Agents:     agents,
		WakeSpread: envDurationMS("TG_GATEWAY_NIGHT_WAKE_SPREAD_MS", nightWakeSpreadDefault),
	}
}

func envHour(name string, fallback int) int {
	h, err := strconv.Atoi(os.Getenv(name))
	if err != nil || h < 0 || h > 23 {
		return fallback
	}
	return h
}

// Covers reports whether the agent sleeps at night at all.
func (n *NightConfig) Covers(agent string) bool {
	return n != nil && n.Agents[agent]
}

// Asleep reports whether now falls inside the night window.
func (n *NightConfig) Asleep(now time.Time) bool {
	if n == nil || n.From == n.To {
		return false
	}
	h := now.In(n.Loc).Hour()
	if n.From > n.To {
		return h >= n.From || h < n.To
	}
	return h >= n.From && h < n.To
}

// WakeAt is the moment the agent gets up if now is inside the night: the
// next To o'clock plus the night's random offset. Zero when awake.
func (n *NightConfig) WakeAt(now time.Time) time.Time {
	if !n.Asleep(now) {
		return time.Time{}
	}
	local := now.In(n.Loc)
	wake := time.Date(local.Year(), local.Month(), local.Day(), n.To, 0, 0, 0, n.Loc)
	if !wake.After(local) {
		wake = wake.Add(24 * time.Hour)
	}
	return wake.Add(n.spread())
}

func (n *NightConfig) spread() time.Duration {
	if n.WakeSpread <= 0 {
		return 0
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.rand == nil {
		n.rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return time.Duration(n.rand.Int63n(int64(n.WakeSpread)))
}
