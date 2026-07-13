// Uniform 4-role model (ADR-009). Effective project role = max(org_role,
// projects[id]). Roles are no longer client-vs-internal personas; they are a
// single ordered ladder: Owner > Admin > Developer > ReadOnly.
export type MemberRole = "Owner" | "Admin" | "Developer" | "ReadOnly";

export type OperationStatus =
  | "Created" | "Validated" | "Queued" | "Rendering"
  | "CommittingToGit" | "Committed" | "WaitingForArgoSync"
  | "Syncing" | "Reconciling" | "Ready" | "Failed"
  | "Cancelled" | "WaitingForApproval";

export interface User {
  id: string;
  username: string;
  email: string;
  display_name: string;
}

export interface Project {
  id: string;
  name: string;
  display_name: string;
  owner_type: string;
  org_id?: string; // IAM org that owns the project (ADR-009); source for org-scoped actions
  default_environment: string;
  created_at: string;
  updated_at: string;
  role?: MemberRole;
}

// ── IAM (user-service owned, ADR-009) ──────────────────────────────────────
// These mirror the user-service API surface in PRD-IAM. dada-cloud does not own
// any of this data; the console reads/writes it directly against user-service
// (see lib/userService.ts).

export interface Org {
  id: string;
  slug: string;
  display_name: string;
  role: MemberRole; // caller's role in this org
}

// A member is a principal (user OR service account) with a role in an org/project.
export interface Member {
  principal_id: string;
  principal_type: "user" | "service_account";
  email: string;
  display_name: string;
  role: MemberRole;
}

export interface Invitation {
  id: string;
  email: string;
  role: MemberRole;
  status: "pending" | "accepted" | "expired";
  created_at: string;
}

export interface Environment {
  id: string;
  project_id: string;
  name: string;
  namespace: string;
  type: "dev" | "prod";
  runtime: "k8s" | "vm";
  app_server_id?: string;
  created_at: string;
}

export type AppServerStatus =
  | "Provisioning"
  | "WaitingForAgent"
  | "Ready"
  | "Deleting"
  | "Deleted"
  | "Failed";

export type AppServerSource = "terraform" | "manual";

// Live state (Portainer proxy) ----------------------------------------------
export interface PortainerContainer {
  Id: string;
  Names: string[];
  Image?: string;
  State: string;  // "running" | "exited" | ...
  Status: string; // "Up 3 minutes" | ...
  Labels?: Record<string, string>;
}

export interface AppServerState {
  status: string;
  source?: AppServerSource;
  online: boolean;
  last_checkin?: number;
  containers?: PortainerContainer[];
  live_error?: string;
}

export interface PortainerStack {
  Id: number;
  Name: string;
  EndpointId: number;
  Status: number; // 1 active, 2 inactive
}

export interface AppState {
  online: boolean;
  stack?: PortainerStack;
  containers?: PortainerContainer[];
  live_error?: string;
}

// Metrics (central Prometheus proxy) -----------------------------------------
export interface MetricPoint {
  t: number; // unix seconds
  v: number;
}

export interface MetricsResponse {
  range: string;
  step: string;
  metrics: Record<string, { unit: string; series: MetricPoint[] }>;
  live_error?: string;
}

// Native monitoring metrics carry one or more labelled series per metric name
// (group-by/filter aware) and a counter/gauge kind so the chart can render
// rate()d counters correctly.
export interface MetricSeries {
  label: string;
  points: MetricPoint[];
}

export interface MonitoringMetricSpec {
  unit: string;
  kind?: "counter" | "gauge";
  series: MetricSeries[];
}

export interface MonitoringMetricsResponse {
  range: string;
  step: string;
  groupBy?: string;
  metrics: Record<string, MonitoringMetricSpec>;
  live_error?: string;
}

export interface MonitoringLabelsResponse {
  labels: Record<string, string[]>;
  names: string[];
}

// Aggregated logs (Elasticsearch/filebeat proxy) -----------------------------
export interface LogEntry {
  timestamp: string;
  message: string;
  vm_name?: string;
  app?: string;
  stream?: string;
}

export interface LogSearchResponse {
  total: number;
  entries: LogEntry[];
}

export interface AppServer {
  id: string;
  project_id: string;
  name: string;
  source: AppServerSource;
  vm_ip?: string;
  vm_provider_id?: string;
  terraform_workspace?: string;
  portainer_endpoint_id?: number;
  status: AppServerStatus;
  error_message?: string;
  created_at: string;
  updated_at: string;
}

export interface Operation {
  id: string;
  actor_id: string;
  project_id: string;
  environment_id?: string;
  action: string;
  resource_kind: string;
  resource_name: string;
  status: OperationStatus;
  payload?: Record<string, unknown>;
  git_commit?: string;
  git_path?: string;
  argo_application?: string;
  error_code?: string;
  error_message?: string;
  validation_result?: WorkloadDiscovery | Record<string, unknown> | null;
  created_at: string;
  updated_at: string;
}

export interface WorkloadDiscoveryMount {
  type: string;
  name?: string;
  source?: string;
  destination: string;
  rw: boolean;
}

export interface WorkloadDiscoveryContainer {
  name: string;
  image: string;
  state: string;
  status: string;
  ports: string[];
  mounts: WorkloadDiscoveryMount[];
}

/** Result of a read-only DiscoverWorkload operation (Portainer docker proxy). */
export interface WorkloadDiscovery {
  endpoint_id: number;
  containers: WorkloadDiscoveryContainer[];
  external_volumes_yaml: string;
  warnings: string[];
}

/** One discovered container the user chose to adopt into the managed compose stack. */
export interface ImportServiceInput {
  container_name: string;
  service_name: string;
  image: string;
  ports: string[];
  volumes: string[];
  include: boolean;
}

/** Body of POST /app-servers/:name/import — adopt discovered workload into a managed app. */
export interface ImportRequest {
  app_name: string;
  services: ImportServiceInput[];
  env: Record<string, string>;
  ack_secrets_in_git: boolean;
}

export interface ResourceSnapshot {
  id: string;
  project_id: string;
  environment_id?: string;
  kind: string;
  name: string;
  phase?: string;
  summary_json: Record<string, unknown>;
  last_synced_at: string;
}

export interface LoginResponse {
  token: string;
  user: User;
}

export interface ProjectsResponse {
  projects: Project[];
}

export interface ProjectDetailResponse {
  project: Project;
  environments: Environment[];
  role: MemberRole;
}

export interface CreateProjectResponse {
  project_id: string;
  default_environment_id: string;
  org_id: string;
  role: MemberRole;
}

export interface DatabasesResponse {
  databases: ResourceSnapshot[];
}

export interface CreateDatabaseResponse {
  operation: Operation;
  message: string;
}

export interface DBBackup {
  id: string;
  resource_name: string;
  database_name: string;
  status: string;
  kind: string;
  size_bytes?: number;
  error_message?: string;
  created_at: string;
  expires_at?: string;
}

export interface DBBackupsResponse {
  backups: DBBackup[];
}

export interface DatabaseCredentialsResponse {
  host: string;
  port: string;
  database: string;
  username: string;
  password: string;
  external_host?: string;
  external_port?: string;
}

export interface S3BucketsResponse {
  buckets: ResourceSnapshot[];
}

export interface CreateS3BucketResponse {
  operation: Operation;
  message: string;
}

export interface S3BucketCredentialsResponse {
  endpoint: string;
  access_key: string;
  secret_key: string;
  bucket_name: string;
  ftp_host?: string;
  sftp_host?: string;
}

export interface OperationsResponse {
  operations: Operation[];
}

/** Persistent data directory (ReadWriteMany PVC) attached to a Kubernetes app. */
export interface AppVolume {
  path: string;
  size: string;
  storage_class?: string;
}

export interface AppSummary {
  image: string;
  port: number;
  replicas: number;
  profile: string;
  status: string;
  message: string;
  volume?: AppVolume;
}

export interface AppsResponse {
  apps: ResourceSnapshot[];
}

/** Generic infrastructure resources (kind='Infra'), e.g. a VM stack's pg/nginx. */
export interface InfraResponse {
  infra: ResourceSnapshot[];
}

/** summary_json shape for an Infra resource snapshot (subtype: database/proxy/…). */
export interface InfraSummary {
  image?: string;
  subtype?: string;
  service?: string;
  status?: string;
}

export interface AppServersResponse {
  app_servers: AppServer[];
}

export interface AppServerResponse {
  app_server: AppServer;
}

export interface CreateAppServerResponse {
  operation: Operation;
  message: string;
}

export interface CreateAppResponse {
  operation: Operation;
  message: string;
}

export interface DeployImageResponse {
  operation: Operation;
  message: string;
}

export interface EndpointSummary {
  app_name: string;
  fqdn: string;
  auth_enabled: boolean;
  auth_scheme: string;
  swagger_enabled: boolean;
  status: string;
  message: string;
}

export interface EndpointsResponse {
  endpoints: ResourceSnapshot[];
}

export interface CreateEndpointResponse {
  operation: Operation;
  message: string;
}

// Custom domains (Vercel-style two-level model).
// Level 1: a project proves ownership of an apex domain via a TXT challenge.
// Level 2: a hostname (apex or subdomain) under a verified apex is attached to an app.
export interface DomainChallenge {
  type: string; // "TXT"
  host: string; // _dada-verify.acme.com
  value: string; // dada-domain-verify=<token>
}

export interface DomainAuthorization {
  id: string;
  project_id: string;
  apex_domain: string;
  verification_token: string;
  status: "pending" | "verified" | "failed";
  verified_at?: string;
  last_checked_at?: string;
  error_message?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  // Convenience challenge attached by list/create/verify responses.
  challenge?: DomainChallenge;
}

export interface DomainHostname {
  id: string;
  authorization_id?: string;
  managed?: boolean;
  environment_id: string;
  app_name: string;
  hostname: string;
  record_type: "A" | "CNAME";
  status: "pending" | "active" | "failed";
  cert_status: string;
  operation_id?: string;
  created_at: string;
  updated_at: string;
}

export interface DomainAuthorizationsResponse {
  authorizations: DomainAuthorization[];
}

export interface AddDomainAuthorizationResponse {
  authorization: DomainAuthorization;
  challenge: DomainChallenge;
}

export interface VerifyDomainAuthorizationResponse {
  authorization: DomainAuthorization;
  challenge: DomainChallenge;
}

export interface HostnamesResponse {
  hostnames: DomainHostname[];
}

export interface AttachHostnameResponse {
  operation: Operation;
  hostname: DomainHostname;
  dns_record: { type: string; host: string; target: string };
  message: string;
}

export interface DetachHostnameResponse {
  operation: Operation;
  message: string;
}

/**
 * Managed DNS (NS delegation). A verified apex can be delegated to our
 * nameservers; we then serve the zone and expose a full record editor.
 */
export type ManagedZoneStatus = "awaiting_ns" | "active";

export interface ManagedZoneRecord {
  name: string;
  type: string;
  ttl: number;
  contents: string[];
}

export interface ManagedZone {
  zone: string;
  status: ManagedZoneStatus;
  nameservers: string[];
  rrsets?: ManagedZoneRecord[];
}

export interface DelegateZoneResponse {
  nameservers: string[];
  zone: string;
  status: ManagedZoneStatus;
}

export interface ZoneImportResult {
  imported: number;
  skipped: number;
}

// AI Studio (v2) ---------------------------------------------------------

export type AIModelSource = "mlflow" | "s3" | "custom";
export type AIModelAuthMode = "apikey" | "jwt" | "public";
export type AIModelType =
  | "sklearn" | "xgboost" | "lightgbm"
  | "pytorch" | "tensorflow" | "triton"
  | "huggingface" | "custom";

export interface AIModelSummary {
  profile: string;
  model_type: AIModelType;
  version: string;
  stage: string;
  artifact_uri?: string;
  container_image?: string;
  auth_mode: AIModelAuthMode;
  attached_app?: string;
  status: string;
  canary_percent?: number;
  mlflow_name?: string;
  mlflow_version?: string;
}

export interface AIModelsResponse {
  models: ResourceSnapshot[];
}

export interface AIModelDetailResponse {
  model: ResourceSnapshot;
  api_key_prefix: string | null;
}

export interface CreateAIModelRequest {
  name: string;
  model_type: AIModelType;
  source: AIModelSource;
  mlflow_name?: string;
  mlflow_version?: string;
  artifact_uri?: string;
  container_image?: string;
  profile: string;
  auth_mode: AIModelAuthMode;
  attached_app_name?: string;
  version?: string;
}

export interface OperationResponse {
  operation: Operation;
  message: string;
}

export interface ProjectQuotas {
  project_id: string;
  cpu_model_max: number;
  gpu_model_max: number;
  monthly_inference_calls: number;
  updated_at: string;
}

export interface QuotaUsageResponse {
  quotas: ProjectQuotas;
  cpu_models_in_use: number;
  gpu_models_in_use: number;
  inference_calls_month: number;
}

export interface MLflowModelVersion {
  name: string;
  version: string;
  source: string;
  run_id?: string;
  current_stage?: string;
  status?: string;
  description?: string;
}

export interface MLflowRegisteredModel {
  name: string;
  description?: string;
  last_updated_timestamp?: number;
  latest_versions?: MLflowModelVersion[];
}

export interface MLflowModelsResponse {
  models: MLflowRegisteredModel[];
  warning?: string;
}

export interface MLflowVersionsResponse {
  versions: MLflowModelVersion[];
}

export interface RevealAPIKeyResponse {
  api_key: string;
  expires_at: string;
}

export interface PendingApproval {
  operation: Operation;
  project_name: string;
  requested_by: string;
}

export interface PendingApprovalsResponse {
  approvals: PendingApproval[];
}

// Vercel-flow — Git / Build / Deploy / Env / Domain types --------------------

export type BuildStatus =
  | "queued"
  | "detecting"
  | "building"
  | "pushing"
  | "success"
  | "failed"
  | "canceled";

export type DeployTrigger =
  | "push"
  | "pr"
  | "manual"
  | "rollback"
  | "promote";

export type GitProvider = "github" | "gitlab";

export interface GitInstallation {
  id: string;
  project_id: string;
  provider: GitProvider;
  installation_id: string;
  account_login: string;
  account_type: string;
  account_avatar_url?: string;
  created_at: string;
}

// An App installation the project can bind without a reinstall (connect wizard).
export interface AvailableInstallation {
  installation_id: string;
  account_login: string;
  account_type: string;
  bound: boolean;
}

export interface GitRemoteRepo {
  full_name: string;
  clone_url: string;
  default_branch: string;
  private: boolean;
  description?: string;
  updated_at: string;
}

export interface GitRepo {
  id: string;
  project_id: string;
  environment_id: string;
  app_name: string;
  provider: GitProvider;
  installation_id?: string;
  repo_full_name: string;
  production_branch: string;
  root_dir: string;
  framework_override?: string;
  auto_deploy: boolean;
  port: number;
  replicas: number;
  profile: string;
  created_at: string;
  updated_at: string;
}

export interface FrameworkDetection {
  framework: string | null;
  package_manager: string | null;
  build_command: string | null;
  install_command: string | null;
  start_command: string | null;
  output_dir: string | null;
  port: number | null;
}

export interface Build {
  id: string;
  git_repo_id: string;
  environment_id: string;
  app_name: string;
  status: BuildStatus;
  trigger: DeployTrigger;
  commit_sha: string;
  commit_message?: string;
  branch: string;
  pr_number?: number;
  image_uri?: string;
  logs_ref?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
  updated_at: string;
}

export interface BuildLogFrame {
  type: "log" | "status" | "error" | "done";
  line?: string;
  msg?: string;
}

export interface Deployment {
  id: string;
  environment_id: string;
  app_name: string;
  build_id?: string;
  operation_id?: string;
  image_uri: string;
  trigger: DeployTrigger;
  is_current: boolean;
  commit_sha?: string;
  branch?: string;
  created_at: string;
  updated_at: string;
}

export interface EnvVar {
  id: string;
  environment_id: string;
  app_name: string;
  key: string;
  value?: string; // only present after reveal
  is_secret: boolean;
  scope: "build" | "runtime" | "both";
  created_at: string;
  updated_at: string;
}

export interface AppDomain {
  id: string;
  environment_id: string;
  app_name: string;
  fqdn: string;
  is_auto: boolean;
  cert_status: "pending" | "issued" | "error";
  created_at: string;
}

// Response wrappers
export interface GitReposResponse {
  repos: GitRepo[];
}

export interface BuildsResponse {
  builds: Build[];
}

export interface DeploymentsResponse {
  deployments: Deployment[];
}

export interface InstallationsResponse {
  installations: GitInstallation[];
}

export interface RemoteReposResponse {
  repos: GitRemoteRepo[];
}

export interface EnvVarsResponse {
  env_vars: EnvVar[];
}

export interface DomainsResponse {
  domains: AppDomain[];
}

// Monitoring (Grafana-backed observability apps) -----------------------------

export interface MonitoringApp {
  id: string;
  project_id: string;
  environment_id: string;
  name: string;
  grafana_dashboard_uid?: string;
  created_at: string;
  updated_at: string;
}

export type HealthState = "healthy" | "degraded" | "down" | "unknown";

export interface HealthStatus {
  state: HealthState;
  critical: boolean;
  last_seen: string | null;
  error_rate_15m: number;
  firing_alerts: number;
  reasons: string[];
}

export interface AlertRule {
  id: string;
  monitoring_app_id: string;
  name: string;
  metric: string;
  condition: string;
  threshold: number;
  duration: string;
  channel_id?: string;
  channel_name?: string;
  enabled: boolean;
  grafana_rule_uid?: string;
  created_at: string;
}

export interface Channel {
  id: string;
  name: string;
  type: "telegram" | "email" | "webhook";
  created_at: string;
}

export interface CloudTaskArtifact {
  file_id: string;
  name: string;
  size: number;
  kind: string;
}

export interface CloudTask {
  id: string;
  project_id: string;
  environment_id: string;
  app_name: string;
  task_type: string;
  intent_id?: string;
  workflow_id?: string;
  status: "running" | "completed" | "failed" | "canceled";
  pr_url?: string;
  artifacts: CloudTaskArtifact[];
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface CloudTasksResponse {
  cloud_tasks: CloudTask[];
}
export interface CloudTaskResponse {
  cloud_task: CloudTask;
}
export interface CreateCloudTaskResponse {
  cloud_task: CloudTask;
}
