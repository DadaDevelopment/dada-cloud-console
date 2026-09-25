package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// agentProjectIDs returns every project holding an agent of this name in
// resource_snapshots, the same table ListAgents reads, with the project whose
// row is a ManagedAgent claim first (a claim wins over a raw CR of the same
// name, matching agentSnapshotKind). Normally there is exactly one.
func (h *Handler) agentProjectIDs(ctx context.Context, agentName string) ([]uuid.UUID, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT project_id FROM resource_snapshots
		 WHERE kind IN ('ManagedAgent', 'Agent') AND name = $1
		 GROUP BY project_id
		 ORDER BY bool_or(kind = 'ManagedAgent') DESC, project_id`, agentName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// authorizeAgent is the project gate of the routes that address an agent by
// name alone (/agents/{agentName}/...). The name is the whole address because
// the agent runtime namespace is shared, so the gate resolves the owning
// project from resource_snapshots and applies effectiveRole to it, which also
// covers /agents identities through agent_project_grants.
//
// A caller with no role in the project gets 404, the same answer as for a name
// that does not exist, so agent names of other tenants cannot be enumerated. A
// write additionally needs canWrite, else 403. When several projects hold the
// name the caller needs the role in every one of them: a same-named claim in
// the caller's own project must not unlock somebody else's agent. A platform
// admin passes for a name with no snapshot, with an empty project id.
//
// It writes the refusal itself and returns the project id to stamp onto
// anything the route persists, and whether the handler may go on.
func (h *Handler) authorizeAgent(c *gin.Context, claims *auth.Claims, agentName string, write bool) (string, bool) {
	ctx := c.Request.Context()
	projectIDs, err := h.agentProjectIDs(ctx, agentName)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve agent project")
		return "", false
	}
	if len(projectIDs) == 0 {
		if isGod(claims) {
			return "", true
		}
		respondNotFound(c)
		return "", false
	}
	for _, projectID := range projectIDs {
		role, err := h.effectiveRole(ctx, claims, projectID)
		if errors.Is(err, pgx.ErrNoRows) {
			respondNotFound(c)
			return "", false
		}
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to check project membership")
			return "", false
		}
		if write && !canWrite(role) {
			respondForbidden(c)
			return "", false
		}
	}
	return projectIDs[0].String(), true
}
