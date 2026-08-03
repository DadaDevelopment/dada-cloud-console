package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	feedbackDefaultLimit = 50
	feedbackMaxLimit     = 200
)

type feedbackItem struct {
	ID          uuid.UUID  `json:"id"`
	CreatedAt   time.Time  `json:"created_at"`
	AgeHours    int        `json:"age_hours"`
	Email       string     `json:"email"`
	OrgID       string     `json:"org_id"`
	Route       string     `json:"route"`
	Message     string     `json:"message"`
	Status      string     `json:"status"`
	ProjectID   *uuid.UUID `json:"project_id"`
	AppName     string     `json:"app_name"`
	CloudTaskID *uuid.UUID `json:"cloud_task_id"`
	Resolution  string     `json:"resolution"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	Autofixable bool       `json:"autofixable"`
}

// ListFeedback returns support tickets newest-first for the operator queue.
// Platform-admin only.
//
// @ID          listFeedback
// @Summary     List in-product feedback (platform-admin only)
// @Description Returns feedback rows with the sender's email, how long the ticket has been waiting, the project/app its route named, and whether that app has a connected repo (which is what makes an auto-fix run possible). Platform-admin only; every other caller gets 403.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Param       status query    string false "Filter by status: new, resolved (default: all)"
// @Param       limit  query    int    false "Max rows to return (default 50, max 200)"
// @Success     200 {object} map[string]interface{} "object with an items array and a new_count"
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Router      /admin/feedback [get]
func (h *Handler) ListFeedback(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isAdminReader(claims) {
		respondForbidden(c)
		return
	}

	var statusFilter *string
	if s := strings.TrimSpace(c.Query("status")); s != "" {
		statusFilter = &s
	}

	limit := feedbackDefaultLimit
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > feedbackMaxLimit {
		limit = feedbackMaxLimit
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT f.id, f.created_at, COALESCE(u.email, ''), COALESCE(f.org_id, ''), f.route, f.message,
		        f.status, f.project_id, f.app_name, f.cloud_task_id, f.resolution, f.resolved_at,
		        EXISTS (SELECT 1 FROM git_repos r WHERE r.project_id = f.project_id AND r.app_name = f.app_name)
		   FROM feedback f
		   LEFT JOIN user_accounts u ON u.id::text = f.user_sub
		  WHERE ($1::text IS NULL OR f.status = $1)
		  ORDER BY f.created_at DESC
		  LIMIT $2`,
		statusFilter, limit)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list feedback")
		return
	}
	defer rows.Close()

	now := time.Now()
	items := []feedbackItem{}
	for rows.Next() {
		var it feedbackItem
		if err := rows.Scan(&it.ID, &it.CreatedAt, &it.Email, &it.OrgID, &it.Route, &it.Message,
			&it.Status, &it.ProjectID, &it.AppName, &it.CloudTaskID, &it.Resolution, &it.ResolvedAt,
			&it.Autofixable); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read feedback")
			return
		}
		it.AgeHours = feedbackAgeHours(it.CreatedAt, now)
		items = append(items, it)
	}
	if rows.Err() != nil {
		respondError(c, http.StatusInternalServerError, "failed to read feedback")
		return
	}

	newCount := 0
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT count(*) FROM feedback WHERE status = 'new'`).Scan(&newCount); err != nil {
		newCount = 0
	}

	c.JSON(http.StatusOK, gin.H{"items": items, "new_count": newCount})
}

type resolveFeedbackRequest struct {
	Resolution string `json:"resolution"`
}

// ResolveFeedback closes a ticket with a note. Platform-admin only.
//
// @ID          resolveFeedback
// @Summary     Resolve a feedback ticket (platform-admin only)
// @Description Marks the ticket resolved and stores what was done about it, so the queue reflects reality instead of memory. Platform-admin only.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id   path string                 true  "Feedback UUID"
// @Param       body body resolveFeedbackRequest false "Resolution note"
// @Success     200 {object} map[string]string
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Failure     404 {object} map[string]string
// @Router      /admin/feedback/{id}/resolve [post]
func (h *Handler) ResolveFeedback(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondNotFound(c)
		return
	}

	var req resolveFeedbackRequest
	_ = c.ShouldBindJSON(&req)

	tag, err := h.pool.Exec(c.Request.Context(),
		`UPDATE feedback SET status='resolved', resolution=$2, resolved_at=NOW() WHERE id=$1`,
		id, strings.TrimSpace(req.Resolution))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve feedback")
		return
	}
	if tag.RowsAffected() == 0 {
		respondNotFound(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "resolved"})
}

// AutofixFeedback points the auto-fix engine at a support ticket. Platform-admin
// only.
//
// @ID          autofixFeedback
// @Summary     Auto-fix the app a feedback ticket is about (platform-admin only)
// @Description Launches a DadaAgent auto-fix run against the repo connected to the app the ticket's route named, using the customer's own words as the failure context, and links the resulting cloud task back to the ticket. Requires the ticket to name a project and an app with a connected repo. Platform-admin only.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "Feedback UUID"
// @Success     202 {object} map[string]interface{} "object with the created cloud_task"
// @Failure     400 {object} map[string]string
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Failure     404 {object} map[string]string
// @Failure     502 {object} map[string]string
// @Failure     503 {object} map[string]string
// @Router      /admin/feedback/{id}/autofix [post]
func (h *Handler) AutofixFeedback(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		respondNotFound(c)
		return
	}

	var projectID *uuid.UUID
	var appName, message string
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT project_id, app_name, message FROM feedback WHERE id=$1`, id).
		Scan(&projectID, &appName, &message)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read feedback")
		return
	}
	if projectID == nil || appName == "" {
		respondError(c, http.StatusBadRequest, "feedback does not name a project and app to fix")
		return
	}

	envID, err := h.feedbackEnvForApp(c.Request.Context(), *projectID, appName)
	if err != nil {
		respondError(c, http.StatusBadRequest, "no connected git repo for app")
		return
	}

	row, err := h.launchAutofix(c.Request.Context(), autofixLaunch{
		ProjectID: *projectID, EnvID: envID, AppName: appName,
		ErrText: feedbackAutofixContext(message), ActorID: claims.UserID,
	})
	if err != nil {
		respondAutofixError(c, err)
		return
	}

	if _, err := h.pool.Exec(c.Request.Context(),
		`UPDATE feedback SET cloud_task_id=$2, status='in_progress' WHERE id=$1`, id, row.ID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to link cloud task to feedback")
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"cloud_task": row})
}

// feedbackAutofixContext frames a customer's message for the agent. The
// engine's other callers hand it machine-generated text -- a build-failure
// summary or an LLM crash diagnosis -- so an unlabelled paragraph of prose
// would read as if the platform had already diagnosed something. Saying who
// wrote it keeps the agent honest about what it actually knows.
func feedbackAutofixContext(message string) string {
	return "User-reported issue (support ticket, the customer's own words):\n\n" + strings.TrimSpace(message)
}
