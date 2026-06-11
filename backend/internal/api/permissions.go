package api

import (
	"context"
	"strings"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// roleFromGroups resolves the role for projectSlug from KC bearer group paths.
// Returns "" if not found. No DB access — pure function.
//
// Expected group paths (from Group Membership mapper, full.path=true):
//   /platform-admins          → MemberRolePlatformAdmin (all projects)
//   /projects/<slug>/<role>   → role for that project
func roleFromGroups(groups []string, projectSlug string) models.MemberRole {
	for _, g := range groups {
		if g == "/platform-admins" {
			return models.MemberRolePlatformAdmin
		}
	}
	prefix := "/projects/" + projectSlug + "/"
	for _, g := range groups {
		if strings.HasPrefix(g, prefix) {
			role := models.MemberRole(strings.TrimPrefix(g, prefix))
			switch role {
			case models.MemberRolePlatformAdmin, models.MemberRoleDeveloper,
				models.MemberRoleClientAdmin, models.MemberRoleClientViewer:
				return role
			}
		}
	}
	return ""
}

// getUserProjectRole returns the user's role in a project, or pgx.ErrNoRows if not a member.
//
// Dual-read: when groups is non-empty (Keycloak mode) the role is derived from
// group paths (/projects/<slug>/<role>) and /platform-admins carried in the
// bearer token — no project_members DB hit. Falls back to project_members when
// groups is empty (local HS256 mode or token has no group claim).
func (h *Handler) getUserProjectRole(ctx context.Context, userID, projectID uuid.UUID, groups []string) (models.MemberRole, error) {
	if len(groups) > 0 {
		var slug string
		if err := h.pool.QueryRow(ctx, "SELECT name FROM projects WHERE id = $1", projectID).Scan(&slug); err == nil {
			if role := roleFromGroups(groups, slug); role != "" {
				return role, nil
			}
		}
		return "", pgx.ErrNoRows
	}

	// Fallback: project_members (local HS256 mode).
	var role models.MemberRole
	err := h.pool.QueryRow(ctx,
		"SELECT role FROM project_members WHERE user_id = $1 AND project_id = $2",
		userID, projectID,
	).Scan(&role)
	if err == pgx.ErrNoRows {
		return "", pgx.ErrNoRows
	}
	return role, err
}

// canWrite returns true for roles that can create/modify resources.
func canWrite(role models.MemberRole) bool {
	return role == models.MemberRolePlatformAdmin ||
		role == models.MemberRoleDeveloper ||
		role == models.MemberRoleClientAdmin
}
