package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AdoptAppConfig pulls an app's git-held configuration into the console's
// model, so an app that was never created through the console becomes
// manageable through it instead of being broken by it.
//
// The console renders values.yaml out of its own database. Apps that arrived by
// another road -- infra services, bots, anything committed by hand into the
// app-of-apps -- carry configuration the database has never seen: a service
// port, a .env mount, environment variables pointing at Secrets made by hand.
// Until now the console had only two answers for them, and both were wrong.
// Rendering anyway deleted what the console could not express: on 2026-08-21
// one env-var save on internal/prod/telemost-bot removed eight secret
// references, moved the service port from 8000 to 8080 and unmounted the file
// holding the bot's token. Refusing the write instead (which the values merge
// and the clobber guard now do) protects the app but leaves its owner with no
// lever at all.
//
// Adoption is the third answer, and the one the product owes such an app: read
// what git says, record it as the console's own state, and from that point on a
// console render REPRODUCES the app rather than replacing it. Nothing is
// committed to git and nothing about the running app changes -- the operation
// only teaches the console what the app already is.
//
// @ID          adoptAppConfig
// @Summary     Adopt an app's existing configuration into the console
// @Description Reads the app's values.yaml from git and records what it finds (environment variables, including ones pointing at Secrets the console does not own, and the service port) as the console's own state. Nothing is committed to git and the running app is untouched. Use it on apps that were created outside the console before editing them through it. Asynchronous; the queued operation's validation_result names every key adopted.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     202       {object} map[string]interface{}
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/adopt-config [post]
func (h *Handler) AdoptAppConfig(c *gin.Context) {
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
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}
	if !h.requireK8sRuntime(c, projectID, envID) {
		return
	}

	op, err := enqueueAdoptAppConfigOp(c.Request.Context(), h.pool, claims.UserID, projectID, envID, appName)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to queue adoption")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "adopting the app's existing configuration; read the operation's validation_result for what was adopted",
	})
}

// adoptAppConfigPayload is the operation payload the gitops-agent reads. It
// carries a name and nothing else: everything adoption records is read from
// git by the agent, so no configuration passes through operations.payload,
// which is plaintext.
type adoptAppConfigPayload struct {
	AppName string `json:"app_name"`
}

func enqueueAdoptAppConfigOp(ctx context.Context, q pgxQuerier, actorID, projectID, envID uuid.UUID, appName string) (*models.Operation, error) {
	payload, err := json.Marshal(adoptAppConfigPayload{AppName: appName})
	if err != nil {
		return nil, err
	}
	var op models.Operation
	row := q.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'AdoptAppConfig', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		actorID, projectID, envID, appName, payload,
	)
	if err := scanOperation(row, &op); err != nil {
		return nil, err
	}
	return &op, nil
}
