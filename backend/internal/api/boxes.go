package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/boxcatalog"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Dada Box control plane, slice 1: the object and its lifecycle skeleton.
//
// Handler shape is copied from appservers.go line for line, and the order of the
// gates matters:
//
//	auth.GetClaims  -> 401
//	malformed id    -> 404 (not 400: a malformed id is indistinguishable from an
//	                  id the caller may not see, and 400 would confirm the format)
//	effectiveRole   -> pgx.ErrNoRows means 404, NEVER 403. 403 on a project the
//	                  caller cannot see is an existence oracle: it lets an
//	                  outsider enumerate project ids by status code.
//	canWrite(role)  -> 403 (the caller demonstrably belongs, so telling them they
//	                  lack permission leaks nothing)
//
// Mutations are async and go through the operations table (D3), so every mutating
// handler answers 202 with the operation to poll. The one deliberate exception in
// the whole feature is the box CLAIM fast path, which is slice 2 and does not use
// operations at all — a 5s worker poll is larger than the entire time-to-ready
// budget. Extend is synchronous because it only moves a timestamp in our own row.

// boxColumns is the SELECT list for a models.Box, in scanBox order.
const boxColumns = `id, project_id, environment_id, name, image, profile, region,
	 status, error_message, instance_ref, node_ref, ssh_host, ssh_port, mcp_url,
	 ttl_seconds, idle_timeout_seconds, expires_at, last_active_at, slept_at,
	 spend_cap_rub, spend_capped_at, last_sample_json, last_sample_at, app_server_id,
	 created_by, created_at, updated_at, deleted_at`

// rowScanner is the shared shape of pgx.Row and pgx.Rows for scanning one box.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanBox(row rowScanner, b *models.Box) error {
	return row.Scan(
		&b.ID, &b.ProjectID, &b.EnvironmentID, &b.Name, &b.Image, &b.Profile, &b.Region,
		&b.Status, &b.ErrorMessage, &b.InstanceRef, &b.NodeRef, &b.SSHHost, &b.SSHPort, &b.MCPURL,
		&b.TTLSeconds, &b.IdleTimeoutSeconds, &b.ExpiresAt, &b.LastActiveAt, &b.SleptAt,
		&b.SpendCapRub, &b.SpendCappedAt, &b.LastSampleJSON, &b.LastSampleAt, &b.AppServerID,
		&b.CreatedBy, &b.CreatedAt, &b.UpdatedAt, &b.DeletedAt,
	)
}

// maxBoxTTLSeconds is the ceiling on a single claim's lifetime before it sleeps.
// A box that sleeps is not destroyed, so the ceiling costs the customer nothing
// but bounds how long one tenant can hold a warm slot on a shared host.
const maxBoxTTLSeconds = 24 * 3600

// generateBoxName mints box-<8 hex> for a caller that named none. Random rather
// than a counter so a name never leaks how many boxes the platform has served.
func generateBoxName() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "box-" + hex.EncodeToString(buf), nil
}

// resolveBox loads one live-or-tombstoned box by project + name and writes the
// 404 itself when it does not exist. Deleted boxes are excluded: the name is
// reusable after deletion, so "the deleted one" is not addressable.
func (h *Handler) resolveBox(c *gin.Context, projectID uuid.UUID, name string) (models.Box, bool) {
	var b models.Box
	row := h.pool.QueryRow(c.Request.Context(),
		`SELECT `+boxColumns+` FROM boxes
		  WHERE project_id = $1 AND name = $2 AND status <> 'Deleted'`,
		projectID, name,
	)
	if err := scanBox(row, &b); err == pgx.ErrNoRows {
		respondNotFound(c)
		return models.Box{}, false
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load box")
		return models.Box{}, false
	}
	return b, true
}

// boxWriteGate runs the full 401/404/404-not-403/403 ladder and returns the
// project id on success. Factored out because eight handlers repeat it and a
// divergence in one of them would be an authorization hole.
func (h *Handler) boxWriteGate(c *gin.Context, write bool) (*auth.Claims, uuid.UUID, bool) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return nil, uuid.Nil, false
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return nil, uuid.Nil, false
	}
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		// 404, not 403: anti-enumeration. See the package comment above.
		respondNotFound(c)
		return nil, uuid.Nil, false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return nil, uuid.Nil, false
	}
	if write && !canWrite(role) {
		respondForbidden(c)
		return nil, uuid.Nil, false
	}
	return claims, projectID, true
}

// enqueueBoxOperation inserts one box operation bound to the box's environment.
//
// environment_id is always stamped: it is the identity carrier the whole
// lifecycle hangs off (D1), and an operation without it could not be correlated
// with the resources a box accumulates.
func (h *Handler) enqueueBoxOperation(ctx context.Context, actorID uuid.UUID, b models.Box, action string, payload any) (models.Operation, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return models.Operation{}, err
	}
	var op models.Operation
	row := h.pool.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, $4, $5, $6, 'Created', $7)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		actorID, b.ProjectID, b.EnvironmentID, action, models.ResourceKindBox, b.Name, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		return models.Operation{}, err
	}
	return op, nil
}

// ListBoxes returns all non-deleted boxes in a project.
//
// @ID          listBoxes
// @Summary     List boxes in a project
// @Description Returns every box in a project that has not been deleted, newest first. A box is an ephemeral root sandbox an agent works in; it owns one environment whose runtime is "box". Read-only.
// @Tags        box
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "object with a boxes array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/boxes [get]
func (h *Handler) ListBoxes(c *gin.Context) {
	_, projectID, ok := h.boxWriteGate(c, false)
	if !ok {
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT `+boxColumns+` FROM boxes
		  WHERE project_id = $1 AND status <> 'Deleted'
		  ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query boxes")
		return
	}
	defer rows.Close()

	boxes := []models.Box{}
	for rows.Next() {
		var b models.Box
		if err := scanBox(rows, &b); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan box")
			return
		}
		boxes = append(boxes, b)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading boxes")
		return
	}
	c.JSON(http.StatusOK, gin.H{"boxes": boxes})
}

// GetBox returns one box by name.
//
// @ID          getBox
// @Summary     Get a box by name
// @Description Returns one box's stored record: status, image, size profile, TTL clocks, spend cap and (once the runtime has reported them) its SSH and MCP coordinates. Read-only.
// @Tags        box
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       boxName   path     string true "Box name"
// @Success     200       {object} map[string]interface{} "object with the box"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/boxes/{boxName} [get]
func (h *Handler) GetBox(c *gin.Context) {
	_, projectID, ok := h.boxWriteGate(c, false)
	if !ok {
		return
	}
	b, ok := h.resolveBox(c, projectID, c.Param("boxName"))
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"box": b})
}

// GetBoxState returns the box's live connection coordinates and freshness.
//
// @ID          getBoxState
// @Summary     Get live state and connection coordinates of a box
// @Description Returns the box's phase plus the connection coordinates the runtime reported (SSH host/port, MCP URL), the age of the newest out-of-guest sample, and whether the box has passed its TTL. Read-only. Coordinates are empty until the runtime reports Ready.
// @Tags        box
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       boxName   path     string true "Box name"
// @Success     200       {object} map[string]interface{} "object with status, connection coordinates and sample freshness"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/boxes/{boxName}/state [get]
func (h *Handler) GetBoxState(c *gin.Context) {
	_, projectID, ok := h.boxWriteGate(c, false)
	if !ok {
		return
	}
	b, ok := h.resolveBox(c, projectID, c.Param("boxName"))
	if !ok {
		return
	}

	resp := gin.H{
		"status":         b.Status,
		"ready":          b.Status == models.BoxStatusReady || b.Status == models.BoxStatusIdle,
		"environment_id": b.EnvironmentID,
		"image":          b.Image,
		"profile":        b.Profile,
	}
	if b.ErrorMessage != "" {
		resp["error_message"] = b.ErrorMessage
	}
	// Connection coordinates are relayed verbatim: they are opaque handles owned
	// by the runtime and the control plane never interprets them.
	if b.SSHHost != "" {
		conn := gin.H{"ssh_host": b.SSHHost}
		if b.SSHPort != nil {
			conn["ssh_port"] = *b.SSHPort
		}
		if b.MCPURL != "" {
			conn["mcp_url"] = b.MCPURL
		}
		resp["connect"] = conn
	}
	if b.ExpiresAt != nil {
		resp["expires_at"] = b.ExpiresAt
		resp["expired"] = time.Now().After(*b.ExpiresAt)
	}
	// Sample age, not the sample: the raw sample is platform telemetry from
	// outside the guest and its shape is the runtime's business, not the API's.
	if b.LastSampleAt != nil {
		resp["sample_age_seconds"] = int(time.Since(*b.LastSampleAt).Seconds())
	}
	c.JSON(http.StatusOK, resp)
}

type createBoxRequest struct {
	Name string `json:"name"`
	// Image and Profile name entries in the frozen boxcatalog (not a table): a
	// size only exists if the pool controller pre-warmed sandboxes of that shape.
	Image   string `json:"image"`
	Profile string `json:"profile"`
	Region  string `json:"region"`
	// TTLSeconds is when the box goes to sleep, not when it is destroyed.
	TTLSeconds int `json:"ttl_seconds"`
	// SpendCapRub, when reached, suspends the box. It never deletes it.
	SpendCapRub *float64 `json:"spend_cap_rub"`
	// SSHPublicKey is a PUBLIC key: the caller keeps the private half, so the
	// platform stores no customer credential at all.
	SSHPublicKey string `json:"ssh_public_key"`
}

// CreateBox creates the box row, its owning environment, and enqueues BoxUp.
//
// @ID          createBox
// @Summary     Create a box (ephemeral root sandbox)
// @Description Creates a box: an ephemeral sandbox with root inside it, an image from the frozen box catalog and a size profile. The box owns exactly one environment (runtime "box", type "dev"), which is the identity every later attachment, env var and hostname hangs off — and which crystallization later promotes in place, so nothing has to be migrated. Asynchronous: returns 202 with an operation; poll it, then read the box state for connection coordinates.
// @Tags        box
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string           true "Project UUID"
// @Param       body      body     createBoxRequest true "Box specification (every field optional)"
// @Success     202       {object} map[string]interface{} "object with the created box and the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string "a live box with that name already exists in this project"
// @Router      /projects/{projectId}/boxes [post]
func (h *Handler) CreateBox(c *gin.Context) {
	claims, projectID, ok := h.boxWriteGate(c, true)
	if !ok {
		return
	}

	var req createBoxRequest
	// Every field is optional, so an empty body is legal; only malformed JSON is
	// a 400. Requiring a body would make "give me a box" more than one step, and
	// if the entrance is more than one step we built a VPS with extra steps.
	if err := c.ShouldBindJSON(&req); err != nil && c.Request.ContentLength > 0 {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	b, ok := h.provisionBoxRecord(c, claims.UserID, projectID, req)
	if !ok {
		return
	}

	payload := models.BoxUpPayload{
		BoxID:        b.ID,
		Name:         b.Name,
		Image:        b.Image,
		Profile:      b.Profile,
		Region:       b.Region,
		TTLSeconds:   b.TTLSeconds,
		SpendCapRub:  b.SpendCapRub,
		SSHPublicKey: req.SSHPublicKey,
	}
	op, err := h.enqueueBoxOperation(c.Request.Context(), claims.UserID, b, models.ActionBoxUp, payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	auditMeta, _ := json.Marshal(payload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		claims.UserID, projectID, op.ID, models.ActionBoxUp, models.ResourceKindBox, b.Name, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{"box": b, "operation": op, "message": "box creation queued"})
}

// provisionBoxRecord validates a box request and writes the environment row and
// the box row in ONE transaction, returning the created box.
//
// Extracted from CreateBox so the synchronous single-call door (BoxUp, boxes_up.go)
// creates the object through the identical path. A second copy of this validation
// would be a second place for the (project_id, name) uniqueness and the D1
// one-environment-per-box invariant to drift, and a drift in either is a box with
// an identity that already owns somebody else's resources.
//
// On failure it has already written the response and returns ok=false.
func (h *Handler) provisionBoxRecord(c *gin.Context, actorID uuid.UUID, projectID uuid.UUID, req createBoxRequest) (models.Box, bool) {
	name := req.Name
	if name == "" {
		generated, err := generateBoxName()
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to generate box name")
			return models.Box{}, false
		}
		name = generated
	}
	if err := validateKubeName(name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return models.Box{}, false
	}

	image := req.Image
	if image == "" {
		image = boxcatalog.DefaultImage().Name
	}
	if _, found := boxcatalog.LookupImage(image); !found {
		respondError(c, http.StatusBadRequest,
			fmt.Sprintf("unknown image %q; available: %v", image, boxcatalog.ImageNames()))
		return models.Box{}, false
	}
	profile := req.Profile
	if profile == "" {
		profile = boxcatalog.DefaultSize().Name
	}
	size, found := boxcatalog.LookupSize(profile)
	if !found {
		respondError(c, http.StatusBadRequest,
			fmt.Sprintf("unknown profile %q; available: %v", profile, boxcatalog.SizeNames()))
		return models.Box{}, false
	}

	ttl := req.TTLSeconds
	if ttl == 0 {
		ttl = size.MaxTTLSeconds
	}
	if ttl < 0 || ttl > maxBoxTTLSeconds {
		respondError(c, http.StatusBadRequest,
			fmt.Sprintf("ttl_seconds must be between 1 and %d", maxBoxTTLSeconds))
		return models.Box{}, false
	}
	if req.SpendCapRub != nil && *req.SpendCapRub <= 0 {
		respondError(c, http.StatusBadRequest, "spend_cap_rub must be positive when set")
		return models.Box{}, false
	}

	// The box_minutes quota gate, checked here rather than in each caller so that
	// createBox and boxUp cannot drift apart. It sits after validation on purpose:
	// a caller with a malformed request should be told about the request, not about
	// their plan. A quota lookup that fails for any other reason does not block the
	// box — an unavailable billing read must not become an outage of the product.
	//
	// This gate was briefly lost when the body of createBox was extracted into this
	// helper: the extraction predated the gate, and merging the two took the
	// extraction wholesale. TestCreateBox_BoxMinutesQuotaUsesTheExistingForbiddenShape
	// caught it, which is the whole reason that test asserts on the response shape
	// rather than on an internal call.
	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		if qErr := h.checkQuota(c.Request.Context(), orgID, "box_minutes"); qErr != nil {
			if qe, ok := qErr.(*quotaExceededError); ok {
				respondQuotaExceeded(c, qe.Resource, qe.Limit)
				return models.Box{}, false
			}
		}
	}

	var projectSlug string
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT name FROM projects WHERE id = $1`, projectID,
	).Scan(&projectSlug); err == pgx.ErrNoRows {
		respondNotFound(c)
		return models.Box{}, false
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve project")
		return models.Box{}, false
	}

	// The environment row and the box row are created in ONE transaction. A box
	// without its environment is an identity-less body, and an environment
	// without its box is an orphan that the name-uniqueness index would then
	// block forever — so a half-create must not be observable.
	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to begin transaction")
		return models.Box{}, false
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck

	var envID uuid.UUID
	if err := tx.QueryRow(c.Request.Context(),
		`INSERT INTO environments (project_id, name, namespace, type, runtime)
		 VALUES ($1, $2, $3, 'dev', 'box')
		 ON CONFLICT (project_id, name) DO NOTHING
		 RETURNING id`,
		projectID, name, projectSlug+"-"+name,
	).Scan(&envID); err == pgx.ErrNoRows {
		// The (project_id, name) pair is taken by an existing environment. That is
		// a 409 rather than a reuse: silently adopting someone else's environment
		// would hand the box an identity that already owns other resources.
		respondError(c, http.StatusConflict, "an environment with that name already exists in this project; pick another box name")
		return models.Box{}, false
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create box environment")
		return models.Box{}, false
	}

	var b models.Box
	row := tx.QueryRow(c.Request.Context(),
		`INSERT INTO boxes (project_id, environment_id, name, image, profile, region,
		                    status, ttl_seconds, spend_cap_rub, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, 'Requested', $7, $8, $9)
		 RETURNING `+boxColumns,
		projectID, envID, name, image, profile, req.Region, ttl, req.SpendCapRub, actorID,
	)
	if err := scanBox(row, &b); err != nil {
		// The partial unique index on (project_id, name) WHERE status <> 'Deleted'
		// is what refuses a duplicate live name — the DATABASE refuses it, so two
		// racing replicas cannot both win.
		respondError(c, http.StatusConflict, "a box with that name already exists in this project")
		return models.Box{}, false
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to commit box creation")
		return models.Box{}, false
	}
	return b, true
}

// DeleteBox marks the box Deleting and enqueues DeleteBox.
//
// @ID          deleteBox
// @Summary     Delete a box
// @Description Destructive: destroys the box's sandbox and its disk. Irreversible for anything living only inside the box. Resources attached to the box (managed databases, buckets) live outside it and are NOT deleted. Asynchronous: returns 202 with an operation; poll it until terminal. The box's name becomes reusable once deletion completes.
// @Tags        box
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       boxName   path     string true "Box name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/boxes/{boxName} [delete]
func (h *Handler) DeleteBox(c *gin.Context) {
	claims, projectID, ok := h.boxWriteGate(c, true)
	if !ok {
		return
	}
	b, ok := h.resolveBox(c, projectID, c.Param("boxName"))
	if !ok {
		return
	}
	if b.Status == models.BoxStatusDeleting {
		// Idempotent: a repeated delete is not an error, it is the same intent.
		c.JSON(http.StatusAccepted, gin.H{"message": "box deletion already queued"})
		return
	}

	// Status moves to Deleting BEFORE the operation is enqueued so a box being
	// torn down is never handed out as live by a concurrent read.
	if _, err := h.pool.Exec(c.Request.Context(),
		`UPDATE boxes SET status = 'Deleting', updated_at = now() WHERE id = $1`, b.ID,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to mark box deleting")
		return
	}

	// EVERY LIVE SESSION IS REVOKED BEFORE THE ENQUEUE, and the order is the
	// point. An operation waits in the queue for as long as a worker takes to poll
	// it, so a credential revoked after the enqueue stays usable for exactly that
	// window — a caller could keep working inside a body the customer has been
	// told is gone, and nothing would look wrong because both steps succeeded.
	// A failure here therefore aborts the delete rather than being logged: a
	// teardown that could not withdraw the credential must not proceed.
	revoked, err := h.revokeBoxSessions(c.Request.Context(), b.ID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to revoke box sessions")
		return
	}

	op, err := h.enqueueBoxOperation(c.Request.Context(), claims.UserID, b,
		models.ActionDeleteBox, models.DeleteBoxPayload{BoxID: b.ID, Reason: "user"})
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	auditMeta, _ := json.Marshal(models.DeleteBoxPayload{BoxID: b.ID, Reason: "user"})
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		claims.UserID, projectID, op.ID, models.ActionDeleteBox, models.ResourceKindBox, b.Name, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{
		"operation":        op,
		"sessions_revoked": revoked,
		"message":          "box deletion queued",
	})
}

// SuspendBox enqueues SuspendBox (freeze; billing for compute stops).
//
// @ID          suspendBox
// @Summary     Suspend (sleep) a box
// @Description Freezes the box so compute billing stops while its disk survives. Not destructive and not a delete: resume brings the same box back. This is also what a reached spend cap does, deliberately — a runaway must cost the customer money, never their data. Asynchronous: returns 202 with an operation.
// @Tags        box
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       boxName   path     string true "Box name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string "box is not in a suspendable phase"
// @Router      /projects/{projectId}/boxes/{boxName}/suspend [post]
func (h *Handler) SuspendBox(c *gin.Context) {
	claims, projectID, ok := h.boxWriteGate(c, true)
	if !ok {
		return
	}
	b, ok := h.resolveBox(c, projectID, c.Param("boxName"))
	if !ok {
		return
	}
	if b.Status == models.BoxStatusSleeping {
		c.JSON(http.StatusAccepted, gin.H{"message": "box is already sleeping"})
		return
	}
	if !models.CanTransitionBoxStatus(b.Status, models.BoxStatusSleeping) {
		respondError(c, http.StatusConflict,
			fmt.Sprintf("a box in phase %s cannot be suspended", b.Status))
		return
	}

	// Same ordering rule as DeleteBox, for the same reason and one more: a
	// suspended box is frozen, so a credential that survived the enqueue would open
	// nothing — until the box is resumed, at which point the credential the
	// customer believed was withdrawn works again. Revoking here means resume
	// hands out a fresh one rather than reviving an old one.
	revoked, err := h.revokeBoxSessions(c.Request.Context(), b.ID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to revoke box sessions")
		return
	}

	op, err := h.enqueueBoxOperation(c.Request.Context(), claims.UserID, b,
		models.ActionSuspendBox, models.SuspendBoxPayload{BoxID: b.ID, Reason: "user"})
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"operation":        op,
		"sessions_revoked": revoked,
		"message":          "box suspend queued",
	})
}

type resumeBoxRequest struct {
	// SSHPublicKey lets a resume rebind a fresh key, so the caller never has to
	// keep one alive across a sleep. Public half only.
	SSHPublicKey string `json:"ssh_public_key"`
}

// ResumeBox enqueues ResumeBox (thaw a sleeping box).
//
// @ID          resumeBox
// @Summary     Resume (wake) a sleeping box
// @Description Thaws a sleeping box and waits for its exec channel to accept again. The same box, the same disk, the same injected credentials. Optionally rebinds a fresh SSH public key so the caller need not keep one alive across the sleep. Asynchronous: returns 202 with an operation.
// @Tags        box
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string           true  "Project UUID"
// @Param       boxName   path     string           true  "Box name"
// @Param       body      body     resumeBoxRequest false "Optional fresh SSH public key"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string "box is not sleeping"
// @Router      /projects/{projectId}/boxes/{boxName}/resume [post]
func (h *Handler) ResumeBox(c *gin.Context) {
	claims, projectID, ok := h.boxWriteGate(c, true)
	if !ok {
		return
	}
	b, ok := h.resolveBox(c, projectID, c.Param("boxName"))
	if !ok {
		return
	}
	var req resumeBoxRequest
	if err := c.ShouldBindJSON(&req); err != nil && c.Request.ContentLength > 0 {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if b.Status == models.BoxStatusReady || b.Status == models.BoxStatusIdle {
		c.JSON(http.StatusAccepted, gin.H{"message": "box is already awake"})
		return
	}
	if b.Status != models.BoxStatusSleeping {
		respondError(c, http.StatusConflict,
			fmt.Sprintf("a box in phase %s cannot be resumed", b.Status))
		return
	}

	op, err := h.enqueueBoxOperation(c.Request.Context(), claims.UserID, b,
		models.ActionResumeBox, models.ResumeBoxPayload{BoxID: b.ID, SSHPublicKey: req.SSHPublicKey})
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "box resume queued"})
}

type extendBoxRequest struct {
	// TTLSeconds is the new lifetime measured from now, capped at maxBoxTTLSeconds.
	TTLSeconds int `json:"ttl_seconds"`
}

// ExtendBox pushes the box's sleep deadline out.
//
// @ID          extendBox
// @Summary     Extend a box's TTL
// @Description Pushes out when the box goes to sleep, measured from now and capped at 24 hours. Synchronous — the only box mutation that is, because it moves a timestamp in our own row and touches no runtime. Reaching the TTL puts a box to sleep, it never destroys it, so extending is a convenience rather than a rescue.
// @Tags        box
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string           true "Project UUID"
// @Param       boxName   path     string           true "Box name"
// @Param       body      body     extendBoxRequest true "New TTL in seconds, from now"
// @Success     200       {object} map[string]interface{} "object with the updated box"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string "box phase cannot be extended"
// @Router      /projects/{projectId}/boxes/{boxName}/extend [post]
func (h *Handler) ExtendBox(c *gin.Context) {
	_, projectID, ok := h.boxWriteGate(c, true)
	if !ok {
		return
	}
	b, ok := h.resolveBox(c, projectID, c.Param("boxName"))
	if !ok {
		return
	}
	var req extendBoxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.TTLSeconds <= 0 || req.TTLSeconds > maxBoxTTLSeconds {
		respondError(c, http.StatusBadRequest,
			fmt.Sprintf("ttl_seconds must be between 1 and %d", maxBoxTTLSeconds))
		return
	}
	if !models.BoxIsLive(b.Status) {
		respondError(c, http.StatusConflict,
			fmt.Sprintf("a box in phase %s cannot be extended", b.Status))
		return
	}

	var updated models.Box
	row := h.pool.QueryRow(c.Request.Context(),
		// $2 is cast explicitly: used once as an integer column value and once as
		// the left operand of an interval multiplication, an uncast parameter makes
		// Postgres deduce two different types for it and fail with 42P08.
		`UPDATE boxes
		    SET ttl_seconds = $2::int,
		        expires_at  = now() + ($2::int * INTERVAL '1 second'),
		        updated_at  = now()
		  WHERE id = $1
		 RETURNING `+boxColumns,
		b.ID, req.TTLSeconds,
	)
	if err := scanBox(row, &updated); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to extend box")
		return
	}
	c.JSON(http.StatusOK, gin.H{"box": updated, "message": "box TTL extended"})
}
