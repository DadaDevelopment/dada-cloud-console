package models

import (
	"time"

	"github.com/google/uuid"
)

// AppServerStatus is the lifecycle state of a customer VM.
type AppServerStatus string

const (
	AppServerStatusProvisioning    AppServerStatus = "Provisioning"
	AppServerStatusWaitingForAgent AppServerStatus = "WaitingForAgent"
	AppServerStatusReady           AppServerStatus = "Ready"
	AppServerStatusDeleting        AppServerStatus = "Deleting"
	AppServerStatusDeleted         AppServerStatus = "Deleted"
	AppServerStatusFailed          AppServerStatus = "Failed"
)

// AppServerSource records how the VM was attached to the platform.
type AppServerSource string

const (
	AppServerSourceTerraform AppServerSource = "terraform" // provisioned by us via Terraform
	AppServerSourceManual    AppServerSource = "manual"    // pre-existing VM, connected over SSH
)

// AppServer represents a customer-provisioned VDS running Docker + Portainer Edge Agent.
type AppServer struct {
	ID                  uuid.UUID       `json:"id"                              db:"id"`
	ProjectID           uuid.UUID       `json:"project_id"                      db:"project_id"`
	Name                string          `json:"name"                            db:"name"`
	Source              AppServerSource `json:"source"                          db:"source"`
	VMIP                *string         `json:"vm_ip,omitempty"                 db:"vm_ip"`
	VMProviderID        *string         `json:"vm_provider_id,omitempty"        db:"vm_provider_id"`
	TerraformWorkspace  *string         `json:"terraform_workspace,omitempty"   db:"terraform_workspace"`
	PortainerEndpointID *int            `json:"portainer_endpoint_id,omitempty" db:"portainer_endpoint_id"`
	Status              AppServerStatus `json:"status"                          db:"status"`
	ErrorMessage        *string         `json:"error_message,omitempty"         db:"error_message"`
	CreatedAt           time.Time       `json:"created_at"                      db:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"                      db:"updated_at"`
}
