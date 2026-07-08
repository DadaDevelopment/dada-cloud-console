package api

import (
	"encoding/json"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ingressRuleReq struct {
	Path string `json:"path"`
	App  string `json:"app"`
	Port int    `json:"port"`
}

type createIngressRequest struct {
	Name        string   `json:"name"`
	Host        string   `json:"host"`
	Aliases     []string `json:"aliases"`
	SSLRedirect bool     `json:"ssl_redirect"`
	BasicAuth   string   `json:"basic_auth"`
	TLS         struct {
		Enabled    bool   `json:"enabled"`
		MinVersion string `json:"min_version"`
		CertPath   string `json:"cert_path"`
		KeyPath    string `json:"key_path"`
	} `json:"tls"`
	Rules []ingressRuleReq `json:"rules"`
}

// CreateIngress enqueues a CreateIngress operation that provisions a managed
// Ingress (routing + TLS) Resource on a VM environment as a first-class nginx
// Application whose config is GENERATED from the routing spec (declarative — no
// hand-written nginx template). VM/compose environments only.
//
// @ID          createIngress
// @Summary     Create a managed Ingress (routing) on a VM environment
// @Description Provisions a managed Ingress on a VM (compose) environment: an nginx Application whose config is generated from host/rules/TLS and shipped from git. Rules route a path to an app service:port on the same stack. Asynchronous: returns 202 with an operation; poll until terminal.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string               true "Project UUID"
// @Param       envId     path     string               true "Environment UUID"
// @Param       body      body     createIngressRequest true "Ingress spec"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/ingress [post]
func (h *Handler) CreateIngress(c *gin.Context) {
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
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
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

	var req createIngressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		respondError(c, http.StatusBadRequest, "name is required")
		return
	}
	if err := validateKubeName(req.Name); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Host == "" {
		respondError(c, http.StatusBadRequest, "host is required")
		return
	}

	payloadBytes, _ := json.Marshal(req)

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreateIngress', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, req.Name, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	auditMeta, _ := json.Marshal(map[string]any{"name": req.Name, "host": req.Host})
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'CreateIngress', 'App', $4, $5)`,
		claims.UserID, projectID, op.ID, req.Name, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "ingress creation queued"})
}
