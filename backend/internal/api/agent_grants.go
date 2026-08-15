package api

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	agentGrantDefaultTTL = 60 * time.Minute
	agentGrantMaxTTL     = 24 * time.Hour
)

// createAgentGrantRequest names the machine identity, what it may do, and for
// how long. Everything except AgentUsername has a default: Developer (write,
// but not member management) for one hour.
type createAgentGrantRequest struct {
	AgentUsername string `json:"agent_username"`
	Role          string `json:"role,omitempty"`
	TTLMinutes    int    `json:"ttl_minutes,omitempty"`
	RunRef        string `json:"run_ref,omitempty"`
}

// agentGrantResponse describes one grant. There is no token in it: the grant
// scopes an identity the agent already authenticates as, so nothing secret is
// minted here and nothing has to be stored by the caller.
type agentGrantResponse struct {
	ID            uuid.UUID  `json:"id"`
	ProjectID     uuid.UUID  `json:"project_id"`
	AgentUsername string     `json:"agent_username"`
	Role          string     `json:"role"`
	RunRef        string     `json:"run_ref,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	Active        bool       `json:"active"`
}

// agentGrantableRole restricts what a grant may carry. Owner and Admin are
// excluded deliberately: those roles manage membership, and an automation that
// can manage membership can widen its own access, which is the property this
// whole mechanism exists to prevent.
func agentGrantableRole(role string) (models.MemberRole, bool) {
	switch models.MemberRole(role) {
	case "":
		return models.MemberRoleDeveloper, true
	case models.MemberRoleDeveloper:
		return models.MemberRoleDeveloper, true
	case models.MemberRoleReadOnly:
		return models.MemberRoleReadOnly, true
	}
	return "", false
}

// CreateAgentGrant scopes a machine identity to this project for a bounded time.
//
// @ID          createAgentGrant
// @Summary     Grant a machine identity access to one project
// @Description Gives an /agents identity a Developer or ReadOnly role on exactly this project until it is revoked or expires. Requires Owner/Admin on the project — you can only lend access you already administer. No token is issued: the agent keeps authenticating as itself, and this row is what its role is resolved from.
// @Tags        agent-grant
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                  true "Project UUID"
// @Param       body      body     createAgentGrantRequest true "Agent identity, role and TTL"
// @Success     201       {object} agentGrantResponse
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/agent-grants [post]
func (h *Handler) CreateAgentGrant(c *gin.Context) {
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
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if isAgent(claims) || !isOrgAdmin(role) {
		respondForbidden(c)
		return
	}

	var req createAgentGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.AgentUsername == "" {
		respondError(c, http.StatusBadRequest, "agent_username is required")
		return
	}
	grantRole, ok := agentGrantableRole(req.Role)
	if !ok {
		respondError(c, http.StatusBadRequest, "role must be Developer or ReadOnly")
		return
	}
	ttl := agentGrantDefaultTTL
	if req.TTLMinutes > 0 {
		ttl = time.Duration(req.TTLMinutes) * time.Minute
	}
	if ttl > agentGrantMaxTTL {
		respondError(c, http.StatusBadRequest, "ttl_minutes must not exceed 1440")
		return
	}

	var agentUserID uuid.UUID
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id FROM users WHERE username = $1`, req.AgentUsername).Scan(&agentUserID)
	if err == pgx.ErrNoRows {
		respondError(c, http.StatusBadRequest, "unknown agent_username")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve agent identity")
		return
	}

	var out agentGrantResponse
	err = h.pool.QueryRow(c.Request.Context(), `
		INSERT INTO agent_project_grants
		            (project_id, agent_user_id, role, run_ref, granted_by, expires_at)
		     VALUES ($1, $2, $3, $4, $5, now() + $6::interval)
		  RETURNING id, created_at, expires_at`,
		projectID, agentUserID, string(grantRole), req.RunRef, claims.UserID, ttl.String(),
	).Scan(&out.ID, &out.CreatedAt, &out.ExpiresAt)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create agent grant")
		return
	}
	out.ProjectID = projectID
	out.AgentUsername = req.AgentUsername
	out.Role = string(grantRole)
	out.RunRef = req.RunRef
	out.Active = true

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:    projectID,
		Action:       "CreateAgentGrant",
		ResourceKind: "AgentGrant",
		ResourceName: req.AgentUsername,
		Outcome:      auditOutcomeSuccess,
		Metadata: map[string]any{
			"role":       string(grantRole),
			"run_ref":    req.RunRef,
			"expires_at": out.ExpiresAt,
		},
	})

	c.JSON(http.StatusCreated, out)
}

// ListAgentGrants shows every grant ever made on this project, live ones first.
//
// @ID          listAgentGrants
// @Summary     List machine-identity grants on a project
// @Description Returns the project's agent grants, newest first, including revoked and expired ones — the row is the audit trail of who lent which automation access, for which run, and when it ended.
// @Tags        agent-grant
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {array}  agentGrantResponse
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/agent-grants [get]
func (h *Handler) ListAgentGrants(c *gin.Context) {
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
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !isOrgAdmin(role) {
		respondForbidden(c)
		return
	}

	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT g.id, COALESCE(u.username, ''), g.role, g.run_ref,
		       g.created_at, g.expires_at, g.revoked_at,
		       (g.revoked_at IS NULL AND g.expires_at > now()) AS active
		  FROM agent_project_grants g
		  LEFT JOIN users u ON u.id = g.agent_user_id
		 WHERE g.project_id = $1
		 ORDER BY g.created_at DESC`, projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list agent grants")
		return
	}
	defer rows.Close()

	out := []agentGrantResponse{}
	for rows.Next() {
		g := agentGrantResponse{ProjectID: projectID}
		if err := rows.Scan(&g.ID, &g.AgentUsername, &g.Role, &g.RunRef,
			&g.CreatedAt, &g.ExpiresAt, &g.RevokedAt, &g.Active); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read agent grants")
			return
		}
		out = append(out, g)
	}
	if rows.Err() != nil {
		respondError(c, http.StatusInternalServerError, "failed to read agent grants")
		return
	}
	c.JSON(http.StatusOK, out)
}

// RevokeAgentGrant ends a grant now, without waiting for its expiry.
//
// @ID          revokeAgentGrant
// @Summary     Revoke a machine-identity grant
// @Description Ends the grant immediately — the agent's next request to this project resolves to no access (404). Idempotent: revoking an already-ended grant succeeds. The row is kept as the audit trail.
// @Tags        agent-grant
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       grantId   path     string true "Grant UUID"
// @Success     204
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/agent-grants/{grantId} [delete]
func (h *Handler) RevokeAgentGrant(c *gin.Context) {
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
	grantID, err := uuid.Parse(c.Param("grantId"))
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
	if isAgent(claims) || !isOrgAdmin(role) {
		respondForbidden(c)
		return
	}

	tag, err := h.pool.Exec(c.Request.Context(), `
		UPDATE agent_project_grants
		   SET revoked_at = now()
		 WHERE id = $1 AND project_id = $2 AND revoked_at IS NULL`, grantID, projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to revoke agent grant")
		return
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := h.pool.QueryRow(c.Request.Context(),
			`SELECT EXISTS(SELECT 1 FROM agent_project_grants WHERE id = $1 AND project_id = $2)`,
			grantID, projectID).Scan(&exists); err != nil || !exists {
			respondNotFound(c)
			return
		}
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:    projectID,
		Action:       "RevokeAgentGrant",
		ResourceKind: "AgentGrant",
		ResourceName: grantID.String(),
		Outcome:      auditOutcomeSuccess,
	})

	c.Status(http.StatusNoContent)
}
