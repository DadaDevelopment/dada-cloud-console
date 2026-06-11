package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetProjectQuotas returns quota limits + current usage + advisory monthly counter.
// Reads-only; safe for any project member.
//
// @ID          getProjectQuotas
// @Summary     Get a project's quotas and usage
// @Description Returns the project's CPU/GPU model quota limits, the number of models currently in use, and the advisory monthly inference-call counter. Read-only.
// @Tags        quota
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{} "object with quota limits and current usage"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/quotas [get]
func (h *Handler) GetProjectQuotas(c *gin.Context) {
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
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}

	q, err := h.loadQuotas(c.Request.Context(), projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load quotas")
		return
	}
	cpuInUse, gpuInUse, err := h.countAIModelsByKind(c.Request.Context(), projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to count models")
		return
	}
	monthCalls, err := h.sumInferenceCalls(c.Request.Context(), projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to sum inference calls")
		return
	}
	c.JSON(http.StatusOK, models.QuotaUsage{
		Quotas:              q,
		CPUModelsInUse:      cpuInUse,
		GPUModelsInUse:      gpuInUse,
		InferenceCallsMonth: monthCalls,
	})
}

func (h *Handler) loadQuotas(ctx context.Context, projectID uuid.UUID) (models.ProjectQuotas, error) {
	var q models.ProjectQuotas
	q.ProjectID = projectID
	err := h.pool.QueryRow(ctx,
		`SELECT cpu_model_max, gpu_model_max, monthly_inference_calls, updated_at
		 FROM project_quotas WHERE project_id = $1`,
		projectID,
	).Scan(&q.CPUModelMax, &q.GPUModelMax, &q.MonthlyInferenceCalls, &q.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Defaults mirror the migration.
		q.CPUModelMax = 5
		q.GPUModelMax = 0
		q.MonthlyInferenceCalls = 100000
		return q, nil
	}
	return q, err
}

func (h *Handler) sumInferenceCalls(ctx context.Context, projectID uuid.UUID) (int64, error) {
	var sum *int64
	err := h.pool.QueryRow(ctx,
		`SELECT SUM(call_count) FROM aimodel_inference_counters
		 WHERE project_id = $1 AND year_month = TO_CHAR(NOW(), 'YYYY-MM')`,
		projectID,
	).Scan(&sum)
	if err != nil {
		return 0, err
	}
	if sum == nil {
		return 0, nil
	}
	return *sum, nil
}
