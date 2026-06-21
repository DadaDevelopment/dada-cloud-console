package models

import (
	"time"

	"github.com/google/uuid"
)

// MemberRole is the uniform 4-value role used at both org and project scope
// (PRD-IAM / ADR-009). dada-cloud reads it from fat JWT claims only — it no
// longer resolves roles from the database. Ordered by privilege:
//
//	Owner > Admin > Developer > ReadOnly
type MemberRole string

const (
	MemberRoleOwner     MemberRole = "Owner"
	MemberRoleAdmin     MemberRole = "Admin"
	MemberRoleDeveloper MemberRole = "Developer"
	MemberRoleReadOnly  MemberRole = "ReadOnly"
)

// RolePriority ranks roles for max-merging org and project roles. Unknown roles
// rank 0 (below ReadOnly) so a malformed claim never grants access.
func RolePriority(r MemberRole) int {
	switch r {
	case MemberRoleOwner:
		return 4
	case MemberRoleAdmin:
		return 3
	case MemberRoleDeveloper:
		return 2
	case MemberRoleReadOnly:
		return 1
	}
	return 0
}

// MaxRole returns the higher-privilege of two roles. Effective project role is
// max(org_role, projects[project_id]) — org Owner/Admin cascade into projects.
func MaxRole(a, b MemberRole) MemberRole {
	if RolePriority(a) >= RolePriority(b) {
		return a
	}
	return b
}

// User represents an authenticated platform user.
// Role is not stored on the user row; it is resolved from project_members
// for a specific project context and populated at query time.
type User struct {
	ID           uuid.UUID `json:"id"           db:"id"`
	Username     string    `json:"username"     db:"username"`
	Email        string    `json:"email"        db:"email"`
	PasswordHash string    `json:"-"            db:"password_hash"`
	DisplayName  string    `json:"display_name" db:"display_name"`
	CreatedAt    time.Time `json:"created_at"   db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"   db:"updated_at"`
}

// ProjectMember links a user to a project with a specific role.
//
// DEMOTED (ADR-009): membership is now owned by user-service and arrives via fat
// JWT claims. dada-cloud no longer reads this table for authorization; it is
// retained only as a legacy/backfill record and may be dropped once user-service
// membership is confirmed live.
type ProjectMember struct {
	ID        uuid.UUID  `json:"id"         db:"id"`
	ProjectID uuid.UUID  `json:"project_id" db:"project_id"`
	UserID    uuid.UUID  `json:"user_id"    db:"user_id"`
	Role      MemberRole `json:"role"       db:"role"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
}
