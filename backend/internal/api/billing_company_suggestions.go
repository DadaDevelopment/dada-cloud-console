package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/partylookup"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SuggestInvoiceCompanies returns companies matching a partial INN so the
// invoice form can fill payer requisites from one selected organisation.
// DaData is called only from the backend; its API key never reaches clients.
//
// @ID          suggestInvoiceCompanies
// @Summary     Suggest payer organisations by INN
// @Description Returns active organisations matching a partial or full INN. Requires write role on the project.
// @Tags        billing
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path string true "Project UUID"
// @Param       q query string true "At least three INN digits"
// @Success     200 {object} map[string]interface{}
// @Failure     400 {object} map[string]string
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Failure     404 {object} map[string]string
// @Failure     409 {object} map[string]string
// @Failure     502 {object} map[string]string
// @Router      /projects/{projectId}/billing/company-suggestions [get]
func (h *Handler) SuggestInvoiceCompanies(c *gin.Context) {
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
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
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

	query := onlyDigits(c.Query("q"))
	if len(query) < 3 || len(query) > 12 {
		respondErrorCode(c, http.StatusBadRequest, "invalid_inn_query", "Введите от трёх до двенадцати цифр ИНН")
		return
	}
	if h.cfg.DaDataAPIKey == "" {
		respondErrorCode(c, http.StatusConflict, "company_lookup_not_configured", "Автозаполнение реквизитов пока не подключено")
		return
	}

	suggestions, err := partylookup.New(h.cfg.DaDataAPIKey).Suggest(c.Request.Context(), query)
	if err != nil {
		respondErrorCode(c, http.StatusBadGateway, "company_lookup_unavailable", "Не удалось получить реквизиты компании")
		return
	}
	c.JSON(http.StatusOK, gin.H{"suggestions": suggestions})
}

func onlyDigits(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, value)
}
