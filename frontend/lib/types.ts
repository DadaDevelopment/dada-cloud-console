export type MemberRole = "platform-admin" | "developer" | "client-admin" | "client-viewer";

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
  default_environment: string;
  created_at: string;
  updated_at: string;
  role?: MemberRole;
}

export interface Environment {
  id: string;
  project_id: string;
  name: string;
  namespace: string;
  type: "dev" | "prod";
  created_at: string;
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
  created_at: string;
  updated_at: string;
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

export interface DatabasesResponse {
  databases: ResourceSnapshot[];
}

export interface CreateDatabaseResponse {
  operation: Operation;
  message: string;
}

export interface OperationsResponse {
  operations: Operation[];
}

export interface AppSummary {
  image: string;
  port: number;
  replicas: number;
  profile: string;
  status: string;
  message: string;
}

export interface AppsResponse {
  apps: ResourceSnapshot[];
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
