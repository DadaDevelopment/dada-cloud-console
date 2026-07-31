package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// uxEventTypes is the closed set of accepted event names for the client-side UX
// telemetry ingest. Closed on purpose: an unbounded event name would open an
// unbounded dimension in ux_events that every path query then has to filter.
//
// "goal" mirrors a Yandex.Metrika reachGoal into our own database. The goal
// itself still goes to Metrika, but Metrika data never joins a user row, is
// sampled, and is lost entirely to an ad blocker -- so a conversion that
// exists only there cannot be placed on the same timeline as the audit action
// it led to. The mirror carries the goal name in `target` and the same params
// in `props`.
var uxEventTypes = map[string]bool{
	"session_start": true,
	"pageview":      true,
	"click":         true,
	"input_commit":  true,
	"nav_leave":     true,
	"visibility":    true,
	"error_shown":   true,
	"goal":          true,
}

// Field caps and batch bounds. The ingest is unauthenticated, so everything
// stored is bounded here rather than trusted from the payload.
const (
	uxEventsBodyMax   = 64 * 1024
	uxEventsBatchMax  = 60
	uxPathMax         = 500
	uxTargetMax       = 200
	uxTypeMax         = 40
	uxPropsMax        = 2000
	uxClockSkewFuture = 5 * time.Minute
	uxClockSkewPast   = 24 * time.Hour
)

// Rate limits for the UX ingest. Clicks are far more frequent than fake-door
// funnel events, so these sit an order of magnitude above the box ingest -- but
// they are charged per batch rather than per event, which is what keeps one
// chatty session from monopolizing the bucket.
const (
	uxIngestPerMin       = 120
	uxIngestGlobalPerMin = 6000
)

// uxEventPayload is one browser event as the client reports it. Props carries
// control names and structural hints only; field values are never sent and must
// never be stored (152-ФЗ).
type uxEventPayload struct {
	Type   string         `json:"type"`
	Path   string         `json:"path"`
	Target string         `json:"target"`
	Props  map[string]any `json:"props"`
	At     string         `json:"at"`
}

// recordUXEventsRequest is one batch flush from a single browser tab. AnonID is
// the browser-scoped id the frontend keeps across login, which is what stitches
// a pre-signup visit to the account; SessionID scopes one visit.
type recordUXEventsRequest struct {
	AnonID    string           `json:"anon_id"`
	SessionID string           `json:"session_id"`
	Events    []uxEventPayload `json:"events"`
}

// parseUXTime accepts the client's RFC3339 timestamp but falls back to server
// time when it is missing or implausible: a browser with a wrong clock would
// otherwise write rows that sort into the middle of another session forever.
func parseUXTime(raw string, now time.Time) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return now
	}
	if t.After(now.Add(uxClockSkewFuture)) || t.Before(now.Add(-uxClockSkewPast)) {
		return now
	}
	return t
}

// optionalUUID parses an opaque client id, returning nil when absent or
// malformed. Absent is normal (browser storage can be blocked), and a malformed
// id is not worth failing a whole telemetry batch over.
func optionalUUID(raw string) *uuid.UUID {
	v, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	return &v
}

// uxUserFromCookie resolves the internal user id from the dada_uid cookie,
// which carries the Keycloak sub published at login and is the same id sent to
// Yandex.Metrika. Resolved server-side on purpose: a client-supplied user id
// would let anyone write rows into somebody else's path.
func (h *Handler) uxUserFromCookie(ctx context.Context, c *gin.Context) *uuid.UUID {
	raw, err := c.Cookie("dada_uid")
	if err != nil || raw == "" {
		return nil
	}
	sub := clampLen(strings.TrimSpace(raw), 128)
	if _, err := uuid.Parse(sub); err != nil {
		return nil
	}
	var id uuid.UUID
	if err := h.pool.QueryRow(ctx,
		`SELECT id FROM users WHERE keycloak_sub = $1`, sub).Scan(&id); err != nil {
		return nil
	}
	return &id
}

// RecordUXEvents stores a batch of client-side UX events into ux_events.
//
// This is the half of the user path that audit_events cannot see. That journal
// records authorized backend WRITE actions, so everything a person does that
// never reaches a mutating endpoint -- opening a page, poking Settings tabs,
// opening and closing a modal, pressing a button that did nothing -- produced
// no row anywhere before this endpoint existed. Yandex.Metrika does not fill
// the gap: it is anonymous, sampled, and does not join to users.
//
// Unauthenticated because half the interesting path happens before login.
// Guarded by a per-IP plus global token bucket, a capped body and batch size,
// and a closed event-name set -- not by the user JWT middleware.
//
// @ID          recordUXEvents
// @Summary     Record client-side UX events
// @Description Ingests a batch of browser UX events (session_start, pageview, click, input_commit, nav_leave, visibility, error_shown) into ux_events, the client-side half of the end-to-end path that audit_events cannot see. Unauthenticated so the pre-login part of the journey is captured; the user is resolved server-side from the dada_uid cookie, never from the payload. Rate-limited per client IP and globally, body and batch size capped, event names checked against a closed set. Carries control names and paths only -- never field values.
// @Tags        telemetry
// @Accept      json
// @Produce     json
// @Param       body body     recordUXEventsRequest true "UX event batch"
// @Success     202  {object} map[string]interface{} "accepted event count"
// @Failure     400  {object} map[string]string
// @Failure     429  {object} map[string]string
// @Router      /telemetry/events [post]
func (h *Handler) RecordUXEvents(c *gin.Context) {
	if !h.uxIngestLimiter.Allow(c.ClientIP()) {
		respondError(c, http.StatusTooManyRequests, "rate limited")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, uxEventsBodyMax)

	var req recordUXEventsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.Events) == 0 {
		c.JSON(http.StatusAccepted, gin.H{"status": "ok", "recorded": 0})
		return
	}
	if len(req.Events) > uxEventsBatchMax {
		req.Events = req.Events[:uxEventsBatchMax]
	}

	ctx := c.Request.Context()
	anonID := optionalUUID(req.AnonID)
	sessionID := optionalUUID(req.SessionID)
	userID := h.uxUserFromCookie(ctx, c)
	now := time.Now().UTC()

	batch := &pgx.Batch{}
	for _, e := range req.Events {
		typ := trimTo(e.Type, uxTypeMax)
		if !uxEventTypes[typ] {
			continue
		}
		props := []byte("{}")
		if len(e.Props) > 0 {
			if b, err := json.Marshal(e.Props); err == nil && len(b) <= uxPropsMax {
				props = b
			}
		}
		batch.Queue(
			`INSERT INTO ux_events (user_id, anon_id, session_id, event_type, path, target, props, occurred_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			userID, anonID, sessionID, typ,
			trimTo(e.Path, uxPathMax),
			trimTo(e.Target, uxTargetMax),
			props,
			parseUXTime(e.At, now),
		)
	}
	if batch.Len() == 0 {
		c.JSON(http.StatusAccepted, gin.H{"status": "ok", "recorded": 0})
		return
	}

	results := h.pool.SendBatch(ctx, batch)
	recorded := 0
	var firstErr error
	for i := 0; i < batch.Len(); i++ {
		if _, err := results.Exec(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		recorded++
	}
	if err := results.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		log.Error().Err(firstErr).Int("queued", batch.Len()).Msg("ux telemetry: insert failed")
	}

	c.JSON(http.StatusAccepted, gin.H{"status": "ok", "recorded": recorded})
}
