package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/kagent"
	"github.com/dada-tuda/console/backend/internal/models"
)

// agentRuntime is the reader NewHandler built, or a disabled one for a Handler
// assembled by hand in a test.
func (h *Handler) agentRuntime() *kagent.Reader {
	if h.agents == nil {
		return kagent.NewReaderWith(nil, nil, "", "")
	}
	return h.agents
}

// AgentToolResponse is one MCP server as the agent form offers it.
//
// The URL is deliberately absent for everyone but platform admins: it is a
// cluster-internal address, it is of no use to a tenant, and the tool list is
// readable by every authenticated user because the servers are shared platform
// infrastructure rather than anyone's private resource.
type AgentToolResponse struct {
	Name            string             `json:"name"`
	Description     string             `json:"description"`
	URL             string             `json:"url,omitempty"`
	Protocol        string             `json:"protocol,omitempty"`
	Project         string             `json:"project,omitempty"`
	Ready           bool               `json:"ready"`
	DiscoveredTools []kagent.ToolEntry `json:"discovered_tools"`
}

// ListAgentTools returns the MCP servers an agent can be pointed at, with what
// each one actually discovered.
//
// @ID          listAgentTools
// @Summary     List the MCP servers an agent can use
// @Description Reads the RemoteMCPServer objects of the agent runtime, including whether each one is accepted and which tools it actually discovered. Pass the project to also see the MCP servers that project added itself; another project's servers are never listed. The cluster-internal URL is returned to platform admins and to the project that owns the server. 503 when this console cannot see the agent runtime.
// @Tags        agents
// @Produce     json
// @Security    BearerAuth
// @Param       project query string false "Project id or name whose own MCP servers should be included"
// @Success     200 {object} map[string]interface{} "object with the tools array"
// @Failure     401 {object} map[string]string
// @Failure     503 {object} map[string]string
// @Router      /agents/tools [get]
func (h *Handler) ListAgentTools(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	tools, err := h.agentRuntime().ListTools(c.Request.Context())
	if errors.Is(err, kagent.ErrClusterUnavailable) {
		respondError(c, http.StatusServiceUnavailable, "agent runtime is not reachable from this console")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list agent tools")
		return
	}

	project := h.toolProjectName(c)
	out := make([]AgentToolResponse, 0, len(tools))
	for _, t := range tools {
		if t.Project != "" && t.Project != project && !claims.IsPlatformAdmin() {
			continue
		}
		item := AgentToolResponse{
			Name:            t.Name,
			Description:     t.Description,
			Protocol:        t.Protocol,
			Project:         t.Project,
			Ready:           t.Ready,
			DiscoveredTools: t.DiscoveredTools,
		}
		if claims.IsPlatformAdmin() || (t.Project != "" && t.Project == project) {
			item.URL = t.URL
		}
		out = append(out, item)
	}
	c.JSON(http.StatusOK, gin.H{"tools": out})
}

// ValidateAgentRequest is a draft agent as the form holds it.
type ValidateAgentRequest struct {
	Name           string               `json:"name"`
	Prompt         string               `json:"prompt"`
	Tools          agentToolDraft       `json:"tools"`
	AllowedHeaders []string             `json:"allowed_headers"`
	Env            []models.AgentEnvVar `json:"env"`
}

// agentToolDraft is the tools list of a draft agent, in either shape the save
// endpoint accepts: a bare name for a server somebody else runs, or the whole
// reference for a server this project brings itself.
//
// The two shapes exist because this endpoint predates custom MCP servers and is
// called by clients that still send `["reels-task-tools"]`. Refusing those would
// break them; answering "no such MCP server" for a draft that saves fine would
// be worse -- the validator would be lying about the very draft it is meant to
// clear.
type agentToolDraft []models.AgentToolRef

// UnmarshalJSON accepts a string or an object per element.
func (d *agentToolDraft) UnmarshalJSON(raw []byte) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	out := make([]models.AgentToolRef, 0, len(items))
	for _, item := range items {
		var name string
		if err := json.Unmarshal(item, &name); err == nil {
			out = append(out, models.AgentToolRef{Name: name})
			continue
		}
		var ref models.AgentToolRef
		if err := json.Unmarshal(item, &ref); err != nil {
			return err
		}
		out = append(out, ref)
	}
	*d = out
	return nil
}

// AgentFieldError names the field a draft agent fails on.
type AgentFieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidateAgent checks a draft agent against everything the cluster will check
// later, and answers now.
//
// Everything here is refused by Kubernetes or by kagent anyway -- but a refusal
// there arrives after the git commit, as an Argo sync failure or an Unready CR,
// minutes later and attached to no user action. The same refusal here is a
// field error next to the field.
//
// A tool that is named but does not exist is the one check that needs the
// cluster: the agent would come up healthy and answer every question without
// it, which is the failure users report as "the agent is lying" rather than as
// an outage.
// @ID          validateAgent
// @Summary     Validate a draft agent before it is written to git
// @Description Checks name, prompt and requested MCP servers against everything the cluster would refuse later. Returns 400 with a per-field error list, or 200 when the draft is safe to commit. Each tools entry is either a bare server name (a server the platform runs) or the whole reference this project brings itself, with url, protocol and headers; a header value may cite the agent env as ${VAR}.
// @Tags        agents
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       request body     ValidateAgentRequest true "Draft agent"
// @Success     200     {object} map[string]interface{} "valid draft"
// @Failure     400     {object} map[string]interface{} "object with the per-field errors array"
// @Failure     401     {object} map[string]string
// @Router      /agents/validate [post]
func (h *Handler) ValidateAgent(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	var req ValidateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	problems := validateAgentDraft(saveAgentRequest{
		Name:   req.Name,
		Prompt: req.Prompt,
		Tools:  req.Tools,
		Env:    req.Env,
	})
	for _, header := range req.AllowedHeaders {
		if err := kagent.ValidateHeader(header); err != nil {
			problems = append(problems, AgentFieldError{Field: "allowed_headers", Message: err.Error()})
		}
	}

	if len(req.Tools) > 0 {
		tools, err := h.agentRuntime().ListTools(c.Request.Context())
		switch {
		case errors.Is(err, kagent.ErrClusterUnavailable):
			// The rest of the draft is still worth answering about. Silence
			// about the tools is better than refusing a valid name and prompt.
		case err != nil:
			respondError(c, http.StatusInternalServerError, "failed to check agent tools")
			return
		default:
			known := make(map[string]bool, len(tools))
			for _, t := range tools {
				known[t.Name] = true
			}
			for _, want := range req.Tools {
				if want.URL != "" || want.Name == "" {
					continue
				}
				if !known[want.Name] {
					problems = append(problems, AgentFieldError{
						Field:   "tools",
						Message: "no MCP server named " + want.Name + " exists in the agent runtime",
					})
				}
			}
		}
	}

	if len(problems) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"valid": false, "errors": problems})
		return
	}
	c.JSON(http.StatusOK, gin.H{"valid": true, "errors": []AgentFieldError{}})
}

// GetAgentState returns the live state of one agent: whether kagent accepted
// it, whether its pods serve, which prompt version is loaded, and where its
// traces are.
// @ID          getAgentState
// @Summary     Live state of one agent
// @Description Assembles the Agent CR conditions, the pods serving it, the prompt version those pods loaded and the Langfuse traces link. A not-yet-synced agent is reported as absent rather than as an error.
// @Tags        agents
// @Produce     json
// @Security    BearerAuth
// @Param       agentName path     string true "Agent name"
// @Success     200       {object} kagent.State
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /agents/{agentName}/state [get]
func (h *Handler) GetAgentState(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}

	state, err := h.agentRuntime().AgentState(c.Request.Context(), c.Param("agentName"))
	switch {
	case errors.Is(err, kagent.ErrClusterUnavailable):
		respondError(c, http.StatusServiceUnavailable, "agent runtime is not reachable from this console")
		return
	case err != nil:
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, state)
}

// toolProjectName resolves the ?project= the agent form sends -- the project the
// form is being filled in for -- into the project name the composition stamps
// onto a tool server it owns.
//
// Without it the list is every RemoteMCPServer of one shared namespace, which
// is every tenant's servers offered to every tenant. An unresolvable value is
// not an error: it simply matches nothing, so the caller is left with the
// platform's own servers rather than with somebody else's.
func (h *Handler) toolProjectName(c *gin.Context) string {
	raw := c.Query("project")
	if raw == "" {
		return ""
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return raw
	}
	var name string
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT name FROM projects WHERE id = $1`, id).Scan(&name); err != nil {
		return ""
	}
	return name
}
