package api

import (
	"context"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// hasGroup reports whether the full-path group list contains want.
func hasGroup(groups []string, want string) bool {
	for _, g := range groups {
		if g == want {
			return true
		}
	}
	return false
}

// isGod reports whether the caller has unconditional Owner access to every
// project: the local dev-god token (AUTH_MODE=local) or the internal staff
// /platform-admins Keycloak group (ADR-009). Neither is a customer role.
func isGod(claims *auth.Claims) bool {
	return claims != nil &&
		(claims.OrgID == "local-dev" || hasGroup(claims.Groups, "/platform-admins"))
}

// effectiveRole resolves the caller's role in a project purely from fat JWT
// claims (ADR-009) — no project_members lookup, no group-path parsing.
//
// Effective project role = max(org_role, projects[project_id]):
//   - god (local dev / platform-admins) → Owner, everywhere.
//   - explicit project membership in claims → max(org_role, project role).
//   - otherwise org Owner/Admin cascade into every project within their org
//     (a single tenant-isolation read of projects.org_id, not a role lookup).
//
// Returns pgx.ErrNoRows when the caller has no access — preserving the contract
// the handlers already branch on.
func (h *Handler) effectiveRole(ctx context.Context, claims *auth.Claims, projectID uuid.UUID) (models.MemberRole, error) {
	if claims == nil {
		return "", pgx.ErrNoRows
	}
	if isGod(claims) {
		return models.MemberRoleOwner, nil
	}

	org := models.MemberRole(claims.OrgRole)

	if pr, ok := claims.Projects[projectID.String()]; ok {
		return models.MaxRole(org, models.MemberRole(pr)), nil
	}

	if org == models.MemberRoleOwner || org == models.MemberRoleAdmin {
		inOrg, err := h.projectInOrg(ctx, projectID, claims.OrgID)
		if err != nil {
			return "", err
		}
		if inOrg {
			return org, nil
		}
	}

	return "", pgx.ErrNoRows
}

// projectInOrg reports whether the project resource row belongs to org. Enforces
// tenant isolation for the org-role cascade (PRD-IAM "Security": org_id from the
// claim must match the resource owner).
func (h *Handler) projectInOrg(ctx context.Context, projectID uuid.UUID, orgID string) (bool, error) {
	if orgID == "" {
		return false, nil
	}
	var exists bool
	err := h.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1 AND org_id = $2)`,
		projectID, orgID,
	).Scan(&exists)
	return exists, err
}

// envBelongsToProject reports whether an environment belongs to a project. Used
// to close cross-tenant IDOR on env-scoped routes: membership is checked against
// the URL projectId, but the envId is attacker-supplied and otherwise unvalidated,
// so a member of project A could target an env of project B without this guard.
func (h *Handler) envBelongsToProject(ctx context.Context, envID, projectID uuid.UUID) (bool, error) {
	var exists bool
	err := h.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM environments WHERE id = $1 AND project_id = $2)`,
		envID, projectID,
	).Scan(&exists)
	return exists, err
}

// isOrgAdmin reports whether the role can perform admin-level actions (manage
// members/keys, approve gated operations): Owner or Admin (ADR-009). This
// replaces the legacy platform-admin gate.
func isOrgAdmin(role models.MemberRole) bool {
	return role == models.MemberRoleOwner || role == models.MemberRoleAdmin
}

// canWrite returns true for roles that can create/modify resources. ReadOnly
// (and any unknown role) cannot (ADR-009 4-role model).
func canWrite(role models.MemberRole) bool {
	return role == models.MemberRoleOwner ||
		role == models.MemberRoleAdmin ||
		role == models.MemberRoleDeveloper
}
