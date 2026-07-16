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
// Engine is set for VM (compose) environments, where the managed database is
// rendered as a platform-owned Application in the environment's aggregate stack
// (postgres) rather than a Crossplane ServiceDatabaseV2 CRD; empty means the k8s
// (Crossplane Postgres) path.
type CreateServiceDatabasePayload struct {
	Name            string `json:"name"`
	Database        string `json:"database"`
	AppRef          string `json:"app_ref"`
	Engine          string `json:"engine,omitempty"`
	BackupEnabled   bool   `json:"backup_enabled"`
	BackupSchedule  string `json:"backup_schedule"`
	BackupRetention string `json:"backup_retention"`
	ExternalEnabled bool   `json:"external_enabled,omitempty"`
}

// DeleteServiceDatabasePayload is the typed payload for DeleteServiceDatabase
// operations. AppRef is the owning app whose resources.values.yaml holds the
// CR entry (empty = the standalone "service-databases-<project>" chart); the
// agent needs it to target the right values file.
type DeleteServiceDatabasePayload struct {
	Name   string `json:"name"`
	AppRef string `json:"app_ref,omitempty"`
}

// CreateS3BucketPayload is the typed payload for CreateS3Bucket operations.
// AppRef is optional: when set, the bucket is owned by that app's chart; when
// empty, it lands in the per-project standalone "s3-buckets-<project>" chart.
type CreateS3BucketPayload struct {
	Name          string `json:"name"`
	BucketName    string `json:"bucket_name"`
	Region        string `json:"region"`
	Description   string `json:"description"`
	Public        bool   `json:"public"`
	FtpSftpEnable bool   `json:"ftp_sftp_enable"`
	AppRef        string `json:"app_ref,omitempty"`
}

// AppVolume describes a persistent data directory for a Helm (Kubernetes) app.
// It maps directly to the workload chart's common.pvc block: a ReadWriteMany
// PersistentVolumeClaim of Size mounted at Path on every replica. RWX is the
// only access mode we expose so multi-replica apps can share one volume.
type AppVolume struct {
	Path         string `json:"path"`
	Size         string `json:"size"`
	StorageClass string `json:"storage_class,omitempty"`
}

// CreateAppPayload is the typed payload for CreateApp operations.
// K8s fields: Replicas, Profile, Volume. VM fields: AppServerName, EnvVars.
type CreateAppPayload struct {
	Name            string            `json:"name"`
	Image           string            `json:"image"`
	Framework       string            `json:"framework,omitempty"`
	Port            int               `json:"port"`
	Replicas        int               `json:"replicas,omitempty"`
	Profile         string            `json:"profile,omitempty"`
	Volume          *AppVolume        `json:"volume,omitempty"`
	WorkloadType    string            `json:"workload_type,omitempty"`
	AppServerName   string            `json:"app_server_name,omitempty"`
	EnvVars         map[string]string `json:"env_vars,omitempty"`
	DefaultHostname string            `json:"default_hostname,omitempty"`
}

// DeployImageVersionPayload is the typed payload for DeployImageVersion operations.
// Framework/Port are carried from build-time detection so a redeploy re-renders
// the app on the correct helm chart + servicePort; both are optional and fall
// back to the persisted App snapshot when absent.
type DeployImageVersionPayload struct {
	AppName   string `json:"app_name"`
	Image     string `json:"image"`
	Framework string `json:"framework,omitempty"`
	Port      int    `json:"port,omitempty"`
}

// UpdateAppStoragePayload is the typed payload for UpdateAppStorage operations:
// attach or resize the persistent data directory of an existing Helm app.
type UpdateAppStoragePayload struct {
	AppName string    `json:"app_name"`
	Volume  AppVolume `json:"volume"`
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

// DeleteAppPayload is the typed payload for DeleteApp operations. The worker's
// existing doDeleteApp already consumes this shape.
type DeleteAppPayload struct {
	Name string `json:"name"`
}

// DeleteProjectPayload is the typed payload for DeleteProject operations. Empty
// on purpose: the worker resolves everything it needs (slug, git tree) from
// op.ProjectID.
type DeleteProjectPayload struct{}

// CreateProjectPayload is the typed payload for CreateProject operations. Empty
// on purpose: the worker resolves the project row (name, display name, owner
// type, default environment) from op.ProjectID and renders project.yaml (+ KC
// group CRs) the same way BootstrapProjects does at agent startup, so a
// project created at runtime gets its project-defaults manifest — and the
// nexus-cred secret ArgoCD derives from it — without waiting for a restart.
type CreateProjectPayload struct{}

// MoveAppPayload is the typed payload for MoveApp operations (ADR-014 Phase 1:
// stateless move across projects). The source project_id/environment_id are the
// operation row's own columns; this payload only carries the destination. JSON
// tags are a hard contract with gitops-agent's doMoveApp worker — do NOT rename
// them.
type MoveAppPayload struct {
	AppName         string    `json:"app_name"`
	TargetProjectID uuid.UUID `json:"target_project_id"`
	TargetEnvID     uuid.UUID `json:"target_env_id"`
}

// DiscoverWorkloadPayload is the typed payload for DiscoverWorkload operations:
// a read-only inventory of an enrolled VM's running containers/volumes via the
// Portainer docker proxy (no SSH). EndpointID is the VM's Portainer endpoint.
type DiscoverWorkloadPayload struct {
	ServerName string `json:"server_name"`
	EndpointID int    `json:"endpoint_id"`
}

// ImportServiceSpec is one discovered container the caller has chosen to adopt
// into the imported compose app. Mirrors the shape DiscoverWorkload already
// returns on validation_result (discoveredContainer), plus Include/ServiceName
// which only make sense in the import direction.
type ImportServiceSpec struct {
	ContainerName string   `json:"container_name"`
	ServiceName   string   `json:"service_name"`
	Image         string   `json:"image"`
	Ports         []string `json:"ports,omitempty"`
	Volumes       []string `json:"volumes,omitempty"`
	Include       bool     `json:"include"`
}

// ImportComposeStackPayload is the typed payload for ImportComposeStack
// operations: adopts a discovered VM workload into a managed compose App —
// render compose.yaml + .env from the included services, commit to git, then
// deploy via the existing DeployStack chain. EnvVars are written verbatim into
// the app's .env (same plaintext-in-git contract as RenderEnvFile); the
// ack_secrets_in_git consent gate is enforced by the API handler, not here.
type ImportComposeStackPayload struct {
	AppName         string              `json:"app_name"`
	ServerName      string              `json:"server_name"`
	Services        []ImportServiceSpec `json:"services"`
	EnvVars         map[string]string   `json:"env_vars,omitempty"`
	AckSecretsInGit bool                `json:"ack_secrets_in_git"`
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

// CreatePreviewEnvPayload is the typed payload for CreatePreviewEnv operations.
// JSON tags are a hard contract with gitops-agent's doCreatePreviewEnv worker —
// do NOT rename them.
type CreatePreviewEnvPayload struct {
	EnvName     string `json:"env_name"`
	Namespace   string `json:"namespace"`
	GitRepoID   string `json:"git_repo_id"`
	PRNumber    int    `json:"pr_number"`
	HeadBranch  string `json:"head_branch"`
	ParentEnvID string `json:"parent_env_id"`
}

// DeletePreviewEnvPayload is the typed payload for DeletePreviewEnv operations.
// JSON tags are a hard contract with gitops-agent's doDeletePreviewEnv worker —
// do NOT rename them.
type DeletePreviewEnvPayload struct {
	EnvironmentID string `json:"environment_id"`
	Namespace     string `json:"namespace"`
}

// DomainAuthorization is Level 1 of the custom-domain model: a project's proven
// ownership of an apex domain via a TXT challenge. Once Status=verified, the
// project may attach the apex and any subdomain to its deployments. Mirrors the
// domain_authorizations table.
type DomainAuthorization struct {
	ID                uuid.UUID  `json:"id"                        db:"id"`
	ProjectID         uuid.UUID  `json:"project_id"                db:"project_id"`
	ApexDomain        string     `json:"apex_domain"               db:"apex_domain"`
	VerificationToken string     `json:"verification_token"        db:"verification_token"`
	Status            string     `json:"status"                    db:"status"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"     db:"verified_at"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty" db:"last_checked_at"`
	ErrorMessage      string     `json:"error_message,omitempty"   db:"error_message"`
	CreatedBy         uuid.UUID  `json:"created_by"                db:"created_by"`
	CreatedAt         time.Time  `json:"created_at"                db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"                db:"updated_at"`
}

// DomainHostname is Level 2: a specific hostname (apex or subdomain) attached to
// one app/environment, routed by a native Ingress + cert-manager cert. Mirrors
// the domain_hostnames table.
type DomainHostname struct {
	ID              uuid.UUID  `json:"id"                     db:"id"`
	AuthorizationID *uuid.UUID `json:"authorization_id,omitempty" db:"authorization_id"`
	Managed         bool       `json:"managed"                db:"managed"`
	EnvironmentID   uuid.UUID  `json:"environment_id"         db:"environment_id"`
	AppName         string     `json:"app_name"               db:"app_name"`
	Hostname        string     `json:"hostname"               db:"hostname"`
	RecordType      string     `json:"record_type"            db:"record_type"`
	Status          string     `json:"status"                 db:"status"`
	CertStatus      string     `json:"cert_status"            db:"cert_status"`
	OperationID     *uuid.UUID `json:"operation_id,omitempty" db:"operation_id"`
	CreatedAt       time.Time  `json:"created_at"             db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"             db:"updated_at"`
}

// AttachCustomHostnamePayload is the typed payload for AttachCustomHostname
// operations. JSON tags are a hard contract with gitops-agent's
// doAttachCustomHostname worker — do NOT rename them.
type AttachCustomHostnamePayload struct {
	AppName  string `json:"app_name"`
	Hostname string `json:"hostname"`
}

// DetachCustomHostnamePayload is the typed payload for DetachCustomHostname
// operations. It removes the {Ingress, <host-as-name>} entry from the owning
// app's resources.values.yaml manifests list.
type DetachCustomHostnamePayload struct {
	AppName  string `json:"app_name"`
	Hostname string `json:"hostname"`
}

// Operation represents an async, GitOps-backed platform operation.
// Field names and db tags mirror the operations table columns exactly.
type Operation struct {
	ID               uuid.UUID       `json:"id"                          db:"id"`
	ActorID          uuid.UUID       `json:"actor_id"                    db:"actor_id"`
	ProjectID        uuid.UUID       `json:"project_id"                  db:"project_id"`
	EnvironmentID    *uuid.UUID      `json:"environment_id,omitempty"    db:"environment_id"`
	Action           string          `json:"action"                      db:"action"`        // CreateServiceDatabase, CreateApp, etc.
	ResourceKind     string          `json:"resource_kind"               db:"resource_kind"` // ServiceDatabase, App, ServiceEndpoint
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
