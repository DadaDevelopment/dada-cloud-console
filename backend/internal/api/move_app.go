package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MoveMovableItem is one resource the move-impact scan classified as safely
// portable to the target project (ADR-014 Phase 1). Env vars are reported as a
// single synthetic {kind:"EnvVars", name:"<n> vars"} entry rather than one row
// per key.
type MoveMovableItem struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

// MoveBlockerItem is one reason a move cannot proceed yet — stateful resources
// that Phase 1 refuses to touch (persistent storage, attached databases).
type MoveBlockerItem struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// MoveImpact is the full move-impact preview for one app.
type MoveImpact struct {
	App             string            `json:"app"`
	SrcProject      string            `json:"src_project"`
	TargetProject   string            `json:"target_project"`
	TargetEnvID     uuid.UUID         `json:"target_env_id"`
	TargetNamespace string            `json:"target_namespace"`
	Movable         []MoveMovableItem `json:"movable"`
	Blockers        []MoveBlockerItem `json:"blockers"`
	NameCollision   bool              `json:"name_collision"`
	CanMove         bool              `json:"can_move"`
}

const (
	moveBlockerReasonVolume   = "persistent storage cannot cross namespaces yet (ADR-014 Phase 2)"
	moveBlockerReasonDatabase = "attached database (ADR-014 Phase 3)"
)

// targetEnvForProject resolves the environment a MoveApp should land in: the
// project's default environment if set, otherwise its oldest environment.
// Mirrors the platform's "one implicit environment per project" model
// (env-collapse) — there is normally exactly one candidate.
func (h *Handler) targetEnvForProject(ctx context.Context, targetProjectID uuid.UUID) (envID uuid.UUID, envName, namespace string, err error) {
	err = h.pool.QueryRow(ctx, `
		SELECT e.id, e.name, e.namespace
		FROM environments e
		JOIN projects p ON p.id = e.project_id
		WHERE e.project_id = $1
		ORDER BY (e.name = p.default_environment) DESC, e.created_at ASC
		LIMIT 1
	`, targetProjectID).Scan(&envID, &envName, &namespace)
	return
}

// computeMoveImpact classifies an app's children into movable vs. blocking for
// a prospective move from (srcProjectID, srcEnvID) to targetProjectID, reusing
// the same console-impact scan doDeleteApp/DeleteAppImpact rely on.
func (h *Handler) computeMoveImpact(ctx context.Context, srcProjectID, srcEnvID uuid.UUID, appName string, targetProjectID uuid.UUID) (MoveImpact, error) {
	var srcProjectName, targetProjectName string
	if err := h.pool.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, srcProjectID).Scan(&srcProjectName); err != nil {
		return MoveImpact{}, fmt.Errorf("look up src project: %w", err)
	}
	if err := h.pool.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, targetProjectID).Scan(&targetProjectName); err != nil {
		return MoveImpact{}, fmt.Errorf("look up target project: %w", err)
	}

	targetEnvID, _, targetNamespace, err := h.targetEnvForProject(ctx, targetProjectID)
	if err != nil {
		return MoveImpact{}, fmt.Errorf("resolve target environment: %w", err)
	}

	var summaryRaw []byte
	if err := h.pool.QueryRow(ctx, `
		SELECT summary_json FROM resource_snapshots
		WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3
	`, srcProjectID, srcEnvID, appName).Scan(&summaryRaw); err != nil {
		return MoveImpact{}, fmt.Errorf("look up src app snapshot: %w", err)
	}
	var desired struct {
		Volume map[string]any `json:"volume"`
	}
	if len(summaryRaw) > 0 {
		if err := json.Unmarshal(summaryRaw, &desired); err != nil {
			return MoveImpact{}, fmt.Errorf("parse src app snapshot: %w", err)
		}
	}

	children, err := h.consoleImpact(ctx, srcProjectID, srcEnvID, appName)
	if err != nil {
		return MoveImpact{}, fmt.Errorf("scan child resources: %w", err)
	}

	impact := MoveImpact{
		App:             appName,
		SrcProject:      srcProjectName,
		TargetProject:   targetProjectName,
		TargetEnvID:     targetEnvID,
		TargetNamespace: targetNamespace,
		Movable:         []MoveMovableItem{},
		Blockers:        []MoveBlockerItem{},
	}

	if len(desired.Volume) > 0 {
		impact.Blockers = append(impact.Blockers, MoveBlockerItem{
			Kind: "Volume", Name: appName, Reason: moveBlockerReasonVolume,
		})
	}
	for _, child := range children {
		if child.Kind == "ServiceDatabaseV2" {
			impact.Blockers = append(impact.Blockers, MoveBlockerItem{
				Kind: child.Kind, Name: child.Name, Reason: moveBlockerReasonDatabase,
			})
			continue
		}
		impact.Movable = append(impact.Movable, MoveMovableItem{
			Kind: child.Kind, Name: child.Name, Group: child.Group,
		})
	}

	var envVarCount int
	if err := h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM env_vars WHERE environment_id = $1 AND app_name = $2`,
		srcEnvID, appName,
	).Scan(&envVarCount); err != nil {
		return MoveImpact{}, fmt.Errorf("count env vars: %w", err)
	}
	if envVarCount > 0 {
		impact.Movable = append(impact.Movable, MoveMovableItem{
			Kind: "EnvVars", Name: fmt.Sprintf("%d vars", envVarCount), Group: impactGroupOther,
		})
	}

	if err := h.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3)`,
		targetProjectID, targetEnvID, appName,
	).Scan(&impact.NameCollision); err != nil {
		return MoveImpact{}, fmt.Errorf("check name collision: %w", err)
	}

	impact.CanMove = len(impact.Blockers) == 0 && !impact.NameCollision
	return impact, nil
}

// MoveAppImpact previews whether an app can move to another project under
// ADR-014 Phase 1 (stateless move only): it classifies the app's children into
// movable vs. blocking (persistent storage, attached database) and flags a name
// collision in the target project's environment.
//
// @ID          moveAppImpact
// @Summary     Preview the impact of moving an app to another project
// @Description Phase 1 (stateless move): classifies the app's children into movable resources and blockers (persistent volume, attached ServiceDatabaseV2), and flags a name collision in the target project. can_move is false whenever any blocker or collision is present.
// @Tags        apps
// @Produce     json
// @Security    BearerAuth
// @Param       projectId       path     string true "Project UUID"
// @Param       envId           path     string true "Environment UUID"
// @Param       appName         path     string true "App name"
// @Param       target_project_id query  string true "Destination project UUID"
// @Success     200             {object} MoveImpact
// @Failure     400             {object} map[string]string
// @Failure     401             {object} map[string]string
// @Failure     404             {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/move-impact [get]
func (h *Handler) MoveAppImpact(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	targetProjectID, err := uuid.Parse(c.Query("target_project_id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "target_project_id must be a valid UUID")
		return
	}
	if targetProjectID == projectID {
		respondError(c, http.StatusBadRequest, "target_project_id must differ from the app's current project")
		return
	}
	if _, err := h.requireMember(c, claims.UserID, targetProjectID); err != nil {
		return
	}

	var exists bool
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3)`,
		projectID, envID, appName,
	).Scan(&exists); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to look up app")
		return
	}
	if !exists {
		respondNotFound(c)
		return
	}

	impact, err := h.computeMoveImpact(c.Request.Context(), projectID, envID, appName, targetProjectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to compute move impact")
		return
	}
	c.JSON(http.StatusOK, impact)
}

// MoveApp enqueues an async MoveApp operation that re-homes a stateless app to
// another project (ADR-014 Phase 1). The gitops-agent worker's doMoveApp
// renders the app under the target project/environment, copies its env vars,
// commits, prunes the source git folder, and repoints resource_snapshots; see
// gitops-agent/internal/worker/dbwatcher.go.
//
// @ID          moveApp
// @Summary     Move an app to another project
// @Description Destructive-ish, asynchronous: re-homes a stateless app (no persistent volume, no attached database) into another project's environment. Requires write access on both the source and target project. Call move-impact first; returns 409 with the blocking reasons if the app is not movable. Returns 202 with an operation; poll it until terminal.
// @Tags        apps
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string             true "Project UUID"
// @Param       envId     path     string             true "Environment UUID"
// @Param       appName   path     string             true "App name"
// @Param       body      body     moveAppRequest      true "Destination project"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]interface{} "blockers preventing the move"
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/move [post]
func (h *Handler) MoveApp(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	var req moveAppRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TargetProjectID == uuid.Nil {
		respondError(c, http.StatusBadRequest, "target_project_id is required")
		return
	}
	if req.TargetProjectID == projectID {
		respondError(c, http.StatusBadRequest, "target_project_id must differ from the app's current project")
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, req.TargetProjectID); err != nil {
		return
	}

	var exists bool
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3)`,
		projectID, envID, appName,
	).Scan(&exists); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to look up app")
		return
	}
	if !exists {
		respondNotFound(c)
		return
	}

	impact, err := h.computeMoveImpact(c.Request.Context(), projectID, envID, appName, req.TargetProjectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to compute move impact")
		return
	}
	if !impact.CanMove {
		c.JSON(http.StatusConflict, gin.H{"error": "app cannot be moved", "blockers": impact.Blockers})
		return
	}

	payload := models.MoveAppPayload{
		AppName:         appName,
		TargetProjectID: req.TargetProjectID,
		TargetEnvID:     impact.TargetEnvID,
	}
	payloadBytes, _ := json.Marshal(payload)

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'MoveApp', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "App move queued"})
}

// moveAppRequest is the JSON body of POST .../apps/{appName}/move.
type moveAppRequest struct {
	TargetProjectID uuid.UUID `json:"target_project_id"`
}
