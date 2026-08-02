package api

import (
	"context"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const feedbackMessageMaxLen = 4000

type submitFeedbackRequest struct {
	Message string `json:"message"`
	Route   string `json:"route"`
}

func feedbackOrgID(claims *auth.Claims) string {
	if claims == nil {
		return ""
	}
	for org := range claims.OrgRoles() {
		return org
	}
	return ""
}

var (
	feedbackProjectRe = regexp.MustCompile(`/projects/([0-9a-fA-F-]{36})`)
	feedbackAppRe     = regexp.MustCompile(`/apps/([^/?#]+)`)
)

// parseFeedbackRoute extracts the project and app a ticket was written from.
// The console sends the console route as-is, so a message left on
// /projects/<uuid>/apps/<name>/settings carries the target the person was
// looking at when they gave up -- which is what lets the auto-fix engine aim
// at a support ticket instead of only at a crash alert. A route that names
// neither yields a nil project and an empty app name, and the ticket stays
// human-triaged.
func parseFeedbackRoute(route string) (*uuid.UUID, string) {
	var projectID *uuid.UUID
	if m := feedbackProjectRe.FindStringSubmatch(route); len(m) == 2 {
		if id, err := uuid.Parse(m[1]); err == nil {
			projectID = &id
		}
	}
	appName := ""
	if m := feedbackAppRe.FindStringSubmatch(route); len(m) == 2 {
		appName = m[1]
	}
	return projectID, appName
}

// @ID          submitFeedback
// @Summary     Submit in-product feedback
// @Description Records a short feedback message with the console route it was sent from, resolves the project/app that route names, and emails the operator so a ticket cannot sit unread. Works with or without a bearer token; if present, the caller's identity is attached.
// @Tags        feedback
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body     submitFeedbackRequest true "Feedback message"
// @Success     201  {object} map[string]string
// @Failure     400  {object} map[string]string
// @Router      /feedback [post]
func (h *Handler) SubmitFeedback(c *gin.Context) {
	var req submitFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		respondError(c, http.StatusBadRequest, "message must not be empty")
		return
	}
	if len(message) > feedbackMessageMaxLen {
		respondError(c, http.StatusBadRequest, "message must be at most 4000 characters")
		return
	}

	var userSub, orgID *string
	senderEmail := ""
	if claims, ok := h.optionalClaims(c); ok {
		sub := claims.UserID.String()
		userSub = &sub
		senderEmail = claims.Email
		if org := feedbackOrgID(claims); org != "" {
			orgID = &org
		}
	}

	projectID, appName := parseFeedbackRoute(req.Route)

	var id uuid.UUID
	if err := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO feedback (user_sub, org_id, route, message, project_id, app_name)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		userSub, orgID, req.Route, message, projectID, appName,
	).Scan(&id); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record feedback")
		return
	}

	h.notifyFeedback(feedbackNotice{
		ID:          id,
		SenderEmail: senderEmail,
		OrgID:       derefOr(orgID, ""),
		Route:       req.Route,
		Message:     message,
		AppName:     appName,
	})

	c.JSON(http.StatusCreated, gin.H{"status": "ok", "id": id})
}

type feedbackNotice struct {
	ID          uuid.UUID
	SenderEmail string
	OrgID       string
	Route       string
	Message     string
	AppName     string
}

// notifyFeedback mails the operator one message per ticket, off the request's
// hot path. Fire-and-forget by the same rule as notifyPaymentSuccess: a mail
// outage must never turn a customer's successful submit into an error. The
// alternative -- what shipped in 040 -- is that a ticket is seen only when
// somebody remembers to run psql, and the live table proves that does not
// happen: four tickets, two of them the same pain, unread for days.
func (h *Handler) notifyFeedback(n feedbackNotice) {
	if h.auditNotifier == nil || h.auditNotifyEmail == "" {
		return
	}
	notifier := h.auditNotifier
	to := h.auditNotifyEmail
	link := h.cfg.PublicBaseURL + "/admin/feedback"

	go func() {
		subject, body := notify.ComposeFeedback(n.SenderEmail, n.OrgID, n.Route, n.Message, n.AppName, link)
		if err := notifier.Send(to, subject, body); err != nil {
			log.Printf("feedback: operator notice for %s to %s failed: %v", n.ID, to, err)
		}
	}()
}

// derefOr reads an optional string column into a plain value.
func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// feedbackEnvForApp finds the environment an app's connected repo lives in.
// Auto-fix needs a repo anyway, so git_repos is both the environment lookup
// and the "is this ticket fixable by an agent at all" test in one query.
func (h *Handler) feedbackEnvForApp(ctx context.Context, projectID uuid.UUID, appName string) (uuid.UUID, error) {
	var envID uuid.UUID
	err := h.pool.QueryRow(ctx,
		`SELECT environment_id FROM git_repos WHERE project_id=$1 AND app_name=$2 ORDER BY created_at DESC LIMIT 1`,
		projectID, appName).Scan(&envID)
	return envID, err
}

// feedbackAgeHours renders how long a ticket has been waiting, which is the
// number that decides what to triage first.
func feedbackAgeHours(createdAt, now time.Time) int {
	return int(now.Sub(createdAt).Hours())
}
