package api

import (
	"net/http"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
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

// @ID          submitFeedback
// @Summary     Submit in-product feedback
// @Description Records a short feedback message with the console route it was sent from. Works with or without a bearer token; if present, the caller's identity is attached.
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
	if claims, ok := h.optionalClaims(c); ok {
		sub := claims.UserID.String()
		userSub = &sub
		if org := feedbackOrgID(claims); org != "" {
			orgID = &org
		}
	}

	if _, err := h.pool.Exec(c.Request.Context(),
		`INSERT INTO feedback (user_sub, org_id, route, message) VALUES ($1, $2, $3, $4)`,
		userSub, orgID, req.Route, message,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record feedback")
		return
	}

	c.JSON(http.StatusCreated, gin.H{"status": "ok"})
}
