package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/dada-tuda/console/backend/internal/boxcatalog"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The single-call door: `box up`.
//
// THIS HANDLER DOES NOT GO THROUGH operations, AND THAT IS THE ONE DELIBERATE
// EXCEPTION IN THE WHOLE FEATURE. A worker poll is 5 seconds
// (VM_POLL_INTERVAL_DB); the entire time-to-ready budget is 10
// (metrics.BoxReadyBudget). Routing a claim through the queue would spend the
// budget before any work started, so the claim is synchronous and the async
// operations path is kept for everything that is genuinely minutes-scale.
//
// It is also one call rather than two on purpose. If the entrance to the product
// is "create, then poll, then read coordinates", we have built a VPS with extra
// steps. So one POST returns a ready body, its coordinates, the one-time
// credential and the MEASURED time it took.
//
// The number in the response comes from box.PhaseTimeline, whose design forbids a
// caller-supplied timestamp: the only way to close a phase is to ask the
// orchestrator's clock at the moment it happens. Nothing in this file can put a
// guest-reported instant into it, and that is by construction rather than by care.

type boxUpRequest struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	Profile string `json:"profile"`
	Region  string `json:"region"`
	// TTLSeconds is when the box goes to SLEEP, not when it is destroyed.
	TTLSeconds  int      `json:"ttl_seconds"`
	SpendCapRub *float64 `json:"spend_cap_rub"`
	// SSHPublicKey is a PUBLIC key: the caller keeps the private half, so the
	// platform stores no customer credential at all.
	SSHPublicKey string `json:"ssh_public_key"`
	// SessionTTLHours defaults to 12 and is capped at 168.
	SessionTTLHours int `json:"session_ttl_hours"`
	// WaitSeconds bounds how long the caller is willing to wait, in [0,120]. It is
	// a bound and not a hint: exceeding it returns a classified failure rather than
	// a body that arrives after the caller gave up.
	WaitSeconds int `json:"wait_seconds"`
}

// BoxUp claims a warm box, proves it is ready, and returns everything needed to
// work in it.
//
// @ID          boxUp
// @Summary     Bring up a box in one call (synchronous)
// @Description The single-call door to a box. Creates the box and its owning environment, claims a pre-warmed body, binds the caller's identity to it, and returns only once a command has actually executed inside it and returned success — not when the API answered and not when a port accepted. The response carries the connection coordinates, a one-time "dadabox_" session token (shown exactly once, never retrievable again), a ready-to-paste mcpServers snippet pointing at the BOX's own endpoint, and the measured time to ready broken down by phase. Synchronous rather than 202-with-an-operation because a worker poll is longer than the entire time-to-ready budget.
// @Tags        box
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string       true  "Project UUID"
// @Param       body      body     boxUpRequest false "Box specification (every field optional)"
// @Success     200       {object} map[string]interface{} "object with the ready box, its connection coordinates, the one-time token and the measured time to ready"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string "a live box with that name already exists in this project"
// @Failure     503       {object} map[string]string "no warm box available, or the box runtime is not configured"
// @Router      /projects/{projectId}/box-up [post]
func (h *Handler) BoxUp(c *gin.Context) {
	claims, projectID, ok := h.boxWriteGate(c, true)
	if !ok {
		return
	}
	var req boxUpRequest
	resourceName := ""
	var envID uuid.UUID

	// Every rejection is recorded before the error is written, because the
	// single-call door is the one place where a refusal IS the product
	// experience: "no warm body available" and "never asked for one" have to be
	// distinguishable in the trail.
	reject := func(status int, reason string, extra map[string]any) {
		meta := map[string]any{"reason": reason, "status": status}
		for k, v := range extra {
			meta[k] = v
		}
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        models.ActionBoxUp,
			ResourceKind:  models.ResourceKindBox,
			ResourceName:  resourceName,
			Outcome:       auditOutcomeFailure,
			Metadata:      meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		reject(status, reason, nil)
		respondError(c, status, msg)
	}

	stack, ok := h.requireBoxRuntime(c)
	if !ok {
		reject(c.Writer.Status(), "box_runtime_unavailable", nil)
		return
	}
	if err := c.ShouldBindJSON(&req); err != nil && c.Request.ContentLength > 0 {
		rejectErr(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	resourceName = req.Name
	wait := req.WaitSeconds
	if wait <= 0 {
		wait = 120
	}
	if wait > 120 {
		rejectErr(http.StatusBadRequest, "invalid_wait_seconds", "wait_seconds must be between 0 and 120")
		return
	}

	b, ok := h.provisionBoxRecord(c, claims.UserID, projectID, createBoxRequest{
		Name:         req.Name,
		Image:        req.Image,
		Profile:      req.Profile,
		Region:       req.Region,
		TTLSeconds:   req.TTLSeconds,
		SpendCapRub:  req.SpendCapRub,
		SSHPublicKey: req.SSHPublicKey,
	})
	if !ok {
		reject(c.Writer.Status(), "box_not_provisioned", nil)
		return
	}
	resourceName = b.Name
	envID = b.EnvironmentID

	// The credential is minted BEFORE the body is claimed so its hash is on record
	// before anything can hand the body out. The plaintext exists only in this
	// response.
	sessionToken, sessionPrefix, sessionExpiry, err := h.mintBoxSession(
		c.Request.Context(), b.ID, projectID, &claims.UserID, boxSessionTTL(req.SessionTTLHours))
	if err != nil {
		h.failBox(c.Request.Context(), b.ID, "failed to mint box session")
		rejectErr(http.StatusInternalServerError, "session_mint_failed", "failed to mint box session")
		return
	}

	if _, err := h.pool.Exec(c.Request.Context(),
		`UPDATE boxes SET status = 'Booting', updated_at = now() WHERE id = $1`, b.ID); err != nil {
		rejectErr(http.StatusInternalServerError, "mark_booting_failed", "failed to mark box booting")
		return
	}

	// The ready path. Its context is the caller's bound, so a spawn that outruns
	// wait_seconds ends as a classified timeout rather than as a body nobody is
	// waiting for.
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(wait)*time.Second)
	defer cancel()

	res, mcpURL, sshHost, spawnErr := h.bootBoxInstance(ctx, stack, projectID, b, req.SSHPublicKey)
	if spawnErr != nil {
		h.failBox(context.Background(), b.ID, spawnErr.Error())
		status := http.StatusServiceUnavailable
		if res != nil && res.PoolHit {
			status = http.StatusInternalServerError
		}
		reject(status, "box_not_ready", map[string]any{
			"error": spawnErr.Error(),
			"pool":  poolLabelFor(res != nil && res.PoolHit),
		})
		respondError(c, status, "box did not become ready: "+spawnErr.Error())
		return
	}
	inst := res.Instance

	updated, err := h.markBoxReady(c.Request.Context(), b.ID, inst, mcpURL, sshHost)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "record_ready_failed", "failed to record the ready box")
		return
	}

	phases := map[string]int64{}
	for phase, d := range res.Timeline.Durations() {
		phases[phase] = d.Milliseconds()
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        models.ActionBoxUp,
		ResourceKind:  models.ResourceKindBox,
		ResourceName:  b.Name,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"box_id": b.ID, "instance_ref": inst.InstanceRef,
			"pool": poolLabelFor(res.PoolHit), "time_to_ready_ms": res.Timeline.Total().Milliseconds(),
			"session_token_prefix": sessionPrefix,
		},
	})

	c.JSON(http.StatusOK, gin.H{
		"box":     updated,
		"connect": h.boxConnectBlock(stack, updated, sessionToken),
		// Shown exactly ONCE. Only the sha256 and a 6-hex prefix are stored, so
		// there is nothing to reveal later and no scrub step that can be forgotten.
		"session": gin.H{
			"token":        sessionToken,
			"token_prefix": sessionPrefix,
			"expires_at":   sessionExpiry,
			"note":         "shown exactly once; only its sha256 and prefix are stored",
		},
		"ready": gin.H{
			"time_to_ready_ms": res.Timeline.Total().Milliseconds(),
			"phase_ms":         phases,
			"pool":             poolLabelFor(res.PoolHit),
			"budget_ms":        int64(10000),
			"measured_by":      "box.PhaseTimeline (orchestrator clock only; a guest-reported instant cannot enter it)",
			"canary":           box.CanaryCommand,
			"critical_path":    res.Steps,
			"guest_clock_skew": "recorded, never measured with",
		},
	})
}

// poolLabelFor renders the pool label the metrics use.
func poolLabelFor(hit bool) string {
	if hit {
		return "hit"
	}
	return "miss"
}

// failBox records a terminal failure the customer must see.
func (h *Handler) failBox(ctx context.Context, boxID any, msg string) {
	_, _ = h.pool.Exec(ctx,
		`UPDATE boxes SET status = 'Failed', error_message = $2, updated_at = now() WHERE id = $1`,
		boxID, msg)
}

// boxConnectBlock renders the connection coordinates plus the mcpServers snippet.
//
// The snippet names the BOX's endpoint, never ours, and it is labelled with
// whether that endpoint is live. A snippet that pointed at a control-plane exec
// tool would route every keystroke of the customer's work through us, which is the
// opposite of the promise the product is sold on — so no such tool exists here,
// and the keep-list being an allowlist is what keeps it that way.
func (h *Handler) boxConnectBlock(stack *boxRuntimeStack, b models.Box, token string) gin.H {
	mcpURL := b.MCPURL
	if mcpURL == "" {
		mcpURL = fmt.Sprintf("%s/api/v1/box/session/mcp", stack.sessions)
	}
	own := boxOwnsItsMCPURL(mcpURL)
	reason := "served by cmd/box-broker INSIDE the box: a command run through this endpoint does not pass through the Dada control plane. " +
		"On LocalRuntime the box shares the host's network namespace, so the address is loopback on the box host rather than the box's own hostname; " +
		"in production it is the box's Pod address (ADR-019)."
	if !own {
		reason = "this box has no endpoint of its own — BOX_BROKER_DIR is unset on this host, or its broker did not come up. " +
			"The URL above is the control-plane fallback (internal/api/box_session.go), which means commands DO pass through us. " +
			"That is a degraded box, not the product."
	}
	return gin.H{
		"ssh_host":    b.SSHHost,
		"ssh_command": boxSSHCommand(b.SSHHost, b.SSHPort),
		"mcp": gin.H{
			"url":       mcpURL,
			"available": own,
			"reason":    reason,
			"snippet":   mcpServersSnippet(b.Name, mcpURL, token),
		},
		"session_endpoint": stack.sessions + "/api/v1/box/session/exec",
		"session_auth":     "X-Dada-Box-Token: <the one-time dadabox_ token>",
		"note": "the session endpoint is the box's own door. It carries no swaggo annotation and is registered " +
			"only when the box runtime is configured, so it never reaches swagger.json and therefore can never " +
			"become a reflected MCP tool on our control-plane surface.",
	}
}

// GetBoxConnection returns a box's coordinates and, on request, a fresh session.
//
// @ID          getBoxConnection
// @Summary     Get a box's connection coordinates
// @Description Returns the box's SSH and MCP coordinates plus the ready-to-paste mcpServers snippet. Pass new_session=true to mint a FRESH one-time "dadabox_" token; the previous one is not revealed because only its sha256 is stored. Minting a new session does not revoke the old ones — use suspend or delete for that, both of which revoke every live session before they enqueue anything.
// @Tags        box
// @Produce     json
// @Security    BearerAuth
// @Param       projectId   path     string true  "Project UUID"
// @Param       boxName     path     string true  "Box name"
// @Param       new_session query    bool   false "Mint a fresh one-time session token"
// @Success     200         {object} map[string]interface{} "object with connection coordinates"
// @Failure     401         {object} map[string]string
// @Failure     404         {object} map[string]string
// @Failure     503         {object} map[string]string "box runtime not configured"
// @Router      /projects/{projectId}/boxes/{boxName}/connection [get]
func (h *Handler) GetBoxConnection(c *gin.Context) {
	claims, projectID, ok := h.boxWriteGate(c, false)
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
	resp := gin.H{"box": gin.H{"name": b.Name, "status": b.Status}}
	token := ""
	if c.Query("new_session") == "true" {
		plaintext, prefix, expiry, err := h.mintBoxSession(
			c.Request.Context(), b.ID, projectID, &claims.UserID, boxSessionTTL(0))
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to mint box session")
			return
		}
		token = plaintext
		// The new digest has to reach the box, or the token this response hands out
		// opens the control-plane fallback and nothing else — a credential that works
		// on the wrong door is worse than one that works on none, because the
		// customer's work would silently start flowing through us.
		inst := instanceFor(b.ID.String(), b.InstanceRef, b.NodeRef, b.Image, b.Region, b.SSHHost, b.SSHPort, b.MCPURL)
		if err := h.syncBoxDoor(c.Request.Context(), stack, inst, b.ID); err != nil {
			respondError(c, http.StatusInternalServerError,
				"the session was minted but could not be installed in the box: "+err.Error())
			return
		}
		resp["session"] = gin.H{
			"token": plaintext, "token_prefix": prefix, "expires_at": expiry,
			"note": "shown exactly once; only its sha256 and prefix are stored",
		}
	}
	resp["connect"] = h.boxConnectBlock(stack, b, token)
	c.JSON(http.StatusOK, resp)
}

// GetBoxCatalog returns the frozen warm-image and size catalog.
//
// @ID          getBoxCatalog
// @Summary     List the box images and size profiles
// @Description Returns the frozen catalog of warm box images and size profiles. Deliberately not a table: a size is only real if the pool controller has pre-warmed bodies of that shape, and a warm image is only real if it has been pulled and digest-pinned, so adding an entry is a code change plus a deploy — the same event that rolls the pool config and the image pin. Read-only.
// @Tags        box
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{} "object with images and sizes"
// @Failure     401 {object} map[string]string
// @Router      /box/catalog [get]
func (h *Handler) GetBoxCatalog(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	images := make([]gin.H, 0, len(boxcatalog.V1Images))
	for _, img := range boxcatalog.V1Images {
		images = append(images, gin.H{
			"name": img.Name, "ref": img.Ref, "digest": img.Digest,
			"description": img.Description, "toolchain": img.Toolchain, "default": img.Default,
		})
	}
	sizes := make([]gin.H, 0, len(boxcatalog.V1Sizes))
	for _, s := range boxcatalog.V1Sizes {
		sizes = append(sizes, gin.H{
			"name": s.Name, "vcpu": s.VCPU, "memory_mb": s.MemoryMB, "disk_gb": s.DiskGB,
			"max_ttl_seconds": s.MaxTTLSeconds, "default": s.Default,
		})
	}
	resp := gin.H{"images": images, "sizes": sizes, "os_slug": box.WarmImageOSSlug}
	if h.boxStack != nil {
		resp["pool"] = gin.H{
			"image":     h.boxStack.image,
			"region":    h.boxStack.region,
			"available": h.boxStack.pool.Available(h.boxStack.image, h.boxStack.region),
			"target":    h.boxStack.pool.Target(h.boxStack.image, h.boxStack.region),
		}
	}
	c.JSON(http.StatusOK, resp)
}
