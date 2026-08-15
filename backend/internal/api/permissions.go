package api

import (
	"context"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// isGod reports whether the caller has unconditional Owner access to every
// project: the hidden /platform-admins staff group (and the local dev-god token,
// which carries that group). Decoded from native group paths (ADR-009 §4). Not a
// customer role.
func isGod(claims *auth.Claims) bool {
	return claims != nil && claims.IsPlatformAdmin()
}

// isPlatformAnalyst reports the read-only staff group (/platform-analysts): the
// caller reads every project and every admin read endpoint but writes nothing.
// It is deliberately NOT folded into isGod — isGod is the write gate.
func isPlatformAnalyst(claims *auth.Claims) bool {
	return claims != nil && claims.IsPlatformAnalyst()
}

// isAdminReader gates the read-only /admin/* endpoints (overview, costs, audit,
// gateway usage). Write endpoints under /admin keep the stricter isGod gate.
func isAdminReader(claims *auth.Claims) bool {
	return isGod(claims) || isPlatformAnalyst(claims)
}

// isAgent reports a non-human automation identity (/agents). Agents may act only
// inside the projects explicitly granted to them and may never create projects.
func isAgent(claims *auth.Claims) bool {
	return claims != nil && claims.IsAgent()
}

// resolveRole computes the caller's effective role on a project from the decoded
// native claims, given the project's owning org (looked up locally — dada-cloud
// owns the resource row keyed by project_id/org_id, ADR-009 §4).
//
// Effective project role = max(orgRole(projectOrg), projectRole):
//   - god (/platform-admins) → Owner, everywhere.
//   - explicit project membership → max(orgRole, projectRole); any org role
//     boosts the explicit grant (e.g. org Admin + project ReadOnly → Admin).
//   - no explicit project membership → only an Owner/Admin org role cascades in;
//     org Developer/ReadOnly do NOT grant blanket project access ("see own
//     projects", PRD-IAM role table).
//   - read-only staff (/platform-analysts) → ReadOnly on every project. Checked
//     last, so an explicit grant or an org cascade always wins the max-merge.
//
// Returns ok=false when the caller has no access. Pure (no DB) so it is unit
// tested directly; effectiveRole wraps it with the org lookup.
func resolveRole(claims *auth.Claims, projectOrg, projectID string) (models.MemberRole, bool) {
	if claims == nil {
		return "", false
	}
	if isGod(claims) {
		return models.MemberRoleOwner, true
	}
	org := models.MemberRole(claims.OrgRole(projectOrg))
	proj := models.MemberRole(claims.ProjectRole(projectID))

	if proj != "" {
		return models.MaxRole(org, proj), true
	}
	if org == models.MemberRoleOwner || org == models.MemberRoleAdmin {
		return org, true
	}
	if isPlatformAnalyst(claims) {
		return models.MemberRoleReadOnly, true
	}
	return "", false
}

// effectiveRole resolves the caller's role in a project from native JWT claims
// (ADR-009). It looks up the project's owning org locally (one cheap read, also
// the tenant-isolation check) and applies resolveRole.
//
// Returns pgx.ErrNoRows when the caller has no access or the project does not
// exist — preserving the contract the handlers already branch on.
func (h *Handler) effectiveRole(ctx context.Context, claims *auth.Claims, projectID uuid.UUID) (models.MemberRole, error) {
	if claims == nil {
		return "", pgx.ErrNoRows
	}
	if isGod(claims) {
		return models.MemberRoleOwner, nil
	}

	org, err := h.projectOrg(ctx, projectID)
	if err != nil {
		return "", err // pgx.ErrNoRows when the project row is absent
	}

	role, ok := resolveRole(claims, org, projectID.String())

	if isAgent(claims) {
		granted, found, gerr := h.agentGrantRole(ctx, claims.UserID, projectID)
		if gerr != nil {
			return "", gerr
		}
		if found {
			role, ok = models.MaxRole(role, granted), true
		}
	}

	if !ok {
		return "", pgx.ErrNoRows
	}
	return role, nil
}

// agentGrantRole returns the role a live agent_project_grants row (migration
// 128) gives this identity on the project, and whether any such row exists.
//
// A machine identity (/agents) holds no personal org and gets no org cascade, so
// this table is the only way it reaches a customer project: the token is minted
// once for the automation and scoped to one project for the length of a run,
// instead of a human token — which here carries /platform-admins, i.e. Owner on
// every project of every tenant — being handed to a pod.
//
// "Live" is both conditions: not revoked (the run's finish call) and not expired
// (the clock, for the run that dies without one). Called only from effectiveRole
// and only for /agents callers, so the whole table is inert for humans and the
// gate stays a single function.
func (h *Handler) agentGrantRole(ctx context.Context, agentUserID, projectID uuid.UUID) (models.MemberRole, bool, error) {
	if agentUserID == uuid.Nil {
		return "", false, nil
	}
	var role string
	err := h.pool.QueryRow(ctx, `
		SELECT role
		  FROM agent_project_grants
		 WHERE agent_user_id = $1
		   AND project_id = $2
		   AND revoked_at IS NULL
		   AND expires_at > now()
		 ORDER BY expires_at DESC
		 LIMIT 1`, agentUserID, projectID).Scan(&role)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return models.MemberRole(role), true, nil
}

// projectOrg returns the org_id that owns a project resource row. Returns
// pgx.ErrNoRows when the project does not exist.
func (h *Handler) projectOrg(ctx context.Context, projectID uuid.UUID) (string, error) {
	var orgID *string
	if err := h.pool.QueryRow(ctx,
		`SELECT org_id FROM projects WHERE id = $1`, projectID,
	).Scan(&orgID); err != nil {
		return "", err
	}
	if orgID == nil {
		return "", nil
	}
	return *orgID, nil
}

// adminOrgIDs returns the orgs where the caller holds an Owner/Admin role — the
// orgs whose projects they see and administer via the org-role cascade.
func adminOrgIDs(claims *auth.Claims) []string {
	if claims == nil {
		return nil
	}
	var ids []string
	for org, role := range claims.OrgRoles() {
		if isOrgAdmin(models.MemberRole(role)) {
			ids = append(ids, org)
		}
	}
	return ids
}

// claimProjectIDs returns the project UUIDs the caller has an explicit role on
// (the keys of the decoded project-role map that parse as UUIDs).
func claimProjectIDs(claims *auth.Claims) []uuid.UUID {
	if claims == nil {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(claims.ProjectRoles()))
	for pid := range claims.ProjectRoles() {
		if id, err := uuid.Parse(pid); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
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
