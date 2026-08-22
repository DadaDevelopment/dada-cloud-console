package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/kagent"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// saveAgentRequest is what the agent editor posts. It is one whole agent: the
// console has a single save, and a save re-states every field, so a partial
// body is a smaller agent rather than an unchanged one.
type saveAgentRequest struct {
	Name          string                `json:"name"`
	DisplayName   string                `json:"display_name"`
	Description   string                `json:"description"`
	Prompt        string                `json:"prompt"`
	PromptVersion string                `json:"prompt_version"`
	ModelConfig   string                `json:"model_config"`
	Runtime       string                `json:"runtime"`
	Tools         []models.AgentToolRef `json:"tools"`
	Env           []models.AgentEnvVar  `json:"env"`
}

// ListAgents returns the agents of one environment as the console knows them.
//
// This is the git-side view: what has been ordered. Whether the agent is
// actually up, and which prompt version is answering, comes from
// GET /agents/{agentName}/state, which reads the runtime. The two diverge on
// every rollout and permanently on a stuck one, which is why they are separate
// endpoints rather than one merged object that would have to lie about one half.
//
// @ID          listAgents
// @Summary     List agents in an environment
// @Description Returns the agents ordered through the console for this environment. Live readiness comes from the agent state endpoint.
// @Tags        agents
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Success     200       {object} map[string]interface{} "object with an agents array of ResourceSnapshot"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/agents [get]
func (h *Handler) ListAgents(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondNotFound(c)
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, environment_id, kind, name, phase, summary_json, last_synced_at
		 FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ManagedAgent'
		 ORDER BY name`,
		projectID, envID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query agents")
		return
	}
	defer rows.Close()

	agents := []models.ResourceSnapshot{}
	for rows.Next() {
		var rs models.ResourceSnapshot
		if err := rows.Scan(
			&rs.ID, &rs.ProjectID, &rs.EnvironmentID, &rs.Kind, &rs.Name,
			&rs.Phase, &rs.SummaryJSON, &rs.LastSyncedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan agent")
			return
		}
		agents = append(agents, rs)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading agents")
		return
	}
	c.JSON(http.StatusOK, gin.H{"agents": agents})
}

// SaveAgent enqueues the operation that writes one agent into git.
//
// The console never commits to the infrastructure repo itself: it writes an
// operation row and the gitops-agent renders the ManagedAgent claim, commits it
// and records the sha. That is what keeps a prompt edit auditable and what
// keeps two saves from racing inside one clone.
//
// Create and update are the same operation on purpose. The claim is upserted by
// name, so a retried operation cannot produce a second agent, and an editor that
// does not know whether the agent exists yet still does the right thing.
//
// @ID          saveAgent
// @Summary     Create or update an agent
// @Description Queues the git write for one agent (prompt, tools, model). Async: returns 202 with an operation; poll until terminal. Re-posting the same name updates that agent.
// @Tags        agents
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string           true "Project UUID"
// @Param       envId     path     string           true "Environment UUID"
// @Param       body      body     saveAgentRequest true "Agent specification"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/agents [post]
func (h *Handler) SaveAgent(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}

	var req saveAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	action := "CreateAgent"
	audit := func(opID uuid.UUID, outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			OperationID:   opID,
			Action:        action,
			ResourceKind:  "ManagedAgent",
			ResourceName:  req.Name,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	reject := func(status int, reason string, body any) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		c.JSON(status, body)
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_a_writer"})
		return
	}

	if problems := validateAgentDraft(req); len(problems) > 0 {
		reject(http.StatusBadRequest, "invalid_agent", gin.H{"valid": false, "errors": problems})
		return
	}

	var existing int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ManagedAgent' AND name = $3`,
		projectID, envID, req.Name,
	).Scan(&existing); err != nil {
		reject(http.StatusInternalServerError, "lookup_failed", gin.H{"error": "failed to look up agent"})
		return
	}
	if existing > 0 {
		action = "UpdateAgent"
	}

	payload := models.SaveAgentPayload{
		Name:          req.Name,
		DisplayName:   req.DisplayName,
		Description:   req.Description,
		Prompt:        req.Prompt,
		PromptVersion: req.PromptVersion,
		ModelConfig:   req.ModelConfig,
		Runtime:       req.Runtime,
		Tools:         req.Tools,
		Env:           req.Env,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		reject(http.StatusInternalServerError, "marshal_failed", gin.H{"error": "failed to marshal payload"})
		return
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		reject(http.StatusInternalServerError, "operation_begin_failed", gin.H{"error": "failed to create operation"})
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var op models.Operation
	row := tx.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, $4, 'ManagedAgent', $5, 'Created', $6)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, action, req.Name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		reject(http.StatusInternalServerError, "operation_insert_failed", gin.H{"error": "failed to create operation"})
		return
	}

	if action == "CreateAgent" {
		if err = seedOptimisticSnapshot(c.Request.Context(), tx, projectID, envID, "ManagedAgent", req.Name, map[string]any{
			"display_name":   req.DisplayName,
			"prompt_version": req.PromptVersion,
		}); err != nil {
			reject(http.StatusInternalServerError, "snapshot_seed_failed", gin.H{"error": "failed to create operation"})
			return
		}
	}

	if err = tx.Commit(c.Request.Context()); err != nil {
		reject(http.StatusInternalServerError, "operation_commit_failed", gin.H{"error": "failed to create operation"})
		return
	}

	audit(op.ID, auditOutcomeSuccess, map[string]any{
		"prompt_version": req.PromptVersion,
		"tools":          len(req.Tools),
		"prompt_bytes":   len(req.Prompt),
	})
	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "agent save queued"})
}

// DeleteAgent enqueues the removal of one agent from git.
//
// @ID          deleteAgent
// @Summary     Delete an agent
// @Description Queues the git write that removes the agent claim. Argo then prunes the agent, its prompt and its MCP servers. Async: returns 202 with an operation.
// @Tags        agents
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Agent name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/agents/{name} [delete]
func (h *Handler) DeleteAgent(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	name := c.Param("name")

	audit := func(opID uuid.UUID, outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			OperationID:   opID,
			Action:        "DeleteAgent",
			ResourceKind:  "ManagedAgent",
			ResourceName:  name,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_a_writer"})
		return
	}

	var exists int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ManagedAgent' AND name = $3`,
		projectID, envID, name,
	).Scan(&exists); err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "lookup_failed"})
		respondError(c, http.StatusInternalServerError, "failed to look up agent")
		return
	}
	if exists == 0 {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_found", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}

	payloadBytes, err := json.Marshal(models.DeleteAgentPayload{Name: name})
	if err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "payload_marshal_failed"})
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeleteAgent', 'ManagedAgent', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "operation_insert_failed"})
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	audit(op.ID, auditOutcomeSuccess, nil)
	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "agent deletion queued"})
}

// validateAgentDraft refuses a draft before it becomes a commit.
//
// Everything here is checkable without the cluster, which matters: a refusal
// that arrives as an Argo sync failure minutes later reads as "the platform is
// broken", while the same refusal on the field reads as "fix this line".
func validateAgentDraft(req saveAgentRequest) []AgentFieldError {
	var problems []AgentFieldError
	if err := kagent.ValidateName(req.Name); err != nil {
		problems = append(problems, AgentFieldError{Field: "name", Message: err.Error()})
	}
	if err := kagent.ValidatePrompt(req.Prompt); err != nil {
		problems = append(problems, AgentFieldError{Field: "prompt", Message: err.Error()})
	}
	seen := map[string]bool{}
	for i, t := range req.Tools {
		if t.Name == "" {
			problems = append(problems, AgentFieldError{
				Field:   fmt.Sprintf("tools[%d].name", i),
				Message: "a tool reference needs the name of an MCP server",
			})
			continue
		}
		if seen[t.Name] {
			problems = append(problems, AgentFieldError{
				Field:   fmt.Sprintf("tools[%d].name", i),
				Message: fmt.Sprintf("%s is listed twice; two claims on one MCP server name fight rather than merge", t.Name),
			})
			continue
		}
		seen[t.Name] = true
		for j, hdr := range t.AllowedHeaders {
			if err := kagent.ValidateHeader(hdr); err != nil {
				problems = append(problems, AgentFieldError{
					Field:   fmt.Sprintf("tools[%d].allowed_headers[%d]", i, j),
					Message: err.Error(),
				})
			}
		}
	}
	for i, e := range req.Env {
		if e.Name == "" {
			problems = append(problems, AgentFieldError{
				Field:   fmt.Sprintf("env[%d].name", i),
				Message: "an environment variable needs a name",
			})
		}
	}
	return problems
}
