package api

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// identitySignalFieldMax bounds every stored string. These values come from
// request headers, so nothing reaches the database unbounded.
const identitySignalFieldMax = 512

// clientHintHeaders are the User-Agent Client Hints Chromium sends on its own,
// with no cooperation from the frontend. They are the cheapest device signal
// available while the real client IP is unavailable (the public load balancer
// SNATs every request to a single address).
var clientHintHeaders = []string{
	"Sec-Ch-Ua",
	"Sec-Ch-Ua-Mobile",
	"Sec-Ch-Ua-Platform",
	"Sec-Ch-Ua-Platform-Version",
	"Sec-Ch-Ua-Arch",
	"Sec-Ch-Ua-Model",
}

// uaFamily reduces a User-Agent to a coarse bucket. The point is not browser
// analytics: a run of accounts sharing one exact family, especially an
// automation family, is the signal worth surfacing.
func uaFamily(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case l == "":
		return "none"
	case strings.Contains(l, "headlesschrome"):
		return "headless-chrome"
	case strings.Contains(l, "playwright") || strings.Contains(l, "puppeteer"):
		return "automation"
	case strings.Contains(l, "curl/") || strings.Contains(l, "wget/") ||
		strings.Contains(l, "python-requests") || strings.Contains(l, "go-http-client"):
		return "cli"
	case strings.Contains(l, "edg/"):
		return "edge"
	case strings.Contains(l, "opr/"):
		return "opera"
	case strings.Contains(l, "firefox/"):
		return "firefox"
	case strings.Contains(l, "chrome/"):
		return "chrome"
	case strings.Contains(l, "safari/"):
		return "safari"
	default:
		return "other"
	}
}

func clipField(s string) string {
	if len(s) > identitySignalFieldMax {
		return s[:identitySignalFieldMax]
	}
	return s
}

// identitySignal is one observation about the client behind a request.
type identitySignal struct {
	UserID         uuid.UUID
	Event          string
	ObservedIP     string
	UserAgent      string
	UAFamily       string
	AcceptLanguage string
	ClientHints    map[string]string
}

// collectIdentitySignal reads everything observable from the request headers.
// ObservedIP is recorded as seen; today the public path collapses it to the load
// balancer address for every visitor, so it is stored for the day PROXY protocol
// makes it meaningful rather than relied on now.
func collectIdentitySignal(c *gin.Context, userID uuid.UUID, event string) identitySignal {
	ua := c.GetHeader("User-Agent")
	hints := make(map[string]string, len(clientHintHeaders))
	for _, h := range clientHintHeaders {
		if v := c.GetHeader(h); v != "" {
			hints[h] = clipField(v)
		}
	}
	return identitySignal{
		UserID:         userID,
		Event:          event,
		ObservedIP:     clipField(c.ClientIP()),
		UserAgent:      clipField(ua),
		UAFamily:       uaFamily(ua),
		AcceptLanguage: clipField(c.GetHeader("Accept-Language")),
		ClientHints:    hints,
	}
}

// recordIdentitySignal persists one observation off the request path. A failure
// here must never cost the user their request: signup succeeds whether or not
// the signal lands.
func recordIdentitySignal(pool *pgxpool.Pool, sig identitySignal) {
	if pool == nil || sig.UserID == uuid.Nil {
		return
	}
	hints, err := json.Marshal(sig.ClientHints)
	if err != nil {
		hints = []byte("{}")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Exec(ctx, `
			INSERT INTO identity_signals
			    (user_id, event, observed_ip, user_agent, ua_family, accept_language, client_hints)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, sig.UserID, sig.Event, sig.ObservedIP, sig.UserAgent, sig.UAFamily, sig.AcceptLanguage, hints)
		if err != nil {
			log.Printf("identity signal: record %s for %s failed: %v", sig.Event, sig.UserID, err)
		}
	}()
}
