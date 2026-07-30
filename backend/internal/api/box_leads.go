package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

// Dada Box fake-door funnel ingest.
//
// This replaces a `console.log("box_funnel " + ...)` line in the Next route
// handler as the system of record. The log line stays as a fail-open fallback —
// the door must keep working when this hop is down (see
// frontend/app/api/box/lead/route.ts and migrations/060_box_leads.sql).
//
// Three properties this endpoint has that the log line could not:
//
//   - The denominator lives next to the numerator. page_view is a first-class
//     event here, so view -> request conversion is one query against one table
//     instead of a ratio between Yandex Metrika and a log stream, which nobody
//     would trust.
//   - Events carry an opaque visitor id (dada_vid), so counters count people
//     rather than clicks. One curious visitor replaying the demo six times is
//     one person, not six.
//   - crystallize_intent is durable. It is the event that decides whether Box is
//     a product with a ladder or a one-off utility (brief §7).
//
// PII: the only personal data accepted is the email/contact the person typed into
// the form, and it is written to box_leads only — never to a log line, never to a
// Prometheus label, never into the vid column (152-ФЗ). `vid` must parse as a
// UUID, which is what makes "no email can end up in this column" a property of
// the code rather than a promise.
//
// Deliberately NOT an MCP tool. The `keep` allowlist in
// internal/mcp/default_overrides.yaml is closed, so a new annotated operation
// does not leak into the agent tool surface — and this one must not: it is a
// marketing ingest written by a landing page, not something an agent calls.

// boxFunnelEvents is the closed set of accepted event names. It matches
// KNOWN_EVENTS in the Next route and the `event` label values documented on
// dada_box_funnel_events_total. Closed on purpose: an unbounded event name would
// be an unbounded Prometheus label.
var boxFunnelEvents = map[string]bool{
	"page_view":          true,
	"demo_run":           true,
	"box_requested":      true,
	"crystallize_intent": true,
}

// boxClaimRe matches the request code the door shows the person (BOX-7F3A-9C21).
// The code is minted by the Next route, not here: the visitor must get a real
// code even when this endpoint is unreachable.
var boxClaimRe = regexp.MustCompile(`^BOX-[0-9A-F]{4}-[0-9A-F]{4}$`)

// boxEmailRe is a deliberately loose sanity check, the same shape as the one the
// form uses client-side. Address validity is settled by mailing the person, not
// by a regex.
var boxEmailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// boxPageViewSessionWindow is how long one "session" lasts for page_view dedup.
// It is 30 minutes because that is the same inactivity gap that separates two box
// sessions in box_repeat_use_7d — one session definition for the whole funnel
// beats two that drift apart.
const boxPageViewSessionWindow = "30 minutes"

// Field caps. This endpoint is unauthenticated, so everything that gets stored is
// bounded here rather than trusted.
const (
	boxFieldMax    = 500
	boxUseCaseMax  = 2000
	boxLocaleMax   = 8
	boxEventMax    = 40
	boxWantsMax    = 20
	boxLeadBodyMax = 16 * 1024
)

// boxFunnelPerMin / boxFunnelBurst bound one client; boxFunnelGlobalPerMin bounds
// the endpoint as a whole. The per-IP bucket is the fairness guard and the global
// bucket is the database guard: an attacker who forges X-Forwarded-For defeats the
// first but not the second.
const (
	boxFunnelPerMin       = 30
	boxFunnelGlobalPerMin = 600
	boxFunnelMaxKeys      = 10000
)

// boxFunnelLimiter is a per-client token bucket plus one global bucket, in the
// shape of telemetry.IngestLimiter but keyed by client IP instead of app id.
// Per-pod and approximate on purpose: it exists to stop a flood, not to enforce a
// quota, so it needs no shared state.
type boxFunnelLimiter struct {
	mu      sync.Mutex
	perMin  int
	buckets map[string]*rate.Limiter
	global  *rate.Limiter
}

func newBoxFunnelLimiter(perMin, globalPerMin int) *boxFunnelLimiter {
	if perMin <= 0 {
		perMin = boxFunnelPerMin
	}
	if globalPerMin <= 0 {
		globalPerMin = boxFunnelGlobalPerMin
	}
	return &boxFunnelLimiter{
		perMin:  perMin,
		buckets: make(map[string]*rate.Limiter),
		global:  rate.NewLimiter(rate.Limit(float64(globalPerMin)/60.0), globalPerMin),
	}
}

// Allow reports whether one more request from key may proceed.
func (l *boxFunnelLimiter) Allow(key string) bool {
	if !l.global.Allow() {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	lim := l.buckets[key]
	if lim == nil {
		// Opportunistic sweep so a spray of one-shot source addresses cannot grow
		// the map without bound. A bucket back at full tokens has been idle for a
		// whole window and carries no state worth keeping.
		if len(l.buckets) >= boxFunnelMaxKeys {
			for k, b := range l.buckets {
				if b.Tokens() >= float64(l.perMin) {
					delete(l.buckets, k)
				}
			}
		}
		lim = rate.NewLimiter(rate.Limit(float64(l.perMin)/60.0), l.perMin)
		l.buckets[key] = lim
	}
	return lim.Allow()
}

// recordBoxFunnelEventRequest is the funnel event as the Next route forwards it.
// Field names are snake_case to match the SQL columns; the Next route maps its
// camelCase browser payload onto them.
type recordBoxFunnelEventRequest struct {
	Event     string   `json:"event"`
	Claim     string   `json:"claim"`
	VID       string   `json:"vid"`
	Locale    string   `json:"locale"`
	UTMSource string   `json:"utm_source"`
	Referer   string   `json:"referer"`
	Email     string   `json:"email"`
	Contact   string   `json:"contact"`
	Agent     string   `json:"agent"`
	Parallel  string   `json:"parallel"`
	Price     string   `json:"price"`
	UseCase   string   `json:"use_case"`
	Wants     []string `json:"wants"`
}

// trimTo trims surrounding space and caps the length, returning "" for empty.
func trimTo(s string, max int) string {
	return clampLen(strings.TrimSpace(s), max)
}

// RecordBoxFunnelEvent stores one fake-door funnel event, plus the lead row when
// the event is box_requested.
//
// Unauthenticated on purpose: the /box landing is a public marketing page with no
// session. Rate-limited per client IP and globally, body size capped, and the
// event name checked against a closed set.
//
// @ID          recordBoxFunnelEvent
// @Summary     Record a Dada Box fake-door funnel event
// @Description Stores one funnel event from the Dada Box private-preview landing (page_view, demo_run, box_requested, crystallize_intent) into box_funnel_events, and the lead itself into box_leads when the event is box_requested. Unauthenticated marketing ingest, called server-to-server by the landing's route handler, rate-limited per client IP. `vid` is the opaque dada_vid visitor cookie and must be a UUID -- never an email or any other personal datum. A page_view is deduplicated per vid within a 30-minute session window and then reports recorded=false. The request code (claim) is minted by the landing so the visitor still gets one when this endpoint is down.
// @Tags        box
// @Accept      json
// @Produce     json
// @Param       body body     recordBoxFunnelEventRequest true "Funnel event"
// @Success     201  {object} map[string]interface{} "status and whether the event was recorded"
// @Failure     400  {object} map[string]string
// @Failure     429  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Router      /box/leads [post]
func (h *Handler) RecordBoxFunnelEvent(c *gin.Context) {
	if !h.boxFunnelLimiter.Allow(c.ClientIP()) {
		respondError(c, http.StatusTooManyRequests, "rate limited")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, boxLeadBodyMax)

	var req recordBoxFunnelEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid JSON body")
		return
	}

	event := trimTo(req.Event, boxEventMax)
	if !boxFunnelEvents[event] {
		respondError(c, http.StatusBadRequest, "unknown event")
		return
	}

	// vid is optional (cookies can be blocked) but must be opaque when present.
	// This is the gate that keeps personal data out of the column entirely.
	var vid *uuid.UUID
	if raw := trimTo(req.VID, 64); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			respondError(c, http.StatusBadRequest, "vid must be a UUID")
			return
		}
		vid = &parsed
	}

	locale := trimTo(req.Locale, boxLocaleMax)
	if locale == "" {
		locale = "ru"
	}

	claim := strings.ToUpper(trimTo(req.Claim, 40))
	if claim != "" && !boxClaimRe.MatchString(claim) {
		respondError(c, http.StatusBadRequest, "claim must look like BOX-XXXX-XXXX")
		return
	}

	props := map[string]any{}
	switch event {
	case "box_requested":
		if claim == "" {
			respondError(c, http.StatusBadRequest, "claim is required for box_requested")
			return
		}
		if email := trimTo(req.Email, 200); email == "" || !boxEmailRe.MatchString(email) {
			respondError(c, http.StatusBadRequest, "a valid email is required for box_requested")
			return
		}
		if trimTo(req.UseCase, boxUseCaseMax) == "" {
			respondError(c, http.StatusBadRequest, "use_case is required for box_requested")
			return
		}
	case "crystallize_intent":
		if claim == "" {
			respondError(c, http.StatusBadRequest, "claim is required for crystallize_intent")
			return
		}
		wants := make([]string, 0, len(req.Wants))
		for _, w := range req.Wants {
			if v := trimTo(w, boxFieldMax); v != "" {
				wants = append(wants, v)
			}
			if len(wants) == boxWantsMax {
				break
			}
		}
		props["wants"] = wants
	}

	propsJSON, err := json.Marshal(props)
	if err != nil {
		// Cannot happen for a map of strings/slices, but a silent nil would write
		// invalid JSONB.
		respondError(c, http.StatusInternalServerError, "failed to encode event properties")
		return
	}

	ctx := c.Request.Context()
	var claimArg any
	if claim != "" {
		claimArg = claim
	}
	utm := trimTo(req.UTMSource, 120)
	referer := trimTo(req.Referer, boxFieldMax)

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record funnel event")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// page_view is deduplicated per visitor per session so the funnel's
	// denominator counts people, not reloads. With no vid there is nothing to
	// deduplicate on, so the row is kept and the runbook counts it separately —
	// dropping it would understate the denominator and flatter the conversion rate.
	insert := `INSERT INTO box_funnel_events (event, claim, vid, locale, utm_source, referer, props)
	           VALUES ($1, $2, $3, $4, $5, $6, $7)`
	if event == "page_view" && vid != nil {
		insert = `INSERT INTO box_funnel_events (event, claim, vid, locale, utm_source, referer, props)
		          SELECT $1, $2, $3, $4, $5, $6, $7
		          WHERE NOT EXISTS (
		              SELECT 1 FROM box_funnel_events
		               WHERE event = 'page_view' AND vid = $3
		                 AND at > now() - interval '` + boxPageViewSessionWindow + `'
		          )`
	}

	tag, err := tx.Exec(ctx, insert, event, claimArg, vid, locale, utm, referer, propsJSON)
	if err != nil {
		log.Error().Err(err).Str("event", event).Msg("box funnel: insert event failed")
		respondError(c, http.StatusInternalServerError, "failed to record funnel event")
		return
	}
	recorded := tag.RowsAffected() > 0

	if event == "box_requested" {
		// ON CONFLICT DO NOTHING makes a retried submission idempotent: the claim
		// code is minted upstream, so the same code can legitimately arrive twice
		// if the fallback path retried.
		if _, err := tx.Exec(ctx,
			`INSERT INTO box_leads (claim, email, contact, agent, parallel, price, use_case, locale, utm_source, vid)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 ON CONFLICT (claim) DO NOTHING`,
			claim,
			trimTo(req.Email, 200),
			nullableString(trimTo(req.Contact, boxFieldMax)),
			nullableString(trimTo(req.Agent, 60)),
			nullableString(trimTo(req.Parallel, 60)),
			nullableString(trimTo(req.Price, 60)),
			trimTo(req.UseCase, boxUseCaseMax),
			locale,
			nullableString(utm),
			vid,
		); err != nil {
			// No payload in the log line: it carries the person's email.
			log.Error().Err(err).Str("claim", claim).Msg("box funnel: insert lead failed")
			respondError(c, http.StatusInternalServerError, "failed to record lead")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record funnel event")
		return
	}

	// Counted only when a row was actually written, so a deduplicated page_view
	// cannot inflate the top of the funnel.
	if recorded {
		metrics.RecordBoxFunnelEvent(event, locale)
	}

	c.JSON(http.StatusCreated, gin.H{"status": "ok", "recorded": recorded})
}

// nullableString maps "" to a SQL NULL so optional columns stay NULL instead of
// filling with empty strings that then need a second case in every query.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// grantBoxRequest records that a claim was handed a box.
type grantBoxRequest struct {
	Claim string `json:"claim"`
	OrgID string `json:"org_id"`
	BoxID string `json:"box_id"`
}

// GrantBox records the concierge handing a box to a claim.
//
// This endpoint is small and unglamorous and it is the reason the experiment can
// be concluded at all. Provisioning is manual in the private preview, so nothing
// else in the system learns which request code received which box — and without
// that link the brief's headline metric (did they come back and use it a second
// time) has no data source whatsoever. One admin call or one psql insert, but it
// is not optional.
//
// Platform-admin only: it writes the ground truth the whole experiment is read
// against.
//
// @ID          grantBox
// @Summary     Record a manual Dada Box grant
// @Description Records that a Dada Box request code (claim) was handed a box during the concierge-provisioned private preview: the join between a fake-door lead and a real box, and the only data source for the repeat-use metric. Platform-admin only. Idempotent per (claim, box_id) -- re-granting the same box refreshes granted_at, and granting a second box to the same claim adds a row, which is the normal case once the first box has been reaped.
// @Tags        box
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body     grantBoxRequest true "Grant"
// @Success     201  {object} map[string]string
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Failure     403  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Router      /admin/box/grants [post]
func (h *Handler) GrantBox(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
		return
	}

	var req grantBoxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid JSON body")
		return
	}

	claim := strings.ToUpper(trimTo(req.Claim, 40))
	if !boxClaimRe.MatchString(claim) {
		respondError(c, http.StatusBadRequest, "claim must look like BOX-XXXX-XXXX")
		return
	}
	orgID := trimTo(req.OrgID, 120)
	if orgID == "" {
		respondError(c, http.StatusBadRequest, "org_id is required")
		return
	}
	boxID, err := uuid.Parse(trimTo(req.BoxID, 64))
	if err != nil {
		respondError(c, http.StatusBadRequest, "box_id must be a UUID")
		return
	}

	// granted_by is the operator's internal user id, never their email: this table
	// is read alongside the funnel and inherits the same no-PII rule.
	if _, err := h.pool.Exec(c.Request.Context(),
		`INSERT INTO box_grants (claim, org_id, box_id, granted_by)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (claim, box_id) DO UPDATE SET granted_at = now(), granted_by = EXCLUDED.granted_by`,
		claim, orgID, boxID, claims.UserID.String(),
	); err != nil {
		log.Error().Err(err).Str("claim", claim).Msg("box grant: insert failed")
		respondError(c, http.StatusInternalServerError, "failed to record grant")
		return
	}

	c.JSON(http.StatusCreated, gin.H{"status": "ok"})
}
