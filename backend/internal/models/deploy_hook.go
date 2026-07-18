package models

import (
	"time"

	"github.com/google/uuid"
)

// AppDeployHook is a revocable bearer-token credential scoped to one app that
// lets external CI (e.g. GitHub Actions) trigger a DeployImageVersion
// operation via POST /api/v1/deploy without a Keycloak session. Mirrors the
// app_deploy_hooks table. TokenHash is never serialized -- only the plaintext
// token is ever shown to a caller, and only once, at creation time.
type AppDeployHook struct {
	ID            uuid.UUID  `json:"id"                     db:"id"`
	ProjectID     uuid.UUID  `json:"project_id"             db:"project_id"`
	EnvironmentID uuid.UUID  `json:"environment_id"         db:"environment_id"`
	AppName       string     `json:"app_name"               db:"app_name"`
	Name          string     `json:"name"                   db:"name"`
	TokenHash     string     `json:"-"                      db:"token_hash"`
	TokenPrefix   string     `json:"token_prefix"           db:"token_prefix"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty"   db:"created_by"`
	CreatedAt     time.Time  `json:"created_at"             db:"created_at"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty" db:"last_used_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"   db:"revoked_at"`
}
