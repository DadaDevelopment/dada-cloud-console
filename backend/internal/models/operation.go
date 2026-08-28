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
//
// Tier is the database quota class derived from the org's billing plan. It maps
// onto ServiceDatabaseV2.spec.tier, which the Crossplane composition turns into
// the role's CONNECTION LIMIT and per-role postgres parameters. Empty means the
// XRD default ("unlimited") — used for the k8s path only.
//
// Shard is the Postgres instance the database is placed on, resolved from the
// db_shards registry at create time and mapped onto ServiceDatabaseV2.spec.shard,
// which selects the provider-sql ProviderConfig of that instance. Placement is
// automatic; empty means the XRD default (the shared instance).
type CreateServiceDatabasePayload struct {
	Name            string `json:"name"`
	Database        string `json:"database"`
	AppRef          string `json:"app_ref"`
	Engine          string `json:"engine,omitempty"`
	Tier            string `json:"tier,omitempty"`
	Shard           string `json:"shard,omitempty"`
	BackupEnabled   bool   `json:"backup_enabled"`
	BackupSchedule  string `json:"backup_schedule"`
	BackupRetention string `json:"backup_retention"`
}

// SetDatabaseEnforcementPayload is the typed payload for
// SetDatabaseEnforcement operations, emitted by the storage-quota watcher and
// never by a human. Enforcement is one of none/read-only/frozen and maps onto
// ServiceDatabaseV2.spec.enforcement; the agent patches that one field into the
// manifest already in git, so no other field of the database is carried here on
// purpose — a payload that re-declared identity would silently rewrite it.
type SetDatabaseEnforcementPayload struct {
	Name        string `json:"name"`
	AppRef      string `json:"app_ref,omitempty"`
	Enforcement string `json:"enforcement"`
}

// SetDatabaseShardPayload is the typed payload for SetDatabaseShard
// operations, emitted by the move worker once a database is live on its target
// shard and never by a human. It carries only the placement: the rest of the
// database identity stays in the manifest already in git, which is the
// authoritative record of it.
type SetDatabaseShardPayload struct {
	Name   string `json:"name"`
	AppRef string `json:"app_ref,omitempty"`
	Shard  string `json:"shard"`
}

// SetDatabaseTierPayload is the typed payload for SetDatabaseTier operations,
// emitted by the tier reconciler when a database's quota tier no longer matches
// the plan its organization is on, and never by a human. Tier maps onto
// ServiceDatabaseV2.spec.tier, which fixes the connection limit and the storage
// limit the quota watcher measures against. Like the enforcement payload it
// carries no other field of the database on purpose.
type SetDatabaseTierPayload struct {
	Name   string `json:"name"`
	AppRef string `json:"app_ref,omitempty"`
	Tier   string `json:"tier"`
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

// DeleteS3BucketPayload is the typed payload for DeleteS3Bucket operations.
// AppRef names the app whose chart carries the S3Bucket CR: the bound app when
// the bucket was created with one, empty for the per-project standalone
// "s3-buckets-<project>" chart.
type DeleteS3BucketPayload struct {
	Name   string `json:"name"`
	AppRef string `json:"app_ref,omitempty"`
}

// CreateServiceCachePayload is the typed payload for CreateServiceCache
// operations: provisions a single, scoped Redis ACL user (ServiceCacheV2)
// on a shared managed-Redis instance. Mirrors CreateServiceDatabasePayload's
// shape; unlike Postgres, Redis ACL has no single "grant the whole engine"
// concept, so Profile selects one of provider-redis's named capability
// profiles (see docs/capability-profiles-addendum.md) instead of an
// implicit "give me a database".
//
// AppRef is required (unlike ServiceDatabaseV2's optional appRef): a cache
// user with no owning app has no chart to live in and no natural
// credentials-secret consumer, so the console does not offer a standalone
// cache the way it offers a standalone database.
type CreateServiceCachePayload struct {
	Name      string `json:"name"`
	AppRef    string `json:"app_ref"`
	KeyPrefix string `json:"key_prefix"`
	Profile   string `json:"profile"`
	Shard     string `json:"shard,omitempty"`
}

// DeleteServiceCachePayload is the typed payload for DeleteServiceCache
// operations. AppRef names the app whose chart carries the User CR.
type DeleteServiceCachePayload struct {
	Name   string `json:"name"`
	AppRef string `json:"app_ref"`
}

// AppVolume describes a persistent data directory for a Helm (Kubernetes) app.
// It maps directly to the workload chart's common.pvc block: a ReadWriteMany
// PersistentVolumeClaim of Size mounted at Path on every replica. RWX is the
// only access mode we expose so multi-replica apps can share one volume.
//
// FSGroup is the group id the volume is chowned to (podSecurityContext.fsGroup).
// A fresh Longhorn volume is owned by root, so an image that runs as any other
// user cannot write to the directory it was just given; zero means the image
// runs as root and needs nothing.
type AppVolume struct {
	Path         string `json:"path"`
	Size         string `json:"size"`
	StorageClass string `json:"storage_class,omitempty"`
	FSGroup      int64  `json:"fs_group,omitempty"`
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
	Worker          bool              `json:"worker,omitempty"`
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

	// ExpectedDrops are the values.yaml paths this operation MEANS to delete,
	// declared by whoever queues it. gitops-agent refuses any deploy that would
	// remove a path from an app's values.yaml, because for a hand-maintained app
	// the console's silence about a key it claims ownership of is not a decision
	// to delete it -- that is how internal/prod/telemost-bot lost eight
	// environment variables, its service port and useDotEnv while one variable
	// was being saved. Deleting a variable must still be able to remove it from
	// git, so the delete path declares its own path here and nothing else.
	ExpectedDrops []string `json:"expected_drops,omitempty"`

	// DryRun turns the operation into a question: gitops-agent renders, merges
	// and diffs the app's values.yaml, writes the resulting plan into
	// operations.validation_result and commits nothing. It exists because the
	// only way to learn that a write would delete hand-maintained configuration
	// used to be to attempt the write.
	DryRun bool `json:"dry_run,omitempty"`

	// DryRunSetKeys and DryRunUnsetKeys are the env-var keys a dry run is
	// asking about. The row is deliberately not persisted, so the render is
	// told which keys the caller means to add or remove. Only KEYS are carried:
	// operations.payload is plaintext and env values never enter it.
	DryRunSetKeys   []string `json:"dry_run_set_keys,omitempty"`
	DryRunUnsetKeys []string `json:"dry_run_unset_keys,omitempty"`
}

// AppResourceEnvelope is the CPU/memory sizing of a single app container.
// Ephemeral storage travels with it so a resize carries an app's existing
// ephemeral limit through instead of dropping it.
type AppResourceEnvelope struct {
	CPURequest       string `json:"cpu_request"`
	MemoryRequest    string `json:"memory_request"`
	CPULimit         string `json:"cpu_limit"`
	MemoryLimit      string `json:"memory_limit"`
	EphemeralRequest string `json:"ephemeral_request,omitempty"`
	EphemeralLimit   string `json:"ephemeral_limit,omitempty"`
}

// ResizeAppPayload is the typed payload for ResizeApp operations: change one
// app's resource envelope and nothing else.
//
// It exists as its own operation because a deploy cannot do this safely. A
// deploy regenerates values.yaml from the database, which for a hand-maintained
// app drops everything the database does not know about, so the agent's clobber
// guard has to refuse it. ResizeApp patches the six resource scalars inside the
// file already in git, leaving the rest byte-for-byte intact.
type ResizeAppPayload struct {
	AppName   string              `json:"app_name"`
	Resources AppResourceEnvelope `json:"resources"`
}

// UpdateAppStoragePayload is the typed payload for UpdateAppStorage operations:
// attach or resize the persistent data directory of an existing Helm app.
type UpdateAppStoragePayload struct {
	AppName string    `json:"app_name"`
	Volume  AppVolume `json:"volume"`
}

// UpdateComposeConfigPayload is the typed payload for UpdateComposeConfig
// operations: patch the desired service spec of a compose (VM) app. The worker
// merges the image/ports into the app snapshot's desired.* block (and, for an
// adopted app, its verbatim compose block, which takes precedence) then
// re-renders the environment's aggregate stack. This is the compose analogue of
// the Helm values common.* editor; the source of truth is the DB snapshot, not
// a git file.
type UpdateComposeConfigPayload struct {
	AppName string   `json:"app_name"`
	Image   string   `json:"image"`
	Ports   []string `json:"ports,omitempty"`
}

// UpdateComposeVolumePayload is the typed payload for UpdateComposeVolume
// operations: set the named-volume mounts of a compose (VM) app's service. The
// worker writes them to desired.volumes and re-renders the aggregate; the
// aggregate renderer re-derives the external-volume pins so existing data is
// preserved.
type UpdateComposeVolumePayload struct {
	AppName string   `json:"app_name"`
	Volumes []string `json:"volumes"`
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

// DeletePreviewEnvPayload is the typed payload for DeletePreviewEnv operations.
// JSON tags are a hard contract with gitops-agent's doDeletePreviewEnv worker —
// do NOT rename them. Previews are no longer a product feature; teardown stays
// because environments opened before the removal still have to be taken down.
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
	StatusReason    *string    `json:"status_reason,omitempty" db:"status_reason"`
	OperationID     *uuid.UUID `json:"operation_id,omitempty" db:"operation_id"`
	CreatedAt       time.Time  `json:"created_at"             db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"             db:"updated_at"`
}

// AttachCustomHostnamePayload is the typed payload for AttachCustomHostname
// operations. JSON tags are a hard contract with gitops-agent's
// doAttachCustomHostname worker — do NOT rename them.
// Port and HostLoopback are only meaningful on vm-runtime environments, where
// the hostname becomes an nginx vhost on the app server itself and the platform
// must know what to proxy to. Port empty means "read the target port off the
// managed app's own compose spec"; HostLoopback switches the upstream from a
// compose service to the VM's host gateway, which is how a workload the platform
// did not deploy (bound to 127.0.0.1) gets published. Both are ignored on k8s,
// where the Service name and port come from the app's own manifest.
type AttachCustomHostnamePayload struct {
	AppName      string `json:"app_name"`
	Hostname     string `json:"hostname"`
	Port         int    `json:"port,omitempty"`
	HostLoopback bool   `json:"host_loopback,omitempty"`
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

// SaveAgentPayload is the typed payload for CreateAgent and UpdateAgent.
//
// An agent is saved whole: the console has one editor and a save re-states
// every field, so the gitops-agent can upsert one ManagedAgent claim by name
// instead of merging a patch into a CR it did not write.
type SaveAgentPayload struct {
	Name          string         `json:"name"`
	DisplayName   string         `json:"display_name,omitempty"`
	Description   string         `json:"description,omitempty"`
	Prompt        string         `json:"prompt"`
	PromptVersion string         `json:"prompt_version,omitempty"`
	ModelConfig   string         `json:"model_config,omitempty"`
	Runtime       string         `json:"runtime,omitempty"`
	Namespace     string         `json:"namespace,omitempty"`
	Tools         []AgentToolRef `json:"tools,omitempty"`
	Env           []AgentEnvVar  `json:"env,omitempty"`
}

// AgentToolRef points an agent at one MCP server. URL is empty for a server
// that already exists in the runtime: the claim then references it by name
// rather than declaring a second RemoteMCPServer for the same endpoint.
//
// AllowedHeaders is what the agent is permitted to replay to that server, which
// is the only channel a caller's identity travels on.
type AgentToolRef struct {
	Name           string            `json:"name"`
	URL            string            `json:"url,omitempty"`
	Description    string            `json:"description,omitempty"`
	Timeout        string            `json:"timeout,omitempty"`
	Protocol       string            `json:"protocol,omitempty"`
	Headers        []AgentToolHeader `json:"headers,omitempty"`
	AllowedHeaders []string          `json:"allowed_headers,omitempty"`
}

// AgentToolHeader is one header the agent sends on every call to its own MCP
// server, which is how a third-party server is authorized.
//
// Value may refer to the agent's own environment as ${VAR}: the token then
// lives in one place -- the env of the agent -- and the header stays readable
// as "Bearer ${NOTION_TOKEN}" instead of being a second copy of the secret.
type AgentToolHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// AgentEnvVar is one plain environment variable of the agent runtime.
type AgentEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DeleteAgentPayload is the typed payload for DeleteAgent operations.
type DeleteAgentPayload struct {
	Name string `json:"name"`
}
