package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Project represents a tenant project / namespace boundary in the platform.
type Project struct {
	ID                 uuid.UUID       `json:"id"                   db:"id"`
	Name               string          `json:"name"                 db:"name"` // slug: internal, client-a
	DisplayName        string          `json:"display_name"         db:"display_name"`
	OwnerType          string          `json:"owner_type"           db:"owner_type"` // team | client
	OwnerID            *uuid.UUID      `json:"owner_id,omitempty"   db:"owner_id"`
	OrgID              string          `json:"org_id,omitempty"     db:"org_id"` // IAM org that owns the project (ADR-009)
	DefaultEnvironment string          `json:"default_environment"  db:"default_environment"`
	Quotas             json.RawMessage `json:"quotas"               db:"quotas"`
	CreatedAt          time.Time       `json:"created_at"           db:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"           db:"updated_at"`
}

// EnvironmentType constrains environment kinds.
type EnvironmentType string

const (
	EnvironmentTypeDev  EnvironmentType = "dev"
	EnvironmentTypeProd EnvironmentType = "prod"
)

// EnvironmentRuntime discriminates the deployment substrate for an environment.
//
// Three values, and the third one is load-bearing: a Dada Box owns exactly one
// environment row with runtime='box' (D1), and crystallization flips that SAME
// row to 'vm' rather than creating a new one, which is how a box's attached
// databases, injected env vars and hostnames survive promotion untouched.
//
// Consequence for anyone adding a runtime branch: 'box' must be handled
// explicitly, never left to a default arm. The guards in api/runtime_guard.go and
// every `runtime = 'k8s'` / `runtime == "vm"` predicate in the API were audited
// for this when 'box' was introduced (migration 061).
type EnvironmentRuntime string

const (
	EnvironmentRuntimeK8s EnvironmentRuntime = "k8s"
	EnvironmentRuntimeVM  EnvironmentRuntime = "vm"
	EnvironmentRuntimeBox EnvironmentRuntime = "box"
)

// Environment represents a deployment environment within a project (e.g. dev, prod).
type Environment struct {
	ID            uuid.UUID          `json:"id"                      db:"id"`
	ProjectID     uuid.UUID          `json:"project_id"              db:"project_id"`
	Name          string             `json:"name"                    db:"name"`      // dev, prod
	Namespace     string             `json:"namespace"               db:"namespace"` // k8s namespace: internal-prod
	Type          EnvironmentType    `json:"type"                    db:"type"`
	Runtime       EnvironmentRuntime `json:"runtime"                 db:"runtime"`
	AppServerID   *uuid.UUID         `json:"app_server_id,omitempty" db:"app_server_id"`
	LimitRange    json.RawMessage    `json:"limit_range"             db:"limit_range"`
	ResourceQuota json.RawMessage    `json:"resource_quota"          db:"resource_quota"`
	IsEphemeral   bool               `json:"is_ephemeral"            db:"is_ephemeral"`
	PRNumber      *int               `json:"pr_number,omitempty"     db:"pr_number"`
	PRHeadBranch  *string            `json:"pr_head_branch,omitempty" db:"pr_head_branch"`
	ExpiresAt     *time.Time         `json:"expires_at,omitempty"    db:"expires_at"`
	CreatedAt     time.Time          `json:"created_at"              db:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"              db:"updated_at"`
}
