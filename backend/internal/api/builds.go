package api

import (
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/wstoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// build mirrors the frontend Build shape.
type build struct {
	ID            uuid.UUID  `json:"id"`
	GitRepoID     uuid.UUID  `json:"git_repo_id"`
	EnvironmentID uuid.UUID  `json:"environment_id"`
	AppName       string     `json:"app_name"`
	Status        string     `json:"status"`
	Trigger       string     `json:"trigger"`
	CommitSHA     string     `json:"commit_sha"`
	CommitMessage *string    `json:"commit_message,omitempty"`
	HeadSHA       *string    `json:"head_sha,omitempty"`
	Branch        string     `json:"branch"`
	ImageURI      *string    `json:"image_uri,omitempty"`
	LogsRef       *string    `json:"logs_ref,omitempty"`
	PRNumber      *int       `json:"pr_number,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ErrorMessage  *string    `json:"error_message,omitempty"`
	FailReason    *string    `json:"fail_reason,omitempty"`
}

const buildSelectCols = `id, git_repo_id, environment_id, app_name, status, trigger,
		commit_sha, commit_message, head_sha, branch, image_uri, logs_ref, pr_number,
		started_at, finished_at, created_at, updated_at, error_message, fail_reason`

func scanBuild(s interface {
	Scan(dest ...any) error
}, b *build) error {
	return s.Scan(&b.ID, &b.GitRepoID, &b.EnvironmentID, &b.AppName, &b.Status, &b.Trigger,
		&b.CommitSHA, &b.CommitMessage, &b.HeadSHA, &b.Branch, &b.ImageURI, &b.LogsRef, &b.PRNumber,
		&b.StartedAt, &b.FinishedAt, &b.CreatedAt, &b.UpdatedAt, &b.ErrorMessage, &b.FailReason)
}

// ListBuilds returns the builds for an app in an environment.
//
// @ID          listBuilds
// @Summary     List builds for an app
// @Description Returns the build history for an app in an environment, most recent first. Read-only.
// @Tags        build
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with a builds array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/builds [get]
func (h *Handler) ListBuilds(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT `+buildSelectCols+`
		 FROM builds
		 WHERE environment_id = $1 AND app_name = $2
		 ORDER BY created_at DESC`,
		envID, appName,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query builds")
		return
	}
	defer rows.Close()

	builds := []build{}
	for rows.Next() {
		var b build
		if err := scanBuild(rows, &b); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan build")
			return
		}
		builds = append(builds, b)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading builds")
		return
	}

	c.JSON(http.StatusOK, gin.H{"builds": builds})
}

// loadProjectBuild loads a build by id and verifies it belongs to the project.
// Returns pgx.ErrNoRows if the build doesn't exist in the project.
func (h *Handler) loadProjectBuild(c *gin.Context, projectID, buildID uuid.UUID, b *build) error {
	row := h.pool.QueryRow(c.Request.Context(),
		`SELECT `+buildSelectCols+`
		 FROM builds b
		 WHERE b.id = $1
		   AND b.git_repo_id IN (SELECT id FROM git_repos WHERE project_id = $2)`,
		buildID, projectID,
	)
	return scanBuild(row, b)
}

// GetBuild returns a single build.
//
// @ID          getBuild
// @Summary     Get a build
// @Description Returns a single build by id. Read-only.
// @Tags        build
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       buildId   path     string true "Build UUID"
// @Success     200       {object} map[string]interface{} "object with the build"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/builds/{buildId} [get]
func (h *Handler) GetBuild(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	buildID, err := uuid.Parse(c.Param("buildId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	var b build
	if err := h.loadProjectBuild(c, projectID, buildID, &b); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load build")
		return
	}

	c.JSON(http.StatusOK, gin.H{"build": b})
}

// TriggerBuild enqueues a manual build for an app's linked repo.
//
// @ID          triggerBuild
// @Summary     Trigger a manual build
// @Description Queues a new build of the app's linked repository at its production branch HEAD. Builds are imperative — this returns 202 with the queued build (not an operation). Requires write access.
// @Tags        build
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     202       {object} map[string]interface{} "object with the queued build"
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/builds [post]
// placeholderCommitSHA mints the synthetic commit_sha a manual build starts with.
// The schema requires commit_sha (UNIQUE(git_repo_id, commit_sha)) but a manual
// trigger has no commit yet, and the value is never overwritten later — doing so
// would collide with a push build already sitting on the real commit. build-agent
// resolves the real HEAD afterwards into the separate head_sha column, which is
// what the console renders.
func placeholderCommitSHA() string {
	return "manual-" + time.Now().UTC().Format("20060102150405.000000")
}

func (h *Handler) TriggerBuild(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	// Resolve the linked repo for this app/env.
	var gitRepoID uuid.UUID
	var prodBranch string
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id, production_branch FROM git_repos
		 WHERE project_id = $1 AND environment_id = $2 AND app_name = $3`,
		projectID, envID, appName,
	).Scan(&gitRepoID, &prodBranch)
	if err == pgx.ErrNoRows {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "TriggerBuild",
			ResourceKind:  "Build",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": "no_linked_repo"},
		})
		respondError(c, http.StatusConflict, "this app has no linked git repository")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve linked repository")
		return
	}

	commitSHA := placeholderCommitSHA()

	var b build
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO builds
		   (git_repo_id, environment_id, app_name, commit_sha, branch, triggered_by, trigger, status)
		 VALUES ($1, $2, $3, $4, $5, $6, 'manual', 'queued')
		 RETURNING `+buildSelectCols,
		gitRepoID, envID, appName, commitSHA, prodBranch, claims.UserID,
	)
	if err := scanBuild(row, &b); err != nil {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "TriggerBuild",
			ResourceKind:  "Build",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": "queue_failed"},
		})
		respondError(c, http.StatusInternalServerError, "failed to queue build")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "TriggerBuild",
		ResourceKind:  "Build",
		ResourceName:  appName,
		Metadata:      map[string]any{"build_id": b.ID.String(), "branch": b.Branch},
	})
	h.notifyAuditEvent(claims, projectID, "TriggerBuild", appName)

	c.JSON(http.StatusAccepted, gin.H{"build": b})
}

// CancelBuild cancels an in-flight build.
//
// @ID          cancelBuild
// @Summary     Cancel a build
// @Description Cancels a queued or in-progress build. Returns 409 if the build already reached a terminal state. Requires write access.
// @Tags        build
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       buildId   path     string true "Build UUID"
// @Success     200       {object} map[string]interface{} "object with the updated build"
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/builds/{buildId}/cancel [post]
func (h *Handler) CancelBuild(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	buildID, err := uuid.Parse(c.Param("buildId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	// Confirm the build exists in this project first (don't leak existence).
	var existing build
	if err := h.loadProjectBuild(c, projectID, buildID, &existing); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load build")
		return
	}

	// Compare-and-set: only non-terminal builds become canceled.
	var b build
	row := h.pool.QueryRow(c.Request.Context(),
		`UPDATE builds
		 SET status = 'canceled', finished_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND status IN ('queued', 'detecting', 'building', 'pushing')
		 RETURNING `+buildSelectCols,
		buildID,
	)
	if err := scanBuild(row, &b); err == pgx.ErrNoRows {
		respondError(c, http.StatusConflict, "build already reached a terminal state")
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to cancel build")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: b.EnvironmentID,
		Action:        "CancelBuild",
		ResourceKind:  "Build",
		ResourceName:  b.AppName,
		Metadata:      map[string]any{"build_id": b.ID.String(), "status_before": "in_flight"},
	})

	c.JSON(http.StatusOK, gin.H{"build": b})
}

// GetBuildLogsToken issues a short-lived delegate token for the build log WebSocket.
//
// @ID          createBuildLogsToken
// @Summary     Issue a token for the build log stream
// @Description Issues a short-lived delegate token plus WebSocket URL the frontend uses to stream a build's logs from the build-agent. Requires write access. Returns 503 when the build-agent is not configured.
// @Tags        build
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       buildId   path     string true "Build UUID"
// @Success     200       {object} map[string]interface{} "object with token and ws_url"
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/builds/{buildId}/logs-token [post]
func (h *Handler) GetBuildLogsToken(c *gin.Context) {
	if h.cfg.BuildAgentTokenSecret == "" || h.cfg.BuildAgentWSURL == "" {
		respondError(c, http.StatusServiceUnavailable, "build log stream not configured")
		return
	}

	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	buildID, err := uuid.Parse(c.Param("buildId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	// Confirm the build belongs to this project before minting a token for it.
	var b build
	if err := h.loadProjectBuild(c, projectID, buildID, &b); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load build")
		return
	}

	token, err := wstoken.Sign(h.cfg.BuildAgentTokenSecret, wstoken.Claims{
		Build: buildID.String(),
		Exp:   time.Now().Add(90 * time.Second).Unix(),
	})
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to sign token")
		return
	}

	h.recordViewAudit(claims, auditActionViewBuildLogs, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: b.EnvironmentID,
		ResourceKind:  "Build",
		ResourceName:  b.AppName,
		Metadata: map[string]any{
			"build_id":     buildID.String(),
			"build_status": b.Status,
		},
	})

	c.JSON(http.StatusOK, gin.H{
		"token":  token,
		"ws_url": h.cfg.BuildAgentWSURL,
	})
}
