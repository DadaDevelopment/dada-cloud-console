package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// AI routing modes. A project is either running on keys it brought itself
// (byok) or on the platform's provider keys, billed per routed call (platform).
const (
	aiRoutingModeBYOK     = "byok"
	aiRoutingModePlatform = "platform"
)

// aiKeyOwner values recorded on a ledger row: whose provider key actually paid
// the upstream bill for that call. "unknown" is what rows written before the
// distinction existed carry, and must never be counted as revenue.
const (
	aiKeyOwnerBYOK     = "byok"
	aiKeyOwnerPlatform = "platform"
	aiKeyOwnerUnknown  = "unknown"
)

type aiRoutingResponse struct {
	Mode      string  `json:"mode"`
	Markup    float64 `json:"markup"`
	UpdatedBy string  `json:"updated_by,omitempty"`
}

type setAIRoutingRequest struct {
	Mode string `json:"mode" binding:"required"`
}

// aiRoutingMode reads a project's routing mode. A project with no row is on
// byok, which is the behaviour every project had before this setting existed:
// its own key when it has one, the free platform fallback when it does not,
// billed nothing either way.
func (h *Handler) aiRoutingMode(ctx context.Context, projectID uuid.UUID) string {
	var mode string
	err := h.pool.QueryRow(ctx,
		`SELECT mode FROM ai_routing_settings WHERE project_id = $1`, projectID,
	).Scan(&mode)
	if err != nil || mode == "" {
		return aiRoutingModeBYOK
	}
	return mode
}

// GetAIRoutingMode returns which of the two key modes a project is on, together
// with the routing markup it would be billed at on the platform key.
//
// @ID          getAIRoutingMode
// @Summary     A project's AI routing mode
// @Description Returns whether the project routes on its own provider keys (byok) or on the platform's, and the markup applied to platform-routed calls.
// @Tags        ai-gateway
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} aiRoutingResponse
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/ai/routing [get]
func (h *Handler) GetAIRoutingMode(c *gin.Context) {
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
	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	c.JSON(http.StatusOK, aiRoutingResponse{
		Mode:   h.aiRoutingMode(c.Request.Context(), projectID),
		Markup: h.cfg.PricingMarkup,
	})
}

// SetAIRoutingMode switches a project between its own provider keys and the
// platform's. Switching to platform is the moment a customer agrees to be
// billed for routing, so both the switch and every refusal of it are audited:
// "who turned the meter on, and when" has to be answerable from the audit trail
// alone when an invoice is disputed.
//
// @ID          setAIRoutingMode
// @Summary     Switch a project's AI routing mode
// @Description Sets the project to route on its own provider keys (byok) or on the platform's keys, billed per call at the routing markup.
// @Tags        ai-gateway
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string              true "Project UUID"
// @Param       body      body     setAIRoutingRequest true "Routing mode"
// @Success     200       {object} aiRoutingResponse
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/ai/routing [put]
func (h *Handler) SetAIRoutingMode(c *gin.Context) {
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

	audit := func(outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			Action:       auditActionSetAIRouting,
			ResourceKind: "AIGateway",
			ResourceName: "routing",
			Outcome:      outcome,
			Metadata:     meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		respondError(c, status, msg)
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		rejectErr(http.StatusNotFound, "not_a_member", "not found")
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "membership_check_failed", "failed to check project membership")
		return
	}
	if !canWrite(role) {
		rejectErr(http.StatusForbidden, "read_only_role", "forbidden")
		return
	}

	var req setAIRoutingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rejectErr(http.StatusBadRequest, "malformed_body", "invalid request body")
		return
	}
	if req.Mode != aiRoutingModeBYOK && req.Mode != aiRoutingModePlatform {
		rejectErr(http.StatusBadRequest, "unknown_mode", "mode must be byok or platform")
		return
	}

	if _, err := h.pool.Exec(c.Request.Context(), `
		INSERT INTO ai_routing_settings (project_id, mode, updated_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (project_id) DO UPDATE
		   SET mode       = EXCLUDED.mode,
		       updated_by = EXCLUDED.updated_by,
		       updated_at = NOW()
	`, projectID, req.Mode, claims.UserID.String()); err != nil {
		rejectErr(http.StatusInternalServerError, "store_failed", "store routing mode: "+err.Error())
		return
	}

	audit(auditOutcomeSuccess, map[string]any{"mode": req.Mode, "markup": h.cfg.PricingMarkup})

	c.JSON(http.StatusOK, aiRoutingResponse{Mode: req.Mode, Markup: h.cfg.PricingMarkup})
}
