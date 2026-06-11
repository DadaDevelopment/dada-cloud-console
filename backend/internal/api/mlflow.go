package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/mlflow"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListMLflowRegisteredModels returns the registered models visible to the
// caller's project (filtered by ai_storage_prefix).
//
// @ID          listMlflowModels
// @Summary     List MLflow registered models for a project
// @Description Returns the MLflow registered models visible to a project, filtered by the project's AI storage prefix. Read-only. The project query param (project UUID) is required.
// @Tags        mlflow
// @Produce     json
// @Security    BearerAuth
// @Param       project query    string true "Project UUID"
// @Success     200     {object} map[string]interface{} "object with a models array"
// @Failure     400     {object} map[string]string
// @Failure     401     {object} map[string]string
// @Failure     404     {object} map[string]string
// @Failure     502     {object} map[string]string
// @Failure     503     {object} map[string]string
// @Router      /mlflow/registered-models [get]
func (h *Handler) ListMLflowRegisteredModels(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Query("project"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "project query parameter is required and must be a UUID")
		return
	}
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}
	prefix, err := h.lookupStoragePrefix(c.Request.Context(), projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read project storage prefix")
		return
	}
	if prefix == "" {
		c.JSON(http.StatusOK, gin.H{"models": []any{}, "warning": "project has no AI storage prefix configured"})
		return
	}
	if h.mlflow == nil {
		respondError(c, http.StatusServiceUnavailable, "MLflow registry is not configured")
		return
	}
	out, err := h.mlflow.SearchRegisteredModels(c.Request.Context(), prefix, 200)
	if err != nil {
		if errors.Is(err, mlflow.ErrUnreachable) {
			respondError(c, http.StatusBadGateway, "MLflow registry unreachable; paste artifactURI manually")
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to list registered models")
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": out})
}

// ListMLflowModelVersions returns all versions of a registered model, filtered
// by storage prefix.
//
// @ID          listMlflowModelVersions
// @Summary     List versions of an MLflow registered model
// @Description Returns all versions of one MLflow registered model, filtered by the project's AI storage prefix. Read-only. The project query param (project UUID) is required.
// @Tags        mlflow
// @Produce     json
// @Security    BearerAuth
// @Param       name    path     string true "MLflow registered model name"
// @Param       project query    string true "Project UUID"
// @Success     200     {object} map[string]interface{} "object with a versions array"
// @Failure     400     {object} map[string]string
// @Failure     401     {object} map[string]string
// @Failure     404     {object} map[string]string
// @Failure     502     {object} map[string]string
// @Failure     503     {object} map[string]string
// @Router      /mlflow/registered-models/{name}/versions [get]
func (h *Handler) ListMLflowModelVersions(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Query("project"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "project query parameter is required and must be a UUID")
		return
	}
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}
	prefix, err := h.lookupStoragePrefix(c.Request.Context(), projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read project storage prefix")
		return
	}
	if h.mlflow == nil {
		respondError(c, http.StatusServiceUnavailable, "MLflow registry is not configured")
		return
	}
	name := c.Param("name")
	versions, err := h.mlflow.GetRegisteredModelVersions(c.Request.Context(), name)
	if err != nil {
		if errors.Is(err, mlflow.ErrUnreachable) {
			respondError(c, http.StatusBadGateway, "MLflow registry unreachable")
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to list versions")
		return
	}
	if prefix != "" {
		filtered := versions[:0]
		for _, v := range versions {
			if startsWith(v.Source, prefix) {
				filtered = append(filtered, v)
			}
		}
		versions = filtered
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

// GetMLflowModelVersion returns one specific MLflow version. Used by the
// wizard to resolve the artifactURI before showing the review screen.
//
// @ID          getMlflowModelVersion
// @Summary     Get one MLflow model version
// @Description Returns a single MLflow registered-model version, including its artifact source URI, scoped to the project's AI storage prefix. Read-only. The project query param (project UUID) is required.
// @Tags        mlflow
// @Produce     json
// @Security    BearerAuth
// @Param       name    path     string true "MLflow registered model name"
// @Param       version path     string true "Model version"
// @Param       project query    string true "Project UUID"
// @Success     200     {object} map[string]interface{} "object with the version"
// @Failure     400     {object} map[string]string
// @Failure     401     {object} map[string]string
// @Failure     404     {object} map[string]string
// @Failure     502     {object} map[string]string
// @Failure     503     {object} map[string]string
// @Router      /mlflow/registered-models/{name}/versions/{version} [get]
func (h *Handler) GetMLflowModelVersion(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Query("project"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "project query parameter is required")
		return
	}
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}
	prefix, err := h.lookupStoragePrefix(c.Request.Context(), projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read project storage prefix")
		return
	}
	if h.mlflow == nil {
		respondError(c, http.StatusServiceUnavailable, "MLflow registry is not configured")
		return
	}
	name := c.Param("name")
	version := c.Param("version")
	v, err := h.mlflow.GetModelVersion(c.Request.Context(), name, version)
	if err != nil {
		if errors.Is(err, mlflow.ErrUnreachable) {
			respondError(c, http.StatusBadGateway, "MLflow registry unreachable")
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to fetch version")
		return
	}
	if prefix != "" && !startsWith(v.Source, prefix) {
		respondNotFound(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": v})
}

func (h *Handler) lookupStoragePrefix(ctx context.Context, projectID uuid.UUID) (string, error) {
	var p *string
	if err := h.pool.QueryRow(ctx,
		`SELECT ai_storage_prefix FROM projects WHERE id = $1`, projectID,
	).Scan(&p); err != nil {
		return "", err
	}
	if p == nil {
		return "", nil
	}
	return *p, nil
}

func startsWith(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}
