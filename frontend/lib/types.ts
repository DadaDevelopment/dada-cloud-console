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
  is_ephemeral?: boolean;
  pr_number?: number | null;
  pr_head_branch?: string | null;
  expires_at?: string | null;
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

/**
 * Per-environment cost breakdown from the OpenCost Allocation API, keyed by the
 * environment's Kubernetes namespace. Costs are in CostResponse.currency (RUB).
 */
export interface EnvCost {
  environment: string;
  namespace: string;
  cpu: number;
  ram: number;
  pv: number;
  total: number;
}

/** Project resource cost over a window (default 30d), aggregated by namespace. */
export interface CostResponse {
  window: string;
  currency: string;
  total: number;
  cpu: number;
  ram: number;
  pv: number;
  by_environment: EnvCost[];
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
  /** Set only on apps deployed from a platform starter template: when the demo reaper deletes them. */
  demo_expires_at?: string;
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

export interface DBBackupDownloadResponse {
  url: string;
  filename: string;
  expires_at: string;
}

export interface DatabaseCredentialsResponse {
  host: string;
  port: string;
  database: string;
  username: string;
  password: string;
  dsn?: string;
  external_host?: string;
  external_port?: string;
  external_dsn?: string;
}

export interface DatabaseInsights {
  collectedAt: string | null;
  stale?: boolean;
  shard?: string;
  database?: string;
  tier?: string;
  sizeBytes?: number;
  sizeLimitBytes?: number;
  growthBytes7d?: number;
  cacheHitRatio?: number | null;
  connections?: number;
  instanceStartAt?: string | null;
  quotaState?: "none" | "read-only" | "frozen";
  graceUntil?: string | null;
  warnRatio?: number;
}

/** One column the archive planner considered as the cutoff. */
export interface DatabaseArchiveColumn {
  name: string;
  type: string;
  notNull: boolean;
  indexed: boolean;
}

/** Rows in one calendar month of the chosen cutoff column. */
export interface DatabaseArchiveBucket {
  month: string;
  rows: number;
  estimated: boolean;
}

/**
 * What archiving one table would cost and free. `archivable: false` carries the
 * reason no column can serve as a cutoff, which is the sentence the console
 * shows instead of a button.
 */
export interface DatabaseArchivePlan {
  table: string;
  archivable: boolean;
  reason?: string;
  column?: DatabaseArchiveColumn;
  columns: DatabaseArchiveColumn[];
  totalRows: number;
  totalBytes: number;
  totalBytesHuman: string;
  buckets?: DatabaseArchiveBucket[];
  bucketsSampled?: boolean;
  cutoff?: string;
  cutoffRows?: number;
  cutoffBytesEstimate?: number;
  cutoffBytesEstimateHuman?: string;
  remainingRows?: number;
}

/** One archive run, queued or finished. */
export interface DatabaseArchiveRun {
  id: string;
  table: string;
  column: string;
  cutoff: string;
  phase: string;
  plannedRows: number;
  deletedRows: number;
  bytesEstimate: number;
  bytesFreed: number;
  freedHuman: string;
  s3Uri: string;
  manifest: Record<string, unknown>;
  error?: string;
  auto: boolean;
  requestedBy?: string;
  createdAt: string;
  finishedAt?: string | null;
}

export interface DatabaseArchiveRunsResponse {
  runs: DatabaseArchiveRun[];
}

export interface DatabaseTableCard {
  schema: string;
  name: string;
  totalBytes: number;
  heapBytes: number;
  indexBytes: number;
  rowsEstimate: number;
  lastAutoanalyze?: string | null;
  windowHours: number;
  growthBytes?: number;
  insertedRows?: number;
  deletedRows?: number;
  appendOnly?: boolean;
  cacheHitRatio?: number;
  bytesReadFromDisk?: number;
}

export interface DatabaseQueryStat {
  queryId: number;
  query: string;
  meanMs: number;
  calls: number;
  totalMs: number;
  share: number;
  rowsPerCall: number;
}

export interface DatabaseAdvisory {
  code: string;
  subject: string;
  severity: "info" | "warning" | "critical";
  detail: string;
  suggestedSql: string;
  evidence: Record<string, unknown>;
  firstSeenAt: string;
  lastSeenAt: string;
}

export interface DatabaseTablesResponse {
  tables: DatabaseTableCard[];
}

export interface DatabaseQueriesResponse {
  queries: DatabaseQueryStat[] | null;
  totalMs: number;
}

export interface DatabaseAdvisoriesResponse {
  advisories: DatabaseAdvisory[];
}

export interface DatabaseTableDetail extends DatabaseTableCard {
  liveRows: number;
  deadRows: number;
  lastAutovacuum?: string | null;
  collectedAt: string;
  sampleStale: boolean;
  seqScans?: number | null;
  indexScans?: number | null;
}

export interface DatabaseTableSeriesPoint {
  at: string;
  totalBytes: number;
}

export interface DatabaseTableIndex {
  name: string;
  sizeBytes: number;
  totalScans: number;
  scansInWindow: number | null;
  neverScanned: boolean;
  windowHours: number;
  isUnique: boolean;
  isPrimary: boolean;
}

export interface DatabaseTableQuery {
  queryId: number;
  query: string;
  meanMs: number;
  calls: number;
  totalMs: number;
  matchedBy: string;
}

export interface DatabaseTableDetailResponse {
  table: DatabaseTableDetail;
  series: DatabaseTableSeriesPoint[];
  indexes: DatabaseTableIndex[];
  queries: DatabaseTableQuery[];
  advisories: DatabaseAdvisory[];
}

export interface DatabaseActivityConnection {
  pid: number;
  user: string;
  applicationName: string;
  clientAddr: string;
  state: string;
  waitEventType: string;
  waitEvent: string;
  stateSeconds: number | null;
  xactSeconds: number | null;
  query: string;
}

export interface DatabaseActivitySummary {
  total: number;
  active: number;
  idle: number;
  idleInTransaction: number;
  waitingOnLock: number;
  oldestXactSeconds: number;
  truncated: boolean;
  collectedAt: string;
}

export interface DatabaseActivityResponse {
  connections: DatabaseActivityConnection[];
  summary: DatabaseActivitySummary;
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

/** One depth-2 subdirectory the volume maintenance report counted files under. */
export interface VolumeMaintenanceTopDir {
  path: string;
  files: number;
}

/**
 * Result of GET .../volume/maintenance/report. status is running while the
 * scan Job has not finished, absent when nobody has started one yet (or it
 * was already swept), and succeeded/failed once the Job is done. Only
 * succeeded carries the usage and top_dirs fields; only failed carries
 * reason/hint.
 */
export interface VolumeMaintenanceReport {
  status: "running" | "succeeded" | "failed" | "absent";
  inodes_total?: number;
  inodes_used?: number;
  inodes_free?: number;
  inodes_ratio?: number;
  bytes_total?: number;
  bytes_used?: number;
  bytes_free?: number;
  top_dirs?: VolumeMaintenanceTopDir[];
  truncated?: boolean;
  finished_at?: string;
  reason?: string;
  hint?: string;
}

/**
 * The CPU/memory envelope an app actually runs with, in Kubernetes quantity
 * notation. Always present on the read path: the backend resolves it from the
 * legacy profile name for apps the autoscaler has never resized.
 */
export interface AppResources {
  cpu_request: string;
  memory_request: string;
  cpu_limit: string;
  memory_limit: string;
}

export interface AppSummary {
  image: string;
  port: number;
  replicas: number;
  profile: string;
  resources?: AppResources;
  status: string;
  message: string;
  volume?: AppVolume;
  url?: string;
  url_status?: string;
  url_reason?: string;
  preview_url?: string;
  /**
   * Free-text override for the shell-style arguments the app's container
   * starts with. Empty or absent means the platform default baked into the
   * image's Docker CMD. See dadaBuildPipeline.groovy and the shared Helm
   * chart's `args` value (gitops-agent renderer.AppSpec.Args).
   */
  start_command?: string;
  /**
   * Present only when the backend detects that this app's git repo is also
   * connected to another app (same repo, different project) -- see
   * `lib/app-twin.ts` for how the console turns this into a banner.
   */
  twin_of?: {
    project_id: string;
    project_name: string;
    app_name: string;
    repo_full_name: string;
  };
  /**
   * Last-mile HTTP probe result (gitops-agent/internal/worker/livenessprobe.go),
   * passed through summary_json unchanged on both the single-app and the
   * app-list endpoints -- see frontend/lib/last-mile-status.ts.
   */
  http_status?: number;
  http_reason?: string;
  http_checked_at?: string;
  worker?: boolean;
}

export interface AppsResponse {
  apps: ResourceSnapshot[];
}

/** One entry of an app's persistent volume, as read live from a running pod. */
export interface AppFileEntry {
  name: string;
  kind: "file" | "dir" | "symlink" | "other";
  size: number;
  modified: number;
  mode: string;
}

/** One directory listing; `truncated` when the directory has more entries than the cap. */
export interface AppFileListResponse {
  path: string;
  entries: AppFileEntry[];
  truncated: boolean;
}

/** A text file's content, plus the mtime to echo back on save for conflict detection. */
export interface AppFileContent {
  path: string;
  content: string;
  size: number;
  modified: number;
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

/**
 * A box: an ephemeral root sandbox an agent works in. Mirrors models.Box; only
 * the fields the console renders are declared.
 */
export interface Box {
  id: string;
  project_id: string;
  environment_id: string;
  name: string;
  image: string;
  profile: string;
  region: string;
  status: string;
  error_message?: string;
  ssh_host?: string;
  ssh_port?: number;
  mcp_url?: string;
  ttl_seconds: number;
  expires_at?: string;
  last_active_at?: string;
  slept_at?: string;
  created_at: string;
}

export interface BoxesResponse {
  boxes: Box[];
}

/**
 * Connection coordinates for one box. `session.token` is present only in the
 * response that minted it — it is shown exactly once and never retrievable
 * again, because only its sha256 and prefix are stored.
 */
/** Which side of the wire a coordinate answers on: the cluster, or the internet. */
export interface BoxReach {
  scope: "cluster" | "public";
  hint: string;
}

export interface BoxConnect {
  ssh_host?: string;
  ssh_command?: string;
  ssh_reach?: BoxReach;
  mcp?: {
    url: string;
    available: boolean;
    reach?: BoxReach;
    reason: string;
    snippet: unknown;
  };
  session_endpoint?: string;
}

export interface BoxExposeResponse {
  exposure: { hostname: string; url: string; port: number };
  first_response?: { ok: boolean; status: number };
  expose_ms?: number;
}

export interface BoxSession {
  token: string;
  token_prefix: string;
  expires_at: string;
}

/** What one synchronous `box up` returned, including the measured time to ready. */
export interface BoxUpResponse {
  box: Box;
  connect: BoxConnect;
  session: BoxSession;
  ready: {
    time_to_ready_ms: number;
    pool: string;
    budget_ms: number;
  };
}

export interface BoxConnectionResponse {
  box?: { name: string; status: string };
  connect: BoxConnect;
  session?: BoxSession;
}

export interface BoxCatalogResponse {
  images: { name: string; description?: string }[];
  sizes: { name: string; description?: string }[];
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

export interface UploadSourceArchiveResponse {
  artifact_uri: string;
  detected: { framework: string; port: number };
  build: Build;
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
  /** Machine code for why the hostname is not live yet; translated via domains.hm.reason.*. */
  status_reason?: string;
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

export type DeleteImpactGroup = "domain" | "database" | "storage" | "ingress" | "certificate" | "other";
export type DeleteImpactSource = "console" | "cluster-only";

export interface DeleteImpactItem {
  kind: string;
  name: string;
  group: DeleteImpactGroup;
  source: DeleteImpactSource;
}

export interface DeleteImpactResponse {
  app?: string;
  project?: string;
  namespace: string;
  cluster_scan: boolean;
  items: DeleteImpactItem[];
  clusterOnly: number;
}

export interface MoveImpactItem {
  kind: string;
  name: string;
  group?: string;
}

export interface MoveImpactBlocker {
  kind: string;
  name: string;
  reason: string;
}

export interface MoveImpactResponse {
  app: string;
  src_project: string;
  target_project: string;
  target_env_id: string;
  target_namespace: string;
  movable: MoveImpactItem[];
  blockers: MoveImpactBlocker[];
  name_collision: boolean;
  can_move: boolean;
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
  project_display_name?: string;
  requested_by: string;
}

export interface PendingApprovalsResponse {
  approvals: PendingApproval[];
}

export interface AuditEvent {
  id: string;
  created_at: string;
  actor_email: string;
  account_kind: string;
  action: string;
  resource_kind: string;
  resource_name: string;
  project_id?: string;
  project_name: string;
  project_slug?: string;
}

export interface AuditEventsResponse {
  events: AuditEvent[];
  total: number;
  limit: number;
  offset: number;
}

export interface AuditActorFacet {
  email: string;
  account_kind: string;
  count: number;
}

export interface AuditActionFacet {
  action: string;
  count: number;
}

export interface AuditCohortFacet {
  account_kind: string;
  count: number;
}

/** One action whose operations outnumber the audit rows joined to them by operation_id. */
export interface AuditCoverageGap {
  action: string;
  operations: number;
  audited: number;
  missing: number;
}

export interface AuditCoverageResponse {
  days: number;
  gaps: AuditCoverageGap[];
  total_missing: number;
}

export interface AuditFacetsResponse {
  actors: AuditActorFacet[];
  actions: AuditActionFacet[];
  cohorts: AuditCohortFacet[];
}

export interface FeedbackItem {
  id: string;
  created_at: string;
  age_hours: number;
  email: string;
  org_id: string;
  route: string;
  message: string;
  status: string;
  project_id?: string | null;
  app_name: string;
  cloud_task_id?: string | null;
  resolution: string;
  resolved_at?: string | null;
  autofixable: boolean;
}

export interface FeedbackListResponse {
  items: FeedbackItem[];
  new_count: number;
}

export interface MyFeedbackItem {
  id: string;
  created_at: string;
  status: string;
  route: string;
  app_name: string;
  message: string;
  resolution: string;
  resolved_at?: string | null;
}

export interface MyFeedbackListResponse {
  items: MyFeedbackItem[];
}

export interface AdminOverviewUsers {
  total: number;
  new_24h: number;
  new_7d: number;
  new_30d: number;
  active_48h: number;
}

export interface AdminOverviewApps {
  total: number;
  ready: number;
  broken: number;
  no_signal: number;
  by_phase: Record<string, number>;
}

export interface AdminOverviewProjects {
  total: number;
  apps: AdminOverviewApps;
  databases: number;
}

export interface AdminOverviewBuilds {
  last_7d_success: number;
  last_7d_failed: number;
  last_7d_canceled: number;
  last_24h: number;
}

export interface AdminOverviewDomains {
  active: number;
  pending: number;
  failed: number;
  retired: number;
}

export interface AdminOverviewMoney {
  available: boolean;
  note?: string;
  currency?: string;
  days?: number;
  hardware_total?: number;
  revenue_total?: number;
  margin_total?: number;
  paid_total?: number;
  metered_total?: number;
  uncollected_total?: number;
  top_loss_makers?: AdminCostLossMaker[];
}

export interface AdminOverviewNotReadyApp {
  name: string;
  project_name: string;
  phase: string;
  reason?: string;
  owner_email: string;
}

/**
 * An app the platform holds no health signal about at all.
 *
 * broken counts only apps with a live k8s workload, so an app without one is
 * neither ready nor broken and used to vanish from the panel entirely. age_seconds
 * runs from first_seen_at, so a row re-synced seconds ago still shows its true age.
 */
export interface AdminOverviewNoSignalApp {
  name: string;
  project_name: string;
  phase: string;
  owner_email: string;
  age_seconds: number;
}

/** Whether the not-ready app list itself can be trusted right now. */
export interface AdminOverviewNotReadyFreshness {
  stale_apps: number;
  newest_sync_age_seconds: number | null;
  blind: boolean;
}

/**
 * A non-k8s resource (database, AI model, CRD) stuck out of Ready.
 *
 * kind_lag_seconds is how far this row's last sync lags behind the newest
 * sync of its own kind. unmaintained is true once that lag passes 15
 * minutes, meaning the status reconciler has provably stopped visiting this
 * row -- it is not known to be broken, updates about it simply stopped.
 */
export interface AdminOverviewNotReadyResource {
  kind: string;
  name: string;
  project_name: string;
  phase: string;
  age_seconds: number;
  kind_lag_seconds: number;
  unmaintained: boolean;
}

/** A domain hostname or apex authorization that failed or got stuck pending. */
export interface AdminOverviewDomainIssue {
  stage: "hostname" | "authorization";
  hostname: string;
  status: string;
  cert_status?: string;
  project_name: string;
  age_seconds: number;
}

/** An operation that never reached a terminal status within the reclaim window. */
export interface AdminOverviewStuckOperation {
  id: string;
  action: string;
  resource_kind: string;
  resource_name: string;
  status: string;
  project_name: string;
  age_seconds: number;
}

export interface AdminOverviewStuckOperations {
  count: number;
  oldest: AdminOverviewStuckOperation[];
}

/** An app whose most recent build failed, regardless of the app's current phase. */
export interface AdminOverviewFailedBuild {
  app_name: string;
  project_name: string;
  commit_sha: string;
  error_message?: string;
  age_seconds: number;
}

/** An app whose live route answered with something other than a healthy status. */
export interface AdminOverviewLiveUrlDeadApp {
  name: string;
  project_name: string;
  owner_email: string;
  hostname: string;
  http_status: number;
  http_reason: string;
  checked_at: string;
  external: boolean;
}

/**
 * Last-mile probe of apps with a live route: did the address itself answer.
 *
 * A green build only proves the image landed, not that the route serves
 * anything -- checked is how many apps had a route to probe, ok answered
 * healthy, dead is a proxy with no backend behind it (or no response at
 * all), app_responded is the application itself answering with a non-2xx/3xx
 * status (a 404 on the wrong path is not the same failure as a proxy with no
 * pod behind it -- see evaluateLastMile), workers is apps that serve no HTTP
 * at all and only carry a leftover domain, never_http is apps a dead-looking
 * status would otherwise condemn but which have never once answered an HTTP
 * probe in their whole life (long-poll bots that were never web apps -- the
 * backend reads this from app_url_http_seen, an observation, not from the
 * worker flag, which such an app was usually created without), and stale is
 * how many were skipped this round and carry no fresh verdict either way.
 * An app that DID once answer and has since gone dark stays in dead. A low dead count next to
 * a high stale count is not good news, it is missing information.
 *
 * dead/checked mix two owner classes: external customer apps (the product
 * signal) and our own internal apps (operator/@dada-tuda.ru staff mail, see
 * isInternalOwnerEmail in backend/internal/api/admin_overview.go). The
 * *_external/*_internal fields split those same rows by that predicate;
 * dead === dead_external + dead_internal and checked === checked_external +
 * checked_internal always hold.
 */
export interface AdminOverviewLiveUrls {
  checked: number;
  ok: number;
  dead: number;
  app_responded: number;
  workers: number;
  never_http?: number;
  stale: number;
  dead_apps: AdminOverviewLiveUrlDeadApp[];
  dead_external: number;
  dead_internal: number;
  checked_external: number;
  checked_internal: number;
}

/**
 * One unhealthy pod or workload belonging to the platform itself (gitops-agent,
 * build-agent, the console, and the rest of the service namespaces) -- not a
 * customer app.
 */
export interface AdminOverviewUnhealthyPlatformWorkload {
  namespace: string;
  kind: string;
  name: string;
  workload: string;
  phase: string;
  ready: boolean;
  restarts: number;
  reason: string;
  message: string;
  age_seconds: number;
  ready_replicas: number;
  desired_replicas: number;
}

/**
 * Health of the platform's own pods, as opposed to user apps.
 *
 * observed is the load-bearing field: false means the check itself could not
 * run (unavailable_reason carries why) and an empty unhealthy list must NOT
 * be read as "all good" -- it means nothing was looked at. checked_at is
 * when the snapshot was taken; a stale checked_at is its own form of
 * blindness even when observed is true.
 */
export interface AdminOverviewPlatformHealth {
  observed: boolean;
  unavailable_reason: string;
  checked_at: string;
  namespaces: string[];
  pods_total: number;
  workloads_total: number;
  unhealthy: AdminOverviewUnhealthyPlatformWorkload[];
}

export interface AdminOverviewDayPoint {
  date: string;
  signups: number;
  build_success: number;
  build_failed: number;
  new_apps: number;
}

export interface AdminOverviewFunnelStage {
  key: string;
  label: string;
  count: number;
}

/**
 * One signup door (users.signup_channel: "password" or a Keycloak broker
 * alias like "yandex"/"google"/"github") and how many rows landed through it
 * in the window.
 */
export interface AdminOverviewChannelCount {
  channel: string;
  count: number;
}

/**
 * Keycloak-side registration funnel: goal-reach counts from the dedicated
 * id.dada-tuda.ru Metrika counter, between "opened the form" and "submitted
 * / hit an error", plus registered which is the real user_accounts count for
 * the same window. Available is false when METRIKA_OAUTH_TOKEN is unset or
 * the Stat API call failed -- Note carries the reason, Stages are still
 * present (zeroed) so the UI can render the shape without a null check.
 *
 * Stages only sees the email/password form: a brokered (identity-provider)
 * signup redirects off that form's DOM before any goal fires, so it's
 * invisible there by construction. Channels is the Postgres-side view that
 * sees every door regardless of how the row was born, closing that gap --
 * rows older than the signup_channel column are simply absent from it.
 */
export interface AdminOverviewRegistrationFunnel {
  available: boolean;
  days: number;
  registered: number;
  stages: AdminOverviewFunnelStage[];
  channels: AdminOverviewChannelCount[];
  note?: string;
}

export interface AdminOverviewResponse {
  users: AdminOverviewUsers;
  projects: AdminOverviewProjects;
  builds: AdminOverviewBuilds;
  domains: AdminOverviewDomains;
  money: AdminOverviewMoney;
  not_ready: AdminOverviewNotReadyApp[];
  no_signal: AdminOverviewNoSignalApp[];
  not_ready_freshness: AdminOverviewNotReadyFreshness;
  not_ready_other: AdminOverviewNotReadyResource[];
  domain_issues: AdminOverviewDomainIssue[];
  stuck_operations: AdminOverviewStuckOperations;
  failed_builds: AdminOverviewFailedBuild[];
  live_urls?: AdminOverviewLiveUrls;
  platform_health?: AdminOverviewPlatformHealth;
  dynamics: AdminOverviewDayPoint[];
  dynamics_days: number;
}

export interface AdminCostResource {
  name: string;
  kind: string;
  cpu_cost: number;
  ram_cost: number;
  pv_cost: number;
  total_cost: number;
  revenue: number;
  margin: number;
  margin_pct: number;
}

export interface AdminCostProject {
  project_id: string;
  project_name: string;
  cost: number;
  revenue: number;
  margin: number;
  margin_pct: number;
  metered_rub?: number;
  resources: AdminCostResource[];
}

export interface AdminCostClient {
  client_id: string;
  client_name: string;
  cost: number;
  revenue: number;
  margin: number;
  margin_pct: number;
  plan?: string;
  plan_price_rub?: number;
  paid_rub?: number;
  metered_rub?: number;
  uncollected_rub?: number;
  projects: AdminCostProject[];
}

export interface AdminCostLossMaker {
  client_name: string;
  margin: number;
}

export interface AdminCostHardwareGroup {
  name: string;
  cluster: string;
  node_count: number;
  price_month_rub: number;
}

/** One logical database as the platform sees it, sitting on a shard. */
export interface AdminDBShardDatabase {
  datname: string;
  size_bytes: number;
  share: number;
  growth_bytes_7d: number;
  connections: number;
  project_id?: string;
  project_name?: string;
  resource?: string;
  app_ref?: string;
  tier?: string;
  critical_advisories: number;
  warning_advisories: number;
  orphan: boolean;
}

/** One managed PostgreSQL instance with what is actually inside it. */
export interface AdminDBShard {
  name: string;
  state: string;
  is_platform: boolean;
  capacity_bytes: number;
  sampled_bytes: number;
  databases: number;
  collected_at?: string;
  instance_start_at?: string;
  top: AdminDBShardDatabase[];
}

export interface AdminDBShardsResponse {
  shards: AdminDBShard[];
  window_days: number;
}

export interface AdminFunnelCohortCount {
  account_kind: string;
  count: number;
}

export interface AdminFunnelChannel {
  source: string;
  visits: number;
  users: number;
  register_opened: number;
  signup_started: number;
  registration_complete: number;
  deploy_success: number;
}

export interface AdminFunnelChannelReport {
  available: boolean;
  days: number;
  channels: AdminFunnelChannel[];
  totals: AdminFunnelChannel;
  note?: string;
}

export interface AdminFunnelResponse {
  window: string;
  excluded_kinds: string[] | null;
  signups: number;
  app_up: number;
  db_up: number;
  vm_up: number;
  box_up: number;
  s3_up: number;
  model_up: number;
  paid: number;
  paid_note?: string;
  cohort_counts: AdminFunnelCohortCount[];
  channel_funnel: AdminFunnelChannelReport;
  registration_funnel: AdminOverviewRegistrationFunnel;
}

export interface AdminCostsResponse {
  available: boolean;
  note?: string;
  days: number;
  window: string;
  currency: string;
  hardware_source?: string;
  hardware_total_cost?: number;
  hardware?: AdminCostHardwareGroup[];
  opencost_raw_total?: number;
  scale_factor?: number;
  total_cost?: number;
  total_revenue?: number;
  total_margin?: number;
  total_paid?: number;
  total_metered?: number;
  total_uncollected?: number;
  metered_since?: string;
  ledger_hours?: number;
  reconstructed_rub?: number;
  reconstructed_from?: string;
  unallocated?: AdminCostResource;
  top_loss_makers?: AdminCostLossMaker[];
  clients?: AdminCostClient[];
  platform?: AdminCostClient | null;
  agent_tokens?: AdminAgentTokenEconomics;
}

export interface AIGatewayProviderStat {
  provider: string;
  calls: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost_usd: number;
}

export interface AIGatewayProjectStat {
  project_id: string;
  project_name: string;
  calls: number;
  cost_usd: number;
}

export interface AIGatewayModelStat {
  model: string;
  calls: number;
  cost_usd: number;
}

export interface AIGatewaySourceStat {
  source: string;
  calls: number;
  cost_usd: number;
}

export interface AIGatewayUsageResponse {
  days: number;
  window: { from: string; to: string };
  currency: string;
  total_cost: number;
  total_calls: number;
  providers: AIGatewayProviderStat[];
  projects: AIGatewayProjectStat[];
  models: AIGatewayModelStat[];
  sources: AIGatewaySourceStat[];
}

export interface AdminAgentTokenEconomics {
  available: boolean;
  window_days?: number;
  tokens?: number;
  cost_usd?: number;
  cost_rub?: number;
  revenue_rub?: number;
  margin_rub?: number;
  usd_rub?: number;
  markup?: number;
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

export type GitProvider = "github" | "gitlab" | "archive";

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
  platform_access: "installation" | "anonymous" | "archive";
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
  /** `.github/workflows/*.{yml,yaml}` filenames found in the repo, omitted/empty when none. */
  ci_workflows?: string[] | null;
}

export interface Build {
  id: string;
  /** Null once the source row is gone: a build outlives the repo connection it came from (backend migration 116), and its own fields still describe it. */
  git_repo_id: string | null;
  environment_id: string;
  app_name: string;
  status: BuildStatus;
  trigger: DeployTrigger;
  commit_sha: string;
  commit_message?: string;
  /** Resolved real HEAD sha for manually triggered builds; null/absent when unresolved or the app has no git branch (uploaded archive). */
  head_sha?: string | null;
  branch: string;
  pr_number?: number;
  image_uri?: string;
  logs_ref?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
  updated_at: string;
  error_message?: string;
  fail_reason?: string;
  /** Count of consecutive builds, ending with this one, that failed with the same `fail_reason` signature. Absent on older backends and on any non-failed build. */
  repeat_count?: number;
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
  commit_message?: string;
  head_sha?: string;
  branch?: string;
  source?: "git" | "archive";
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

/**
 * Non-blocking, machine-readable note attached to a save response: the value
 * was accepted, but it looks like a bare host or fragment saved under a
 * connection-string-shaped key (DATABASE_URL, REDIS_URL, *_DSN, ...) rather
 * than a full `scheme://user:password@host:port/db` string.
 */
export interface EnvVarWarning {
  key: string;
  code: "value_is_not_a_connection_string";
  message: string;
}

export interface SetEnvVarResponse {
  env_var: EnvVar;
  operation?: Operation;
  warnings?: EnvVarWarning[];
}

export interface BulkSetEnvVarsResponse {
  env_vars: EnvVar[];
  operation?: Operation;
  warnings?: EnvVarWarning[];
}

export interface DomainsResponse {
  domains: AppDomain[];
}

/** A CI deploy token issued for an app. The plaintext token is never included here — only at creation time, via `DeployHookCreated`. */
export interface DeployHook {
  id: string;
  name: string;
  token_prefix: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}

/** Returned once, immediately after `POST .../deploy-hooks` — the only time the plaintext token is ever visible. */
export interface DeployHookCreated {
  id: string;
  name: string;
  token: string;
  token_prefix: string;
  base_url: string;
  deploy_url: string;
  created_at: string;
}

// AI Gateway (OpenAI-compatible endpoint, project-scoped BYOK) ---------------

/** A project's AI Gateway key. The plaintext is only ever present in `AIGatewayKeyCreated`. */
export interface AIGatewayKey {
  id: string;
  name: string;
  token_prefix: string;
  scopes: string;
  created_at: string;
  last_used_at?: string | null;
  revoked_at?: string | null;
}

/** Returned once, right after `POST .../ai/keys` — the only time the plaintext key exists client-side. */
export interface AIGatewayKeyCreated {
  id: string;
  name: string;
  key: string;
  token_prefix: string;
  scopes: string;
  base_url: string;
  created_at: string;
}

/** A stored BYOK provider credential, secret-free: `key_hint` is masked and unusable. */
export interface AIProviderCredential {
  provider: string;
  key_hint: string;
  api_base?: string;
  updated_at: string;
}

/** One model alias callable through the gateway. `alias` goes in the request's `model` field, not `upstream`. */
export interface AICatalogModel {
  alias: string;
  provider: string;
  kind: "chat" | "embeddings";
  upstream: string;
}

/** One upstream a project can store a credential for. `key_url` is where the customer mints that key. */
export interface AICatalogProvider {
  name: string;
  label: string;
  key_url: string;
}

export interface AICatalogResponse {
  base_url: string;
  models: AICatalogModel[];
  providers: AICatalogProvider[];
}

export interface AIUsageModelStat {
  model: string;
  calls: number;
  cost_usd: number;
}

export interface AIUsageDayStat {
  day: string;
  calls: number;
  cost_usd: number;
}

/**
 * One app's share of a project's gateway spend, resolved through the
 * ServiceIdentity that paid (ADR-021). Only calls made with an app's own
 * identity token appear, so the rows sum to at most `total_cost`.
 */
export interface AIUsageAppStat {
  identity_id: string;
  app_name: string;
  calls: number;
  cost_usd: number;
}

/** A project's own AI Gateway consumption over the trailing window. Costs are the provider's, in USD. */
export interface ProjectAIUsage {
  days: number;
  currency: string;
  total_calls: number;
  total_cost: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_billed: number;
  routed_calls: number;
  routing_mode: AIRoutingMode;
  models: AIUsageModelStat[];
  daily: AIUsageDayStat[];
  apps: AIUsageAppStat[];
}

/** Whose provider key a project's AI calls go out on. */
export type AIRoutingMode = "byok" | "platform";

export interface AIRoutingSettings {
  mode: AIRoutingMode;
  markup: number;
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

export interface AppDiagnosis {
  reason: string;
  diagnosis: string;
  log_excerpt: string[];
  can_autofix: boolean;
  generated_at: string;
}

/**
 * One narrow, backend-eligibility-checked offer to make up for a platform
 * bug that broke the user's own action: GET /api/v1/recovery-prompt (see
 * backend/internal/api/platform_recovery.go). The frontend renders this
 * verbatim and never re-derives eligibility -- the backend already confirmed
 * the failure predates the fix, the user has not recovered on their own, and
 * (for the install kind) the user still has zero apps.
 */
export type RecoveryPromptKind = "solution_install_env_failed" | "payment_recurring_forbidden";

export interface RecoveryPrompt {
  kind: RecoveryPromptKind;
  failed_at: string;
  fixed_at: string;
  project_id: string;
  environment_id: string;
  resource_name: string;
}

export interface RecoveryPromptResponse {
  prompt: RecoveryPrompt | null;
}

/**
 * One entry of the ready-made project catalog: a public open-source repository
 * the console can build and deploy in one click, plus the build spec verified
 * for it. Mirrors the backend's `internal/solutions` catalog.
 */
/** One value an entry asks for before it can start (a password, an API base). */
export interface SolutionParam {
  key: string;
  label: string;
  help: string;
  kind: "text" | "secret" | "select";
  required: boolean;
  default: string;
  options: string[] | null;
  placeholder: string;
}

/** The persistent data directory an image entry mounts. */
export interface SolutionVolume {
  path: string;
  size: string;
}

/** A catalog group and the heading the console draws above it. */
export interface SolutionCategory {
  key: string;
  title: string;
}

export interface Solution {
  slug: string;
  name: string;
  tagline: string;
  /** Owner's GitHub picture, same source the resolver puts on its rows. */
  icon: string;
  /**
   * Which track this entry takes: "repo" is built from source by our pipeline,
   * "image" runs a published image and skips the build entirely. The two are
   * not the same promise, so the card says which one it is.
   */
  source: "repo" | "image";
  /** Set only for `source: "image"`: the pinned image the app runs. */
  image: string;
  /** Set only for image entries that keep state on disk. */
  volume: SolutionVolume | null;
  /**
   * The environment runtimes this entry can be installed into. Most entries
   * name both; a game server names "vm" alone, because its port is reachable
   * only where the machine publishes it directly.
   */
  runtimes: string[];
  params: SolutionParam[] | null;
  about: string;
  bullets: string[] | null;
  category: string;
  homepage: string;
  license: string;
  repo: string;
  branch: string;
  root_dir: string;
  /** Build framework override; "dockerfile" means build the repo's own Dockerfile. */
  framework: string;
  port: number;
  profile: string;
  warning: string;
  first_run: string;
  /** What to expect from the build itself (a real repo takes longer than a starter). */
  build_note: string;
}

/**
 * One row of the resolver's answer to whatever the customer typed in the single
 * "what do you want to run?" field.
 *
 * `kind` is what the row installs, and the console needs it because the three
 * kinds go down different paths: `solution` and `repo` link a repository and
 * build it, while `managed` is a database the platform runs. Rows with
 * `from: "search"` came from GitHub rather than from our catalog, so they carry
 * stars and a licence instead of a verified build spec — `framework`, `port`
 * and `profile` are empty for them on purpose, and the pipeline detects.
 */
export interface SolutionCandidate {
  kind: "solution" | "repo" | "managed";
  slug: string;
  name: string;
  tagline: string;
  icon: string;
  repo: string;
  branch: string;
  root_dir: string;
  framework: string;
  port: number;
  profile: string;
  engine: string;
  stars?: number;
  license?: string;
  archived?: boolean;
  homepage?: string;
  from?: string;
}

export interface ResolveSolutionsResponse {
  query: string;
  candidates: SolutionCandidate[];
  /** Whether the query fell through to GitHub search at all. */
  searched: boolean;
  /** Search was attempted and failed; the local rows are still trustworthy. */
  search_failed: boolean;
}

export interface InstallSolutionResponse {
  app_name: string;
  /** Present on the build track only: an image install has nothing to build. */
  build?: Build;
  /** Present on the image track: the queued app-creation operation. */
  operation?: Operation;
  default_hostname?: string;
  source?: "repo" | "image";
  /** Present only when the install also ordered a managed database. */
  database?: Operation | null;
  installed: boolean;
}
