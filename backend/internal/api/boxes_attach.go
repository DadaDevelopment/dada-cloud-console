package api

import (
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// attach: managed resources for a running box.
//
// SYNCHRONOUS, and the divergence from the operations convention is stated rather
// than hidden. models.AttachBoxDatabasePayload already describes the production
// path: AttachBoxDatabase enqueues a CHILD CreateServiceDatabase against the box's
// environment_id, waits for it to reach Committed, resolves the Crossplane
// connection secret through cloudtask.DBCredentialsResolver, and only then injects
// env — the same parent/child idiom as doImportComposeStack -> DeployStack.
//
// That path needs Crossplane and a Kubernetes cluster. Neither exists in the
// environment this adapter runs in, so this handler drives the AttachProvider seam
// directly against a real managed Postgres cluster. Both routes end at the same
// place — a credential the box can open — and the seam is what keeps them one
// design rather than two.
//
// What is NOT compromised: the resource lives OUTSIDE the box, the credential
// reaches the box only through its 0600 env file, and nothing secret is written to
// box_attachments. Those are the properties that make the attach safe, and none of
// them depends on whether the provisioning was synchronous.

type attachBoxDatabaseRequest struct {
	// Name labels the attachment inside the box's namespace of attachments.
	Name string `json:"name"`
	// EnvPrefix lets a box hold two databases without their env keys colliding.
	EnvPrefix string `json:"env_prefix"`
}

// AttachBoxDatabase provisions a managed Postgres database outside the box and
// injects its credential into the box.
//
// @ID          attachBoxDatabase
// @Summary     Attach a managed Postgres database to a running box
// @Description Provisions a managed PostgreSQL database and role OUTSIDE the box and injects the connection credential into the box's env file (mode 0600, root). The box can then open it immediately — the very next command inside the box sees DATABASE_URL. The database is deliberately not inside the box: a disposable body must not own the customer's data, so deleting or crystallizing the box never destroys it. The response lists WHICH env keys were injected and never their values.
// @Tags        box
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                   true  "Project UUID"
// @Param       boxName   path     string                   true  "Box name"
// @Param       body      body     attachBoxDatabaseRequest false "Attachment name and optional env prefix"
// @Success     200       {object} map[string]interface{} "object with the attachment and the injected env key names"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string "the box is not in a phase that can accept an attachment"
// @Failure     503       {object} map[string]string "box runtime or managed Postgres not configured"
// @Router      /projects/{projectId}/boxes/{boxName}/attach/database [post]
func (h *Handler) AttachBoxDatabase(c *gin.Context) {
	claims, projectID, ok := h.boxWriteGate(c, true)
	if !ok {
		return
	}
	stack, ok := h.requireBoxRuntime(c)
	if !ok {
		return
	}
	b, ok := h.resolveBox(c, projectID, c.Param("boxName"))
	if !ok {
		return
	}
	var req attachBoxDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil && c.Request.ContentLength > 0 {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		req.Name = "db"
	}
	if err := validateKubeName(req.Name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	// Attaching mid-flight is the product; attaching to a body that is not running
	// is a 409 rather than a queued intent, because the injection has to land in a
	// filesystem that exists.
	if b.Status != models.BoxStatusReady && b.Status != models.BoxStatusIdle {
		respondError(c, http.StatusConflict,
			"a box in phase "+string(b.Status)+" cannot accept an attachment; it must be Ready or Idle")
		return
	}
	if b.InstanceRef == "" {
		respondError(c, http.StatusConflict, "the box has no runtime instance yet")
		return
	}

	var attachmentID uuid.UUID
	if err := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO box_attachments (box_id, environment_id, kind, name, env_prefix, status, created_by)
		 VALUES ($1, $2, 'postgres', $3, $4, 'Attaching', $5)
		 ON CONFLICT (box_id, name) WHERE detached_at IS NULL
		 DO UPDATE SET status = 'Attaching', env_prefix = EXCLUDED.env_prefix, error_message = ''
		 RETURNING id`,
		b.ID, b.EnvironmentID, req.Name, req.EnvPrefix, claims.UserID,
	).Scan(&attachmentID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record the attachment")
		return
	}

	inst := instanceFor(b.ID.String(), b.InstanceRef, b.NodeRef, b.Image, b.Region, b.SSHHost, b.SSHPort, b.MCPURL)
	// Measured to a USABLE CREDENTIAL, not to this handler's response: what matters
	// to an agent mid-flight is when it can open the connection.
	started := time.Now()
	injected, resourceName, err := stack.attach.AttachPostgresNamed(c.Request.Context(), inst, req.Name, req.EnvPrefix)
	if err != nil {
		metrics.RecordBoxAttach("postgres", "failed", time.Since(started))
		_, _ = h.pool.Exec(c.Request.Context(),
			`UPDATE box_attachments SET status = 'Failed', error_message = $2 WHERE id = $1`,
			attachmentID, err.Error())
		status := http.StatusInternalServerError
		if !stack.attach.ManagedPostgresConfigured() {
			status = http.StatusServiceUnavailable
		}
		respondError(c, status, "failed to attach database: "+err.Error())
		return
	}
	elapsed := time.Since(started)
	metrics.RecordBoxAttach("postgres", "success", elapsed)

	if _, err := h.pool.Exec(c.Request.Context(),
		`UPDATE box_attachments
		    SET status = 'Attached', injected_keys = $2, resource_name = $3, error_message = ''
		  WHERE id = $1`,
		attachmentID, injected, resourceName,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record the injected keys")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"attachment": gin.H{
			"id":            attachmentID,
			"kind":          "postgres",
			"name":          req.Name,
			"resource_name": resourceName,
			"env_prefix":    req.EnvPrefix,
			"status":        "Attached",
		},
		// Key NAMES only. A response that echoed the DSN would put the credential
		// into every log and proxy between here and the caller.
		"injected_env_keys": injected,
		"attach_ms":         elapsed.Milliseconds(),
		"note": "the database lives OUTSIDE the box; deleting or crystallizing the box does not destroy it. " +
			"Values are only in the box's env file (0600, root) and are never returned or stored here.",
	})
}

// ListBoxAttachments returns the resources attached to a box.
//
// @ID          listBoxAttachments
// @Summary     List the resources attached to a box
// @Description Returns the box's attachments with the env key NAMES each one injected. Never any values: the credentials exist only inside the box's 0600 env file, which is also the only thing crystallization carries. Read-only.
// @Tags        box
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       boxName   path     string true "Box name"
// @Success     200       {object} map[string]interface{} "object with an attachments array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/boxes/{boxName}/attachments [get]
func (h *Handler) ListBoxAttachments(c *gin.Context) {
	_, projectID, ok := h.boxWriteGate(c, false)
	if !ok {
		return
	}
	b, ok := h.resolveBox(c, projectID, c.Param("boxName"))
	if !ok {
		return
	}
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, kind, name, resource_name, env_prefix, injected_keys, status, error_message, created_at, detached_at
		   FROM box_attachments WHERE box_id = $1 ORDER BY created_at DESC`, b.ID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query attachments")
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var (
			id                                  uuid.UUID
			kind, name, resourceName, envPrefix string
			keys                                []string
			status, errMsg                      string
			createdAt                           time.Time
			detachedAt                          *time.Time
		)
		if err := rows.Scan(&id, &kind, &name, &resourceName, &envPrefix, &keys, &status, &errMsg, &createdAt, &detachedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan attachment")
			return
		}
		item := gin.H{
			"id": id, "kind": kind, "name": name, "resource_name": resourceName,
			"env_prefix": envPrefix, "injected_env_keys": keys, "status": status,
			"created_at": createdAt,
		}
		if errMsg != "" {
			item["error_message"] = errMsg
		}
		if detachedAt != nil {
			item["detached_at"] = detachedAt
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading attachments")
		return
	}
	c.JSON(http.StatusOK, gin.H{"attachments": out})
}
