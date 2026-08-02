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
	"github.com/rs/zerolog/log"
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

// MoveBlockerItem is one reason a move cannot proceed yet. A persistent volume is
// the only blocker: its cross-namespace copy (ADR-014 Phase 2) is not yet
// implemented. An attached database is no longer a blocker — it moves with the
// app as an Orphan-safe re-point (Phase 3).
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

const moveBlockerReasonVolume = "persistent storage cannot cross namespaces yet (ADR-014 Phase 2)"

// classifyMoveChildren splits an app's children into what a move carries along
// (Movable) versus what still blocks it (Blockers). It is pure over its inputs so
// the classification — the one part of the impact scan with real branching — is
// unit-testable without a database.
//
// An attached ServiceDatabaseV2 is MOVABLE (ADR-014 Phase 3): the move re-points
// its CR to the target namespace as an Orphan-safe re-home. The logical database
// in the shared postgresql cluster never moves, so its data is never at risk.
//
// A persistent volume is still a Blocker: in-agent volume copy (ADR-014 Phase 2)
// is not implemented, so advertising it as movable would promise a move the
// gitops-agent worker deliberately refuses. Volume stays a hard blocker here even
// though the worker gates on MOVE_VOLUME_ENABLED — the console must never offer a
// move that would land on an empty PVC. Both sides flip together only once Phase 2
// copy ships.
func classifyMoveChildren(appName string, hasVolume bool, children []ImpactItem, envVarCount int) (movable []MoveMovableItem, blockers []MoveBlockerItem) {
	movable = []MoveMovableItem{}
	blockers = []MoveBlockerItem{}
	if hasVolume {
		blockers = append(blockers, MoveBlockerItem{Kind: "Volume", Name: appName, Reason: moveBlockerReasonVolume})
	}
	for _, child := range children {
		movable = append(movable, MoveMovableItem{Kind: child.Kind, Name: child.Name, Group: child.Group})
	}
	if envVarCount > 0 {
		movable = append(movable, MoveMovableItem{Kind: "EnvVars", Name: fmt.Sprintf("%d vars", envVarCount), Group: impactGroupOther})
	}
	return movable, blockers
}

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

	var envVarCount int
	if err := h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM env_vars WHERE environment_id = $1 AND app_name = $2`,
		srcEnvID, appName,
	).Scan(&envVarCount); err != nil {
		return MoveImpact{}, fmt.Errorf("count env vars: %w", err)
	}

	movable, blockers := classifyMoveChildren(appName, len(desired.Volume) > 0, children, envVarCount)
	impact := MoveImpact{
		App:             appName,
		SrcProject:      srcProjectName,
		TargetProject:   targetProjectName,
		TargetEnvID:     targetEnvID,
		TargetNamespace: targetNamespace,
		Movable:         movable,
		Blockers:        blockers,
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

// resolveManagedDatabaseName returns the logical database name of a
// ServiceDatabaseV2 snapshot, falling back to the resource name (the two match
// unless spec.database was set explicitly). Unlike lookupManagedDatabase it never
// writes an HTTP response, so it is safe to call from a best-effort background
// step. Returns "" when the snapshot is missing.
func (h *Handler) resolveManagedDatabaseName(ctx context.Context, projectID, envID uuid.UUID, resourceName string) string {
	var summaryRaw []byte
	if err := h.pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, resourceName,
	).Scan(&summaryRaw); err != nil {
		return ""
	}
	if database := serviceDatabaseName(summaryRaw); database != "" {
		return database
	}
	return resourceName
}

// startMoveSafetyBackups fires a best-effort logical backup of every managed
// database an about-to-move app carries, giving the operator a restore point
// captured immediately before the Orphan-safe re-point re-homes the DB's
// credentials secret (ADR-014 Phase 3).
//
// Best-effort by design: it is a no-op when Kanister is not configured, and a
// per-database failure is logged and swallowed rather than blocking the move. The
// re-point never touches the shared logical database, so a missing safety backup
// is a strictly smaller risk than refusing an otherwise-valid move. It reads the
// movable set the impact scan already computed, so it backs up exactly the
// databases the move will re-point.
func (h *Handler) startMoveSafetyBackups(ctx context.Context, projectID, envID uuid.UUID, impact MoveImpact, actor *uuid.UUID) {
	if h.kanister == nil || !h.kanister.Enabled() {
		return
	}
	for _, item := range impact.Movable {
		if item.Kind != "ServiceDatabaseV2" {
			continue
		}
		database := h.resolveManagedDatabaseName(ctx, projectID, envID, item.Name)
		if database == "" {
			continue
		}
		if _, err := h.startDBBackup(ctx, projectID, envID, item.Name, database, models.DBBackupKindPreMove, actor); err != nil {
			log.Warn().Err(err).Str("app", impact.App).Str("database", item.Name).
				Msg("move: pre-move safety backup failed; proceeding with the move (the re-point never touches the logical database)")
		}
	}
}

// MoveAppImpact previews whether an app can move to another project (ADR-014):
// it classifies the app's children into movable resources — including an attached
// ServiceDatabaseV2, carried along as an Orphan-safe re-point (Phase 3) — versus
// blockers (a persistent volume, whose in-agent copy is Phase-2 pending) and
// flags a name collision in the target project's environment.
//
// @ID          moveAppImpact
// @Summary     Preview the impact of moving an app to another project
// @Description Classifies the app's children into movable resources (env vars, PublicApi/domain, and an attached ServiceDatabaseV2 re-pointed as an Orphan-safe re-home) and blockers (a persistent volume, whose cross-namespace copy is not yet implemented), and flags a name collision in the target project. can_move is false whenever any blocker or collision is present.
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

// MoveApp enqueues an async MoveApp operation that re-homes an app to another
// project (ADR-014). The gitops-agent worker's doMoveApp renders the app under
// the target project/environment, copies its env vars, re-points any attached
// ServiceDatabaseV2 to the target namespace (an Orphan-safe re-home, Phase 3),
// commits, prunes the source git folder, and repoints resource_snapshots; see
// gitops-agent/internal/worker/dbwatcher.go. A best-effort pre-move backup of
// each attached database is taken first when Kanister is configured.
//
// @ID          moveApp
// @Summary     Move an app to another project
// @Description Destructive-ish, asynchronous: re-homes an app into another project's environment, carrying its env vars and re-pointing any attached ServiceDatabaseV2 as an Orphan-safe re-home (a best-effort pre-move backup is taken first). A persistent volume still blocks the move. Requires write access on both the source and target project. Call move-impact first; returns 409 with the blocking reasons if the app is not movable. Returns 202 with an operation; poll it until terminal.
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
	appName := c.Param("appName")
	var req moveAppRequest

	audit := func(opID uuid.UUID, outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			OperationID:   opID,
			Action:        "MoveApp",
			ResourceKind:  "App",
			ResourceName:  appName,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	reject := func(status int, reason string) {
		meta := map[string]any{"reason": reason, "status": status}
		if req.TargetProjectID != uuid.Nil {
			meta["target_project_id"] = req.TargetProjectID.String()
		}
		audit(uuid.Nil, auditOutcomeFailure, meta)
	}
	rejectErr := func(status int, reason, msg string) {
		reject(status, reason)
		respondError(c, status, msg)
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		reject(http.StatusForbidden, "not_a_writer_on_source")
		return
	}
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		rejectErr(http.StatusInternalServerError, "env_check_failed", "failed to verify environment")
		return
	} else if !ok {
		reject(http.StatusNotFound, "env_not_in_project")
		respondNotFound(c)
		return
	}

	if err := c.ShouldBindJSON(&req); err != nil || req.TargetProjectID == uuid.Nil {
		rejectErr(http.StatusBadRequest, "target_project_required", "target_project_id is required")
		return
	}
	if req.TargetProjectID == projectID {
		rejectErr(http.StatusBadRequest, "target_equals_source", "target_project_id must differ from the app's current project")
		return
	}
	if _, err := h.requireWriter(c, claims.UserID, req.TargetProjectID); err != nil {
		reject(http.StatusForbidden, "not_a_writer_on_target")
		return
	}

	var exists bool
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3)`,
		projectID, envID, appName,
	).Scan(&exists); err != nil {
		rejectErr(http.StatusInternalServerError, "lookup_failed", "failed to look up app")
		return
	}
	if !exists {
		reject(http.StatusNotFound, "not_found")
		respondNotFound(c)
		return
	}

	impact, err := h.computeMoveImpact(c.Request.Context(), projectID, envID, appName, req.TargetProjectID)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "impact_failed", "failed to compute move impact")
		return
	}
	if !impact.CanMove {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{
			"reason":            "blocked",
			"status":            http.StatusConflict,
			"target_project_id": req.TargetProjectID.String(),
			"blockers":          impact.Blockers,
		})
		c.JSON(http.StatusConflict, gin.H{"error": "app cannot be moved", "blockers": impact.Blockers})
		return
	}

	h.startMoveSafetyBackups(c.Request.Context(), projectID, envID, impact, &claims.UserID)

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
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	audit(op.ID, auditOutcomeSuccess, map[string]any{
		"target_project_id": req.TargetProjectID.String(),
		"target_env_id":     impact.TargetEnvID.String(),
	})

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "App move queued"})
}

// moveAppRequest is the JSON body of POST .../apps/{appName}/move.
type moveAppRequest struct {
	TargetProjectID uuid.UUID `json:"target_project_id"`
}
