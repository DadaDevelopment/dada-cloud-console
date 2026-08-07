package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// The reactivation campaign: accounts that signed up, got a project, and never
// shipped a build.
//
// reactivationMinAge is how long an account is left alone first. A person who
// registered this morning has not stalled, they are mid-session, and mailing
// them "you never deployed" reads as surveillance rather than help.
//
// reactivationPlan / reactivationGrantDays are the offer behind the promo
// link. The grant is a real term, never perpetual: an accidental forever-free
// paid plan is invisible in the plan column and only shows up as missing
// revenue months later.
const (
	reactivationCampaign   = "reactivation-no-deploy"
	reactivationVariant    = "a"
	reactivationMinAge     = 72 * time.Hour
	reactivationPlan       = "startup"
	reactivationGrantDays  = 30
	reactivationSendPerRun = 50
)

// promoTokenBytes sizes the per-recipient promo token. The token is the only
// thing standing between a mailbox and a free paid plan until the redeem
// endpoint checks the session, so it is generated from crypto/rand and never
// derived from the address.
const promoTokenBytes = 24

// reactivationMailer is the slice of notify.Notifier this sweeper needs; tests
// substitute a recorder. Mirrors expiryMailer rather than sharing it, so a
// change to one campaign's mail surface cannot silently retype the other's.
type reactivationMailer interface {
	SendHTML(to, subject, textBody, htmlBody string) error
}

// campaignSpec names the campaign a sweep runs under and how much of it runs.
//
// It exists so the sweep is addressable by campaign rather than hardcoded to
// one: the integration tests share the production database, and a test that
// ran the live campaign would stamp sent_at on real recipients who never got a
// letter — silently burning the send. Under its own campaign name the same
// code path is exercised against real data with the live funnel untouched.
type campaignSpec struct {
	Campaign string
	Variant  string
	MinAge   time.Duration
	PerRun   int
}

// liveReactivationSpec is the campaign the server actually mails.
func liveReactivationSpec() campaignSpec {
	return campaignSpec{
		Campaign: reactivationCampaign,
		Variant:  reactivationVariant,
		MinAge:   reactivationMinAge,
		PerRun:   reactivationSendPerRun,
	}
}

// campaignCandidate is one account the sweeper is about to enroll.
type campaignCandidate struct {
	UserID uuid.UUID
	Email  string
}

// pendingSend is one enrolled row that has not been delivered yet.
type pendingSend struct {
	ID    uuid.UUID
	Email string
	Token string
}

// newPromoToken returns a fresh opaque promo token.
func newPromoToken() (string, error) {
	buf := make([]byte, promoTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// promoLink builds the tracked URL that goes in the letter.
//
// It points at the console's own /promo/{token} page rather than at a backend
// redirect: on the console host the ingress routes only /api, /auth, /mcp and
// a few fixed paths to the backend, and everything else — including any short
// vanity path — is served by the frontend. A link the backend can never
// receive is a dead link, however pretty it reads in an email.
//
// The page records the click through the public click endpoint before it asks
// for a session, so the gap between "clicked" and "redeemed" stays visible:
// that gap is what says whether the offer or the login wall is losing people.
func promoLink(publicBaseURL, token string) string {
	return fmt.Sprintf("%s/promo/%s", publicBaseURL, token)
}

// promoPixelURL builds the open-tracking image URL for a recipient.
//
// It points at the backend directly, unlike the promo link: /api is one of the
// few prefixes the console ingress routes to the backend, so this is the one
// shape of tracked URL a mail client can actually fetch. The .gif suffix is
// cosmetic — some clients and proxies are happier fetching something that
// looks like an image — and the handler matches on the token before it.
func promoPixelURL(publicBaseURL, token string) string {
	return fmt.Sprintf("%s/api/v1/promo/pixel/%s.gif", publicBaseURL, token)
}

// promoHeroURL builds the URL of a campaign banner served from the console's
// static assets. The console frontend answers every path the ingress does not
// hand to the backend, and /email/* is unauthenticated, so a mail client can
// fetch it without a session.
func promoHeroURL(publicBaseURL, name string) string {
	return fmt.Sprintf("%s/email/%s.png", publicBaseURL, name)
}

// SweepReactivation runs one pass of the dormant-account campaign in three
// phases: enroll everyone newly eligible, deliver whatever is still unsent,
// and backfill conversions.
//
// Enrollment and delivery are separate on purpose. The row is written first,
// so the unique index on (campaign, user_id) is what guarantees one letter per
// person — if delivery and dedup shared a step, an SMTP timeout after a
// successful send would look like "not sent yet" on the next tick and mail the
// same person again.
//
// publicBaseURL empty disables the campaign entirely: without it the letter
// would carry a broken link, and a broken link is worse than no letter.
func SweepReactivation(ctx context.Context, pool *pgxpool.Pool, mailer reactivationMailer, publicBaseURL string, now time.Time) {
	if mailer == nil || publicBaseURL == "" {
		return
	}
	sweepCampaign(ctx, pool, mailer, publicBaseURL, now, liveReactivationSpec())
}

// sweepCampaign is SweepReactivation's body for one named campaign.
func sweepCampaign(ctx context.Context, pool *pgxpool.Pool, mailer reactivationMailer, publicBaseURL string, now time.Time, spec campaignSpec) {
	enrollReactivation(ctx, pool, now, spec)
	deliverReactivation(ctx, pool, mailer, publicBaseURL, now, spec)
	backfillCampaignConversions(ctx, pool, now)
}

// enrollReactivation writes a row for every account that is now eligible.
//
// "Never deployed" is checked two ways — no build in a project they own, and
// no build they personally triggered anywhere. Either one is enough to
// disqualify: the cost of skipping someone who did ship is a missed letter,
// while the cost of the reverse is telling an active customer they have done
// nothing.
func enrollReactivation(ctx context.Context, pool *pgxpool.Pool, now time.Time, spec campaignSpec) {
	rows, err := pool.Query(ctx, `
		SELECT ua.id, ua.email
		FROM user_accounts ua
		WHERE ua.account_kind = 'customer'
		  AND ua.email IS NOT NULL AND ua.email <> ''
		  AND ua.created_at <= $1
		  AND NOT EXISTS (
		      SELECT 1 FROM builds b
		      JOIN environments e ON e.id = b.environment_id
		      JOIN projects p ON p.id = e.project_id
		      WHERE p.owner_id = ua.id
		  )
		  AND NOT EXISTS (SELECT 1 FROM builds b2 WHERE b2.triggered_by = ua.id)
		  AND NOT EXISTS (
		      SELECT 1 FROM growth_campaign_sends s
		      WHERE s.campaign = $2 AND s.user_id = ua.id
		  )
		ORDER BY ua.created_at
		LIMIT $3
	`, now.Add(-spec.MinAge), spec.Campaign, spec.PerRun)
	if err != nil {
		log.Error().Err(err).Msg("reactivation: list candidates")
		return
	}
	candidates := make([]campaignCandidate, 0)
	for rows.Next() {
		var c campaignCandidate
		if err := rows.Scan(&c.UserID, &c.Email); err != nil {
			rows.Close()
			log.Error().Err(err).Msg("reactivation: scan candidate")
			return
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("reactivation: read candidates")
		return
	}

	for _, c := range candidates {
		token, err := newPromoToken()
		if err != nil {
			log.Error().Err(err).Msg("reactivation: mint token")
			return
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO growth_campaign_sends (campaign, variant, user_id, email, token, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6)
			ON CONFLICT (campaign, user_id) DO NOTHING
		`, spec.Campaign, spec.Variant, c.UserID, c.Email, token, now); err != nil {
			log.Error().Err(err).Str("user_id", c.UserID.String()).Msg("reactivation: enroll")
			continue
		}
	}
}

// deliverReactivation mails every enrolled row that has no sent_at yet.
// sent_at advances only after the SMTP call returns clean, so a mail outage
// retries on the next tick instead of burning the recipient.
func deliverReactivation(ctx context.Context, pool *pgxpool.Pool, mailer reactivationMailer, publicBaseURL string, now time.Time, spec campaignSpec) {
	rows, err := pool.Query(ctx, `
		SELECT id, email, token
		FROM growth_campaign_sends
		WHERE campaign = $1 AND sent_at IS NULL
		ORDER BY created_at
		LIMIT $2
	`, spec.Campaign, spec.PerRun)
	if err != nil {
		log.Error().Err(err).Msg("reactivation: list pending sends")
		return
	}
	pending := make([]pendingSend, 0)
	for rows.Next() {
		var p pendingSend
		if err := rows.Scan(&p.ID, &p.Email, &p.Token); err != nil {
			rows.Close()
			log.Error().Err(err).Msg("reactivation: scan pending send")
			return
		}
		pending = append(pending, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("reactivation: read pending sends")
		return
	}

	for _, p := range pending {
		link := promoLink(publicBaseURL, p.Token)
		subject, body := notify.ComposeReactivation("Startup", reactivationGrantDays, link)
		htmlBody := notify.ComposeReactivationHTML("Startup", reactivationGrantDays, link, promoPixelURL(publicBaseURL, p.Token), promoHeroURL(publicBaseURL, "hero-reactivation"))
		if err := mailer.SendHTML(p.Email, subject, body, htmlBody); err != nil {
			log.Error().Err(err).Str("email", p.Email).Msg("reactivation: send failed, will retry")
			continue
		}
		if _, err := pool.Exec(ctx, `
			UPDATE growth_campaign_sends SET sent_at = $2, updated_at = $2 WHERE id = $1
		`, p.ID, now); err != nil {
			log.Error().Err(err).Str("send_id", p.ID.String()).Msg("reactivation: mark sent")
			continue
		}
		log.Info().Str("campaign", spec.Campaign).Str("email", p.Email).Msg("reactivation: sent")
	}
}

// backfillCampaignConversions stamps converted_at from the recipient's first
// successful build after the letter went out.
//
// Conversion is measured on the build, not on the redeem: taking a free plan
// is a click, shipping an app is the outcome the campaign exists to buy, and
// conflating them would make an unused grant look like a win.
func backfillCampaignConversions(ctx context.Context, pool *pgxpool.Pool, now time.Time) {
	if _, err := pool.Exec(ctx, `
		UPDATE growth_campaign_sends s
		SET converted_at = sub.first_success, updated_at = $1
		FROM (
			SELECT s2.id, MIN(b.created_at) AS first_success
			FROM growth_campaign_sends s2
			JOIN projects p ON p.owner_id = s2.user_id
			JOIN environments e ON e.project_id = p.id
			JOIN builds b ON b.environment_id = e.id
			WHERE s2.sent_at IS NOT NULL
			  AND s2.converted_at IS NULL
			  AND b.status = 'success'
			  AND b.created_at > s2.sent_at
			GROUP BY s2.id
		) sub
		WHERE s.id = sub.id
	`, now); err != nil {
		log.Error().Err(err).Msg("reactivation: backfill conversions")
	}
}

// The fix wave: second letter to a first-wave recipient who redeemed the plan
// and then stalled without a single build. The first wave's own data located
// the stall inside the product -- the git-import page could only connect
// GitHub, every other path was a drawn button with a 503 behind it -- so this
// wave announces the repair instead of repeating the offer.
//
// reactivationFixMinAge is measured from the REDEEM, not from signup: the
// letter says "you activated and then nothing", and mailing it an hour after
// the redeem would say "we watch you in real time" instead.
const (
	reactivationFixCampaign = "reactivation-git-url-fix"
	reactivationFixMinAge   = 24 * time.Hour
)

// liveReactivationFixSpec is the fix wave the server actually mails.
func liveReactivationFixSpec() campaignSpec {
	return campaignSpec{
		Campaign: reactivationFixCampaign,
		Variant:  reactivationVariant,
		MinAge:   reactivationFixMinAge,
		PerRun:   reactivationSendPerRun,
	}
}

// SweepReactivationFix runs one pass of the fix wave. Conversions are
// backfilled FIRST: a recipient who shipped since the last tick must fall out
// of the target set before enrollment reads it, or the letter tells someone
// with a live app that they never deployed.
func SweepReactivationFix(ctx context.Context, pool *pgxpool.Pool, mailer reactivationMailer, publicBaseURL string, now time.Time) {
	if mailer == nil || publicBaseURL == "" {
		return
	}
	sweepFixCampaign(ctx, pool, mailer, publicBaseURL, now, liveReactivationFixSpec(), reactivationCampaign)
}

// sweepFixCampaign is SweepReactivationFix's body for one named campaign pair;
// tests run it under their own names so the live funnel stays untouched.
func sweepFixCampaign(ctx context.Context, pool *pgxpool.Pool, mailer reactivationMailer, publicBaseURL string, now time.Time, spec campaignSpec, sourceCampaign string) {
	backfillCampaignConversions(ctx, pool, now)
	enrollReactivationFix(ctx, pool, now, spec, sourceCampaign)
	deliverReactivationFix(ctx, pool, mailer, publicBaseURL, now, spec)
}

// enrollReactivationFix targets first-wave rows that were redeemed at least
// MinAge ago and never converted. The build checks are repeated verbatim from
// the first wave's enrollment even though converted_at should already cover
// success: a recipient mid-first-build has moved past the connect screen, and
// a letter about the connect screen would land as noise.
func enrollReactivationFix(ctx context.Context, pool *pgxpool.Pool, now time.Time, spec campaignSpec, sourceCampaign string) {
	rows, err := pool.Query(ctx, `
		SELECT s.user_id, s.email
		FROM growth_campaign_sends s
		WHERE s.campaign = $1
		  AND s.redeemed_at IS NOT NULL
		  AND s.converted_at IS NULL
		  AND s.redeemed_at <= $2
		  AND NOT EXISTS (
		      SELECT 1 FROM builds b
		      JOIN environments e ON e.id = b.environment_id
		      JOIN projects p ON p.id = e.project_id
		      WHERE p.owner_id = s.user_id
		  )
		  AND NOT EXISTS (SELECT 1 FROM builds b2 WHERE b2.triggered_by = s.user_id)
		  AND NOT EXISTS (
		      SELECT 1 FROM growth_campaign_sends f
		      WHERE f.campaign = $3 AND f.user_id = s.user_id
		  )
		ORDER BY s.redeemed_at
		LIMIT $4
	`, sourceCampaign, now.Add(-spec.MinAge), spec.Campaign, spec.PerRun)
	if err != nil {
		log.Error().Err(err).Msg("reactivation fix: list candidates")
		return
	}
	candidates := make([]campaignCandidate, 0)
	for rows.Next() {
		var c campaignCandidate
		if err := rows.Scan(&c.UserID, &c.Email); err != nil {
			rows.Close()
			log.Error().Err(err).Msg("reactivation fix: scan candidate")
			return
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("reactivation fix: read candidates")
		return
	}

	for _, c := range candidates {
		token, err := newPromoToken()
		if err != nil {
			log.Error().Err(err).Msg("reactivation fix: mint token")
			return
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO growth_campaign_sends (campaign, variant, user_id, email, token, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6)
			ON CONFLICT (campaign, user_id) DO NOTHING
		`, spec.Campaign, spec.Variant, c.UserID, c.Email, token, now); err != nil {
			log.Error().Err(err).Str("user_id", c.UserID.String()).Msg("reactivation fix: enroll")
			continue
		}
	}
}

// deliverReactivationFix mails every enrolled fix-wave row that has no sent_at
// yet, with the recipient's real plan expiry in the letter when the billing
// row still has one. The date is decoration, not logic: a missing expiry
// drops the date from the sentence rather than blocking the send.
func deliverReactivationFix(ctx context.Context, pool *pgxpool.Pool, mailer reactivationMailer, publicBaseURL string, now time.Time, spec campaignSpec) {
	rows, err := pool.Query(ctx, `
		SELECT s.id, s.email, s.token,
		       COALESCE(to_char(exp.plan_expires_at, 'DD.MM.YYYY'), '')
		FROM growth_campaign_sends s
		LEFT JOIN LATERAL (
		    SELECT ba.plan_expires_at
		    FROM projects p
		    JOIN billing_accounts ba ON ba.org_id = p.org_id
		    WHERE p.owner_id = s.user_id AND ba.plan = $3
		    ORDER BY p.created_at
		    LIMIT 1
		) exp ON true
		WHERE s.campaign = $1 AND s.sent_at IS NULL
		ORDER BY s.created_at
		LIMIT $2
	`, spec.Campaign, spec.PerRun, reactivationPlan)
	if err != nil {
		log.Error().Err(err).Msg("reactivation fix: list pending sends")
		return
	}
	type fixSend struct {
		pendingSend
		Expires string
	}
	pending := make([]fixSend, 0)
	for rows.Next() {
		var p fixSend
		if err := rows.Scan(&p.ID, &p.Email, &p.Token, &p.Expires); err != nil {
			rows.Close()
			log.Error().Err(err).Msg("reactivation fix: scan pending send")
			return
		}
		pending = append(pending, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("reactivation fix: read pending sends")
		return
	}

	for _, p := range pending {
		link := promoLink(publicBaseURL, p.Token)
		subject, body := notify.ComposeReactivationFix("Startup", p.Expires, link)
		htmlBody := notify.ComposeReactivationFixHTML("Startup", p.Expires, link, promoPixelURL(publicBaseURL, p.Token), promoHeroURL(publicBaseURL, "hero-git-url"))
		if err := mailer.SendHTML(p.Email, subject, body, htmlBody); err != nil {
			log.Error().Err(err).Str("email", p.Email).Msg("reactivation fix: send failed, will retry")
			continue
		}
		if _, err := pool.Exec(ctx, `
			UPDATE growth_campaign_sends SET sent_at = $2, updated_at = $2 WHERE id = $1
		`, p.ID, now); err != nil {
			log.Error().Err(err).Str("send_id", p.ID.String()).Msg("reactivation fix: mark sent")
			continue
		}
		log.Info().Str("campaign", spec.Campaign).Str("email", p.Email).Msg("reactivation fix: sent")
	}
}

type promoTokenRequest struct {
	Token string `json:"token" binding:"required"`
}

// promoTokenHexLen is the exact length of a well-formed promo token. The click
// endpoint is public, so anything off-shape is rejected before it reaches the
// database rather than being turned into a query.
const promoTokenHexLen = promoTokenBytes * 2

// RecordPromoClick stamps the click on a promo token.
//
// Public and unauthenticated on purpose: the recipient arrives from an email
// with no session, and the whole point of the endpoint is to record that they
// arrived BEFORE the login wall gets a chance to hide it. Nothing is granted
// here and nothing is disclosed — the response is the same 204 for a live
// token and an unknown one, so it cannot be used to test whether an address
// was mailed.
//
// @ID          recordPromoClick
// @Summary     Record a campaign promo-link click
// @Description Marks a promo token as clicked. Public, idempotent, and answers identically for unknown tokens.
// @Tags        growth
// @Accept      json
// @Param       body body promoTokenRequest true "Promo token"
// @Success     204
// @Failure     400 {object} map[string]string
// @Router      /promo/click [post]
func (h *Handler) RecordPromoClick(c *gin.Context) {
	var body promoTokenRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(body.Token) != promoTokenHexLen {
		c.Status(http.StatusNoContent)
		return
	}
	if _, err := h.pool.Exec(c.Request.Context(), `
		UPDATE growth_campaign_sends
		SET clicked_at = COALESCE(clicked_at, $2), updated_at = $2
		WHERE token = $1
	`, body.Token, time.Now().UTC()); err != nil {
		log.Error().Err(err).Msg("promo click: record")
	}
	c.Status(http.StatusNoContent)
}

// promoPixelGIF is a 1x1 transparent GIF, the smallest thing a mail client
// will fetch and render without leaving a visible mark in the letter.
var promoPixelGIF = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00,
	0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00,
	0x00, 0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02,
	0x44, 0x01, 0x00, 0x3b,
}

// RecordPromoOpen stamps the open on a promo token and returns the pixel.
//
// Public and unauthenticated: a mail client fetches this with no session, from
// an address the platform has never seen. It answers with the same image for a
// live token, an unknown token and a malformed one, so the endpoint cannot be
// used to test whether an address was mailed, and it never fails the fetch —
// a broken image in the letter is a visible defect for the recipient and buys
// nothing.
//
// What it measures is a floor. Gmail and friends proxy remote images, which
// can register an open the recipient never performed, and any client with
// images off registers nothing at all. opened_at uses COALESCE so it records
// the first fetch only: later proxy re-fetches must not drag the timestamp
// forward past the click.
//
// @ID          recordPromoOpen
// @Summary     Campaign open-tracking pixel
// @Description Returns a 1x1 GIF and records the first open of a promo token. Public; answers identically for unknown tokens.
// @Tags        growth
// @Produce     image/gif
// @Param       token path string true "Promo token, optionally suffixed .gif"
// @Success     200 {string} string "GIF89a"
// @Router      /promo/pixel/{token} [get]
func (h *Handler) RecordPromoOpen(c *gin.Context) {
	token := strings.TrimSuffix(c.Param("token"), ".gif")
	if len(token) == promoTokenHexLen {
		if _, err := h.pool.Exec(c.Request.Context(), `
			UPDATE growth_campaign_sends
			SET opened_at = COALESCE(opened_at, $2), updated_at = $2
			WHERE token = $1
		`, token, time.Now().UTC()); err != nil {
			log.Error().Err(err).Msg("promo open: record")
		}
	}
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Data(http.StatusOK, "image/gif", promoPixelGIF)
}

type redeemPromoRequest struct {
	Token string `json:"token" binding:"required"`
}

// RedeemPromo grants the campaign's plan to the authenticated caller.
//
// The token identifies a send, not a bearer credential: the row's user_id must
// equal the caller's own id, so a forwarded link cannot be spent by whoever
// received it. Redeeming twice is a no-op that still answers 200 — the console
// calls this on page load and a retry must not read as a failure.
//
// A caller who is already on a paid plan keeps it. The upsert refuses to touch
// anything but a free account, which stops the grant from shortening a paying
// customer's own term to 30 days.
//
// @ID          redeemPromo
// @Summary     Redeem a campaign promo token
// @Description Grants the campaign plan for a fixed term to the caller's org. The token must belong to the calling user.
// @Tags        growth
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body     redeemPromoRequest true "Promo token"
// @Success     200  {object} map[string]interface{}
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Failure     403  {object} map[string]string
// @Failure     404  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Router      /promo/redeem [post]
func (h *Handler) RedeemPromo(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	var body redeemPromoRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()

	var sendID, ownerID uuid.UUID
	var redeemedAt *time.Time
	err := h.pool.QueryRow(ctx, `
		SELECT id, user_id, redeemed_at FROM growth_campaign_sends WHERE token = $1
	`, body.Token).Scan(&sendID, &ownerID, &redeemedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read promo")
		return
	}
	if ownerID != claims.UserID {
		respondForbidden(c)
		return
	}
	if redeemedAt != nil {
		c.JSON(http.StatusOK, gin.H{"plan": reactivationPlan, "days": reactivationGrantDays, "already_redeemed": true})
		return
	}

	var orgID string
	err = h.pool.QueryRow(ctx, `
		SELECT org_id FROM projects
		WHERE owner_id = $1 AND org_id IS NOT NULL
		ORDER BY created_at
		LIMIT 1
	`, claims.UserID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && orgID == "") {
		respondError(c, http.StatusBadRequest, "no organization to grant the plan to")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve organization")
		return
	}

	granted, err := h.applyPromoGrant(ctx, sendID, orgID, now)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to grant plan")
		return
	}
	h.recordAudit(ctx, claims.UserID, auditEntry{
		Action:       "RedeemPromo",
		ResourceKind: "BillingAccount",
		ResourceName: reactivationPlan,
		Outcome:      auditOutcomeSuccess,
		Metadata:     map[string]any{"org_id": orgID, "campaign": reactivationCampaign, "granted": granted},
	})
	c.JSON(http.StatusOK, gin.H{
		"plan":             reactivationPlan,
		"days":             reactivationGrantDays,
		"granted":          granted,
		"already_redeemed": false,
	})
}

// applyPromoGrant marks the send redeemed and upserts the term, in one
// transaction so a grant can never exist without the row that explains where
// it came from. It reports whether the plan actually moved: false means the
// org was already paying and was deliberately left alone.
func (h *Handler) applyPromoGrant(ctx context.Context, sendID uuid.UUID, orgID string, now time.Time) (bool, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE growth_campaign_sends SET redeemed_at = $2, updated_at = $2 WHERE id = $1
	`, sendID, now); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, plan_expires_at, expiry_notified_at, updated_at)
		VALUES ($1, $2, $3::timestamptz, $3::timestamptz + make_interval(days => $4), NULL, $3::timestamptz)
		ON CONFLICT (org_id) DO UPDATE
		  SET plan               = EXCLUDED.plan,
		      plan_assigned_at   = EXCLUDED.plan_assigned_at,
		      plan_expires_at    = EXCLUDED.plan_expires_at,
		      expiry_notified_at = NULL,
		      updated_at         = EXCLUDED.updated_at
		  WHERE billing_accounts.plan = 'free'
	`, orgID, reactivationPlan, now, reactivationGrantDays)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// campaignStatsRow is one (campaign, variant, signup week) bucket of the
// growth report.
type campaignStatsRow struct {
	Campaign  string `json:"campaign"`
	Variant   string `json:"variant"`
	SignupWk  string `json:"signup_week"`
	Sent      int    `json:"sent"`
	Opened    int    `json:"opened"`
	Clicked   int    `json:"clicked"`
	Redeemed  int    `json:"redeemed"`
	Converted int    `json:"converted"`
}

// GetGrowthCampaigns reports the campaign funnel.
//
// Bucketed by signup week rather than send date: the question a reactivation
// campaign has to answer is how long an account can sit dormant before it is
// unreachable, and that is a property of when they registered.
//
// @ID          getGrowthCampaigns
// @Summary     Campaign funnel report (platform staff)
// @Description Sent/opened/clicked/redeemed/converted per campaign, variant and signup week.
// @Tags        growth
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{}
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Failure     500 {object} map[string]string
// @Router      /admin/growth/campaigns [get]
func (h *Handler) GetGrowthCampaigns(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) && !isPlatformAnalyst(claims) {
		respondForbidden(c)
		return
	}
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT s.campaign,
		       s.variant,
		       to_char(date_trunc('week', u.created_at), 'YYYY-MM-DD') AS signup_week,
		       count(*) FILTER (WHERE s.sent_at IS NOT NULL)      AS sent,
		       count(*) FILTER (WHERE s.opened_at IS NOT NULL)    AS opened,
		       count(*) FILTER (WHERE s.clicked_at IS NOT NULL)   AS clicked,
		       count(*) FILTER (WHERE s.redeemed_at IS NOT NULL)  AS redeemed,
		       count(*) FILTER (WHERE s.converted_at IS NOT NULL) AS converted
		FROM growth_campaign_sends s
		JOIN users u ON u.id = s.user_id
		GROUP BY s.campaign, s.variant, signup_week
		ORDER BY s.campaign, s.variant, signup_week
	`)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read campaigns")
		return
	}
	defer rows.Close()
	out := make([]campaignStatsRow, 0)
	for rows.Next() {
		var r campaignStatsRow
		if err := rows.Scan(&r.Campaign, &r.Variant, &r.SignupWk, &r.Sent, &r.Opened, &r.Clicked, &r.Redeemed, &r.Converted); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan campaigns")
			return
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read campaigns")
		return
	}
	c.JSON(http.StatusOK, gin.H{"campaigns": out})
}
