package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

// managedAgentKind is the Crossplane claim the console writes, and adoptedAgentKind
// is the raw kagent CR that a hand-maintained resources.values.yaml carries.
//
// Both reach resource_snapshots through the same reader: the git-watcher upserts
// every manifest entry of an app's resources.values.yaml by (kind, name), so an
// agent written by hand into git is already a row here, exactly like a claim the
// console ordered. The list shows both, because "what is in git" is the whole
// point of that reader -- hiding the hand-written half would make the console
// disagree with the repository it renders. Only the claim half is writable: a
// claim named after an existing raw Agent would compose a SECOND CR with that
// name into the runtime namespace and the two would fight over it.
const (
	managedAgentKind = "ManagedAgent"
	adoptedAgentKind = "Agent"
)

// agentSnapshotKind reports which kind already holds this agent name in the
// environment, or "" when the name is free. A claim wins over a raw CR when
// both exist: the claim is the one the console can act on.
func (h *Handler) agentSnapshotKind(ctx context.Context, projectID, envID uuid.UUID, name string) (string, error) {
	var kind string
	err := h.pool.QueryRow(ctx,
		`SELECT kind FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind IN ('ManagedAgent', 'Agent') AND name = $3
		 ORDER BY (kind = 'ManagedAgent') DESC
		 LIMIT 1`,
		projectID, envID, name,
	).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return kind, nil
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
// @Description Returns the agents of this environment as git holds them: ManagedAgent claims ordered through the console and raw kagent Agent CRs maintained by hand. Live readiness comes from the agent state endpoint.
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
		 WHERE project_id = $1 AND environment_id = $2 AND kind IN ('ManagedAgent', 'Agent')
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
// @Failure     409       {object} map[string]string
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

	if problems := h.toolOwnershipProblems(c.Request.Context(), projectID, req.Tools); len(problems) > 0 {
		reject(http.StatusConflict, "tool_owned_by_another_project", gin.H{"valid": false, "errors": problems})
		return
	}

	existingKind, err := h.agentSnapshotKind(c.Request.Context(), projectID, envID, req.Name)
	if err != nil {
		reject(http.StatusInternalServerError, "lookup_failed", gin.H{"error": "failed to look up agent"})
		return
	}
	switch existingKind {
	case adoptedAgentKind:
		reject(http.StatusConflict, "agent_not_console_owned", gin.H{
			"error":   "agent_not_console_owned",
			"message": "this agent is a raw kagent CR maintained in git outside the console; editing it here would compose a second CR with the same name",
		})
		return
	case managedAgentKind:
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
// @Failure     409       {object} map[string]string
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

	existingKind, err := h.agentSnapshotKind(c.Request.Context(), projectID, envID, name)
	if err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "lookup_failed"})
		respondError(c, http.StatusInternalServerError, "failed to look up agent")
		return
	}
	if existingKind == "" {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_found", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}
	if existingKind == adoptedAgentKind {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "agent_not_console_owned", "status": http.StatusConflict})
		c.JSON(http.StatusConflict, gin.H{
			"error":   "agent_not_console_owned",
			"message": "this agent is a raw kagent CR maintained in git outside the console; the console has no git path to remove it from",
		})
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

	if h.tgGateway != nil {
		if err := h.tgGateway.Unbind(c.Request.Context(), name); err != nil {
			log.Printf("tg-gateway unbind on agent delete %q: %v", name, err)
		}
	}

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
	envNames := map[string]bool{}
	for _, e := range req.Env {
		envNames[e.Name] = true
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
		if t.URL == "" && (len(t.Headers) > 0 || t.Protocol != "") {
			problems = append(problems, AgentFieldError{
				Field:   fmt.Sprintf("tools[%d].url", i),
				Message: fmt.Sprintf("%s has no address, so it points at a server somebody else owns; its protocol and headers are set there, not here", t.Name),
			})
		}
		if t.URL != "" {
			if err := kagent.ValidateToolURL(t.URL); err != nil {
				problems = append(problems, AgentFieldError{
					Field:   fmt.Sprintf("tools[%d].url", i),
					Message: err.Error(),
				})
			}
		}
		if err := kagent.ValidateProtocol(t.Protocol); err != nil {
			problems = append(problems, AgentFieldError{
				Field:   fmt.Sprintf("tools[%d].protocol", i),
				Message: err.Error(),
			})
		}
		for j, hdr := range t.Headers {
			if err := kagent.ValidateOutgoingHeaderName(hdr.Name); err != nil {
				problems = append(problems, AgentFieldError{
					Field:   fmt.Sprintf("tools[%d].headers[%d].name", i, j),
					Message: err.Error(),
				})
			}
			for _, ref := range kagent.EnvReferences(hdr.Value) {
				if !envNames[ref] {
					problems = append(problems, AgentFieldError{
						Field:   fmt.Sprintf("tools[%d].headers[%d].value", i, j),
						Message: fmt.Sprintf("this agent has no environment variable %s, so the header would be sent with an empty value and the server would answer 401", ref),
					})
				}
			}
		}
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

// toolOwnershipProblems refuses a draft that would take over, or point at, an
// MCP server another project owns.
//
// The agent runtime is one namespace for the whole platform, so a
// RemoteMCPServer name is global: a claim that declares a server under a name
// somebody else already owns does not merge with it, it fights it, and the
// loser's agent quietly loses its tools. A reference without an address is the
// same hole read-only -- it would hand this project an agent that calls another
// tenant's server with that tenant's credentials.
//
// A cluster this console cannot see yields no problems: refusing every save
// because the reader is down would turn a monitoring outage into an editing
// outage, and the composition still cannot produce a duplicate object.
func (h *Handler) toolOwnershipProblems(ctx context.Context, projectID uuid.UUID, tools []models.AgentToolRef) []AgentFieldError {
	if len(tools) == 0 {
		return nil
	}
	runtime := h.agentRuntime()
	if !runtime.Enabled() {
		return nil
	}
	existing, err := runtime.ListTools(ctx)
	if err != nil {
		return nil
	}
	owner := map[string]string{}
	for _, t := range existing {
		owner[t.Name] = t.Project
	}

	var projectName string
	if err := h.pool.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, projectID).Scan(&projectName); err != nil {
		return nil
	}

	return toolNameTakeovers(tools, owner, projectName)
}

// toolNameTakeovers is the decision toolOwnershipProblems makes once it knows
// who owns which name, kept apart from the cluster and the database so it can
// be read and tested as the rule it is.
//
// Declaring an address under a name that already exists is a takeover whoever
// owns it: the composition writes one object per name, so the platform's own
// server would be replaced rather than joined. Naming an existing server
// without an address is only a problem when a project owns it -- that is how
// the shared platform servers are referenced.
func toolNameTakeovers(tools []models.AgentToolRef, owner map[string]string, projectName string) []AgentFieldError {
	var problems []AgentFieldError
	for i, t := range tools {
		ownedBy, known := owner[t.Name]
		if !known || ownedBy == projectName {
			continue
		}
		switch {
		case t.URL != "":
			problems = append(problems, AgentFieldError{
				Field:   fmt.Sprintf("tools[%d].name", i),
				Message: fmt.Sprintf("an MCP server named %s already runs on this platform and is not yours; a claim under that name replaces it rather than adding one, so pick a different name for your own server", t.Name),
			})
		case ownedBy != "":
			problems = append(problems, AgentFieldError{
				Field:   fmt.Sprintf("tools[%d].name", i),
				Message: fmt.Sprintf("the MCP server %s belongs to another project; pick a different name for your own server", t.Name),
			})
		}
	}
	return problems
}
