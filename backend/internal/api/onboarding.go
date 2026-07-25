package api

import (
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
)

// onboardingKeys is the whitelist of known onboarding campaign keys. It MUST
// stay in sync with the frontend campaign registry in
// frontend/lib/onboarding/campaigns.ts. Adding a campaign = add its key here.
var onboardingKeys = map[string]bool{
	"agent": true,
}

// onboardingStatuses is the set of accepted progress states.
var onboardingStatuses = map[string]bool{
	"seen":    true,
	"skipped": true,
	"done":    true,
}

type reportOnboardingRequest struct {
	Status string `json:"status"`
	Step   int    `json:"step"`
}

// @ID          getOnboarding
// @Summary     Get the caller's onboarding status map
// @Description Returns a map of onboarding_key to status ("seen"|"skipped"|"done") for the authenticated user. Empty object if none.
// @Tags        onboarding
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]string
// @Router      /onboarding [get]
func (h *Handler) GetOnboarding(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT onboarding_key, status FROM user_onboarding WHERE user_sub = $1`,
		claims.UserID.String())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read onboarding")
		return
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, status string
		if err := rows.Scan(&key, &status); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan onboarding")
			return
		}
		out[key] = status
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read onboarding")
		return
	}
	c.JSON(http.StatusOK, out)
}

// @ID          reportOnboarding
// @Summary     Report onboarding progress for a campaign
// @Description Upserts the caller's status for one onboarding key. Monotonic: a "done" or "skipped" state is never downgraded.
// @Tags        onboarding
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       key  path     string                  true "Onboarding campaign key"
// @Param       body body     reportOnboardingRequest true "Progress"
// @Success     200  {object} map[string]string
// @Failure     400  {object} map[string]string
// @Router      /onboarding/{key} [post]
func (h *Handler) PostOnboarding(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	key := c.Param("key")
	if !onboardingKeys[key] {
		respondError(c, http.StatusBadRequest, "unknown onboarding key")
		return
	}
	var req reportOnboardingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if !onboardingStatuses[req.Status] {
		respondError(c, http.StatusBadRequest, "invalid status")
		return
	}
	if _, err := h.pool.Exec(c.Request.Context(),
		`INSERT INTO user_onboarding (user_sub, onboarding_key, status, step_reached)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_sub, onboarding_key) DO UPDATE
		 SET status       = EXCLUDED.status,
		     step_reached = GREATEST(user_onboarding.step_reached, EXCLUDED.step_reached),
		     updated_at   = NOW()
		 WHERE user_onboarding.status NOT IN ('done', 'skipped')`,
		claims.UserID.String(), key, req.Status, req.Step,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record onboarding")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
