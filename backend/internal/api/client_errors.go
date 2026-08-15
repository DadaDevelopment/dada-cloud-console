package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// clientErrorPersistTimeout bounds the detached goroutine that writes the
// ux_events row: it must never outlive the request by more than a moment.
const clientErrorPersistTimeout = 5 * time.Second

const clientErrorMaxBody = 32 * 1024

type clientErrorReport struct {
	Message        string `json:"message"`
	Stack          string `json:"stack"`
	ComponentStack string `json:"component_stack"`
	URL            string `json:"url"`
	Kind           string `json:"kind"`
}

// clampLen truncates s to at most n bytes so a hostile or huge client payload
// cannot bloat a single log line.
func clampLen(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ReportClientError records a browser-side crash into the server logs, so
// client-side React/render errors surface in `kubectl logs` instead of dying
// silently in the user's browser console. It is intentionally unauthenticated
// (crashes happen on any page, including before login) and fire-and-forget:
// bad or empty payloads still return 204, and the body is size-capped.
//
// @ID          reportClientError
// @Summary     Report a client-side crash
// @Description Records a browser-side error (React error boundary or an unhandled window error/rejection) into the server logs. Unauthenticated; the browser posts message/stack/url. Always returns 204.
// @Tags        telemetry
// @Accept      json
// @Param       body body     clientErrorReport true "Client error"
// @Success     204  "no content"
// @Router      /client-errors [post]
func (h *Handler) ReportClientError(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, clientErrorMaxBody)

	var r clientErrorReport
	if err := c.ShouldBindJSON(&r); err != nil || r.Message == "" {
		c.Status(http.StatusNoContent)
		return
	}
	kind := r.Kind
	if kind == "" {
		kind = "react"
	}
	log.Warn().
		Str("kind", clampLen(kind, 40)).
		Str("url", clampLen(r.URL, 300)).
		Str("ua", clampLen(c.Request.UserAgent(), 200)).
		Str("component_stack", clampLen(r.ComponentStack, 2000)).
		Str("stack", clampLen(r.Stack, 4000)).
		Msg("client crash: " + clampLen(r.Message, 500))

	ua := c.Request.UserAgent()
	cookieSub, _ := c.Cookie("dada_uid")
	go h.recordErrorShownEvent(kind, r.URL, r.Message, ua, cookieSub)

	c.Status(http.StatusNoContent)
}

// splitReportURL separates a browser-reported URL into a bare pathname (for
// the ux_events `path` column, which every other event type also keys on)
// and the same URL with its query string and fragment stripped (for `props`,
// where the extra context of scheme/host is still useful for path analysis
// across the marketing and console hosts). The query string can carry
// tokens, so it never reaches either output -- mirrors currentPath() in
// frontend/lib/ux-telemetry.ts.
func splitReportURL(raw string) (path, cleanURL string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if u, err := url.Parse(raw); err == nil {
		u.RawQuery = ""
		u.Fragment = ""
		return u.Path, u.String()
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	return raw, raw
}

// recordErrorShownEvent persists one error_shown row into ux_events so a
// browser crash becomes queryable and joinable to the same user's
// pageview/click path, surviving even if the log line it was written next to
// is never read. Reuses RecordUXEvents' identity resolution (uxUserFromSub)
// and column set; does not touch anon_id/session_id because the beacon body
// this endpoint receives (frontend/lib/report-error.ts) never carries them.
//
// Runs detached from the request (called via `go`) so a slow or failing
// insert can never add latency to, or fail, the always-204 response above --
// and takes plain values rather than *gin.Context because gin recycles that
// context back into its pool as soon as the handler returns.
//
// The stack trace deliberately never reaches props: that stays in the log
// line only (kubectl logs), while this row exists for path analysis, not
// debugging one crash.
func (h *Handler) recordErrorShownEvent(kind, rawURL, message, ua, cookieSub string) {
	if h.pool == nil || strings.TrimSpace(message) == "" {
		return
	}
	path, cleanURL := splitReportURL(rawURL)
	props, err := json.Marshal(map[string]string{
		"message": clampLen(message, 500),
		"url":     clampLen(cleanURL, 300),
		"ua":      clampLen(ua, 200),
	})
	if err != nil {
		log.Error().Err(err).Msg("client crash: ux_events props marshal failed")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), clientErrorPersistTimeout)
	defer cancel()

	userID := h.uxUserFromSub(ctx, cookieSub)
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO ux_events (user_id, anon_id, session_id, event_type, path, target, props, occurred_at)
		 VALUES ($1, NULL, NULL, 'error_shown', $2, $3, $4, $5)`,
		userID, trimTo(path, uxPathMax), trimTo(kind, uxTargetMax), props, time.Now().UTC(),
	); err != nil {
		log.Error().Err(err).Msg("client crash: ux_events insert failed")
	}
}
