package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// OperationStatus represents the lifecycle state of an async platform operation.
type OperationStatus string

const (
	OperationStatusCreated            OperationStatus = "Created"
	OperationStatusValidated          OperationStatus = "Validated"
	OperationStatusQueued             OperationStatus = "Queued"
	OperationStatusRendering          OperationStatus = "Rendering"
	OperationStatusCommittingToGit    OperationStatus = "CommittingToGit"
	OperationStatusCommitted          OperationStatus = "Committed"
	OperationStatusWaitingForArgoSync OperationStatus = "WaitingForArgoSync"
	OperationStatusSyncing            OperationStatus = "Syncing"
	OperationStatusReconciling        OperationStatus = "Reconciling"
	OperationStatusReady              OperationStatus = "Ready"
	OperationStatusFailed             OperationStatus = "Failed"
	OperationStatusCancelled          OperationStatus = "Cancelled"
	OperationStatusWaitingForApproval OperationStatus = "WaitingForApproval"
)

// CreateServiceDatabasePayload is the typed payload for CreateServiceDatabase operations.
type CreateServiceDatabasePayload struct {
	Name            string `json:"name"`
	Database        string `json:"database"`
	AppRef          string `json:"app_ref"`
	BackupEnabled   bool   `json:"backup_enabled"`
	BackupSchedule  string `json:"backup_schedule"`
	BackupRetention string `json:"backup_retention"`
}

// CreateAppPayload is the typed payload for CreateApp operations.
// K8s fields: Replicas, Profile. VM fields: AppServerName, EnvVars.
type CreateAppPayload struct {
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Port          int               `json:"port"`
	Replicas      int               `json:"replicas,omitempty"`
	Profile       string            `json:"profile,omitempty"`
	AppServerName string            `json:"app_server_name,omitempty"`
	EnvVars       map[string]string `json:"env_vars,omitempty"`
}

// DeployImageVersionPayload is the typed payload for DeployImageVersion operations.
type DeployImageVersionPayload struct {
	AppName string `json:"app_name"`
	Image   string `json:"image"`
}

// CreateAppServerPayload is the typed payload for CreateAppServer operations.
//
// Mode selects the provisioning path:
//   - "terraform" (default): we provision a VM via Terraform, then bootstrap it.
//     Uses Flavor/OSImage/Region/SSHKeyName.
//   - "manual": a pre-existing VM is connected over SSH. Uses VMIP/SSHUser/
//     SSHPort/SSHPrivateKey. The private key is one-shot — the agent scrubs it
//     from operations.payload once the operation reaches a terminal state.
type CreateAppServerPayload struct {
	Name       string `json:"name"`
	Mode       string `json:"mode,omitempty"` // "terraform" (default) | "manual"
	Flavor     string `json:"flavor,omitempty"`
	OSImage    string `json:"os_image,omitempty"`
	Region     string `json:"region,omitempty"`
	SSHKeyName string `json:"ssh_key_name,omitempty"`

	// Manual-mode fields.
	VMIP          string `json:"vm_ip,omitempty"`
	SSHUser       string `json:"ssh_user,omitempty"`
	SSHPort       int    `json:"ssh_port,omitempty"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty"` // scrubbed after terminal state
}

// DeleteAppServerPayload is the typed payload for DeleteAppServer operations.
type DeleteAppServerPayload struct {
	AppServerName string `json:"app_server_name"`
}

// UpdateAppEnvVarsPayload is the typed payload for UpdateAppEnvVars operations (VM track only).
type UpdateAppEnvVarsPayload struct {
	AppName string            `json:"app_name"`
	EnvVars map[string]string `json:"env_vars"`
}

// CreatePublicApiPayload is the typed payload for CreatePublicApi operations.
type CreatePublicApiPayload struct {
	AppName        string   `json:"app_name"`
	PublicApiName  string   `json:"public_api_name"`
	FQDN           string   `json:"fqdn"`
	AuthEnabled    bool     `json:"auth_enabled"`
	AuthScheme     string   `json:"auth_scheme"`
	AuthScopes     []string `json:"auth_scopes,omitempty"`
	SwaggerEnabled bool     `json:"swagger_enabled"`
	SwaggerPath    string   `json:"swagger_path"`
	SwaggerTitle   string   `json:"swagger_title"`
}

// Operation represents an async, GitOps-backed platform operation.
// Field names and db tags mirror the operations table columns exactly.
type Operation struct {
	ID               uuid.UUID       `json:"id"                          db:"id"`
	ActorID          uuid.UUID       `json:"actor_id"                    db:"actor_id"`
	ProjectID        uuid.UUID       `json:"project_id"                  db:"project_id"`
	EnvironmentID    *uuid.UUID      `json:"environment_id,omitempty"    db:"environment_id"`
	Action           string          `json:"action"                      db:"action"`           // CreateServiceDatabase, CreateApp, etc.
	ResourceKind     string          `json:"resource_kind"               db:"resource_kind"`    // ServiceDatabase, App, ServiceEndpoint
	ResourceName     string          `json:"resource_name"               db:"resource_name"`
	Status           OperationStatus `json:"status"                      db:"status"`
	Payload          json.RawMessage `json:"payload"                     db:"payload"`
	ValidationResult json.RawMessage `json:"validation_result,omitempty" db:"validation_result"`
	GitCommit        string          `json:"git_commit,omitempty"        db:"git_commit"`
	GitPath          string          `json:"git_path,omitempty"          db:"git_path"`
	ArgoApplication  string          `json:"argo_application,omitempty"  db:"argo_application"`
	ErrorCode        string          `json:"error_code,omitempty"        db:"error_code"`
	ErrorMessage     string          `json:"error_message,omitempty"     db:"error_message"`
	CreatedAt        time.Time       `json:"created_at"                  db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"                  db:"updated_at"`
}
