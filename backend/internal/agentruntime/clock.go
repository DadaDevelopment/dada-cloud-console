package agentruntime

import (
	"os"
	"strings"
	"sync/atomic"
	"time"
	_ "time/tzdata"
)

var runtimeLocation atomic.Pointer[time.Location]

// RuntimeLocation is the zone every clock value shown to the model is
// rendered in: message timestamps in the run envelope and the run's own
// "now". Defaults to UTC until SetRuntimeLocation is called. The zone
// database is embedded (time/tzdata) so the alpine image needs no tzdata
// package for IANA names to resolve.
func RuntimeLocation() *time.Location {
	if loc := runtimeLocation.Load(); loc != nil {
		return loc
	}
	return time.UTC
}

// SetRuntimeLocation switches the zone the model sees; nil resets to UTC.
func SetRuntimeLocation(loc *time.Location) {
	runtimeLocation.Store(loc)
}

// LocationFromEnv reads AGENT_RUNTIME_TZ as an IANA zone name; empty means
// UTC. An unknown name is an error so a typo fails startup instead of
// silently keeping the model three hours off.
func LocationFromEnv() (*time.Location, error) {
	name := strings.TrimSpace(os.Getenv("AGENT_RUNTIME_TZ"))
	if name == "" {
		return time.UTC, nil
	}
	return time.LoadLocation(name)
}

// localizeMessages returns a copy of messages with every clock field moved
// into the runtime zone. The stored records stay untouched: the zone is a
// presentation concern for the model, not a storage one.
func localizeMessages(messages []Message, loc *time.Location) []Message {
	out := make([]Message, len(messages))
	for i, m := range messages {
		m.CreatedAt = m.CreatedAt.In(loc)
		m.SourceSentAt = localizeTime(m.SourceSentAt, loc)
		m.EditedAt = localizeTime(m.EditedAt, loc)
		m.DeletedAt = localizeTime(m.DeletedAt, loc)
		out[i] = m
	}
	return out
}

func localizeTime(t *time.Time, loc *time.Location) *time.Time {
	if t == nil {
		return nil
	}
	v := t.In(loc)
	return &v
}
