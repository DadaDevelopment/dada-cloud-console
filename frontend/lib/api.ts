// API client — fetch wrapper that attaches the JWT Authorization header.

import type {
  LoginResponse,
  User,
  ProjectsResponse,
  ProjectDetailResponse,
  CreateProjectResponse,
  OperationsResponse,
  Operation,
  DatabasesResponse,
  CreateDatabaseResponse,
  DBBackup,
  DBBackupsResponse,
  DBBackupDownloadResponse,
  S3BucketsResponse,
  CreateS3BucketResponse,
  S3BucketCredentialsResponse,
  DatabaseCredentialsResponse,
  DatabaseInsights,
  DatabaseTablesResponse,
  DatabaseTableDetailResponse,
  DatabaseActivityResponse,
  DatabaseQueriesResponse,
  DatabaseAdvisoriesResponse,
  DatabaseArchivePlan,
  DatabaseArchiveRun,
  DatabaseArchiveRunsResponse,
  AppsResponse,
  AppFileListResponse,
  AppFileContent,
  UploadSourceArchiveResponse,
  InfraResponse,
  AppServersResponse,
  CostResponse,
  AppServerResponse,
  CreateAppServerResponse,
  CreateAppResponse,
  DeployImageResponse,
  EndpointsResponse,
  CreateEndpointResponse,
  DomainAuthorizationsResponse,
  AddDomainAuthorizationResponse,
  VerifyDomainAuthorizationResponse,
  HostnamesResponse,
  AttachHostnameResponse,
  DetachHostnameResponse,
  ManagedZone,
  ManagedZoneRecord,
  DelegateZoneResponse,
  ZoneImportResult,
  AIModelsResponse,
  AIModelDetailResponse,
  CreateAIModelRequest,
  OperationResponse,
  QuotaUsageResponse,
  MLflowModelsResponse,
  MLflowVersionsResponse,
  MLflowModelVersion,
  RevealAPIKeyResponse,
  PendingApprovalsResponse,
  MyFeedbackListResponse,
  AuditEventsResponse,
  AuditCoverageResponse,
  AuditFacetsResponse,
  FeedbackListResponse,
  AdminOverviewResponse,
  AdminCostsResponse,
  AdminDBShardsResponse,
  AIGatewayUsageResponse,
  AppState,
  AppServerState,
  ImportRequest,
  MetricsResponse,
  LogSearchResponse,
  // Vercel-flow
  GitReposResponse,
  BuildsResponse,
  DeploymentsResponse,
  InstallationsResponse,
  GitInstallation,
  AvailableInstallation,
  RemoteReposResponse,
  EnvVarsResponse,
  SetEnvVarResponse,
  BulkSetEnvVarsResponse,
  DomainsResponse,
  Build,
  FrameworkDetection,
  Solution,
  ResolveSolutionsResponse,
  SolutionCategory,
  InstallSolutionResponse,
  // Monitoring
  MonitoringApp,
  HealthStatus,
  AlertRule,
  Channel,
  MonitoringMetricsResponse,
  MonitoringLabelsResponse,
  CloudTasksResponse,
  CloudTaskResponse,
  CreateCloudTaskResponse,
  AppDiagnosis,
  DeleteImpactResponse,
  MoveImpactResponse,
  DeployHook,
  DeployHookCreated,
  AIGatewayKey,
  AIGatewayKeyCreated,
  AIProviderCredential,
  AICatalogResponse,
  ProjectAIUsage,
  AIRoutingMode,
  AIRoutingSettings,
  BoxesResponse,
  BoxUpResponse,
  BoxConnectionResponse,
  BoxExposeResponse,
  BoxCatalogResponse,
} from "./types";
import type { OnboardingStatus } from "./onboarding/types";

// Empty string → relative URLs → requests go through the ingress proxy.
// Override with NEXT_PUBLIC_API_URL at build time only for non-prod targets.
export const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL ?? "";

type TokenGetter = () => Promise<string | null>;
let _tokenGetter: TokenGetter | null = null;

export function setTokenGetter(fn: TokenGetter | null): void {
  _tokenGetter = fn;
}

export async function getToken(): Promise<string | null> {
  if (_tokenGetter) return _tokenGetter();
  if (typeof window === "undefined") return null;
  return localStorage.getItem("dada_token");
}

/**
 * `body` is the payload as an object, never a pre-serialized string: apiFetch
 * owns the JSON.stringify. Typing it `object` is the gate — a caller that
 * stringifies first now fails tsc instead of shipping a JSON string as the
 * whole request body, which the Go handlers reject with
 * "cannot unmarshal string into Go value of type ...".
 */
type RequestOptions = {
  method?: string;
  body?: object;
  token?: string;
  // Override the base URL. user-service endpoints (orgs/members/invitations)
  // sit at the gateway root, not under dada-cloud's /api/v1 — see lib/userService.ts.
  baseUrl?: string;
  timeoutMs?: number;
};

function raceAbort<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  if (signal.aborted) {
    return Promise.reject(new DOMException("Aborted", "AbortError"));
  }
  return new Promise<T>((resolve, reject) => {
    const onAbort = () => reject(new DOMException("Aborted", "AbortError"));
    signal.addEventListener("abort", onAbort, { once: true });
    promise.then(resolve, reject).finally(() => signal.removeEventListener("abort", onAbort));
  });
}

/**
 * Perform an authenticated API call.
 *
 * On a non-2xx response the thrown Error carries `status`, plus `code` (the
 * backend's machine-readable `error` field, e.g. "quota_exceeded") and, when
 * the failure is a plan limit, `upgrade` plus the `resource` and `limit` that
 * were hit -- enough for the caller to render an upsell for the exact thing
 * the user was refused. Also carries `provisioningSince` (from the backend's
 * `provisioning_since`), the ISO timestamp of when a still-provisioning
 * resource's creation actually started, for callers whose only other proof
 * is a tab-local timer that resets on reload. The Error's message prefers the
 * backend's human `message` over the code, so callers that just render
 * `err.message` show a sentence rather than a raw error code.
 */
export async function apiFetch<T>(
  path: string,
  options: RequestOptions = {}
): Promise<T> {
  const { method = "GET", body, token, baseUrl, timeoutMs = 30_000 } = options;

  // Hard timeout so a hung request (e.g. a stuck SSO silent-refresh that never
  // resolves the bearer token) surfaces as an error instead of an infinite
  // spinner. 30s is well above any healthy API call. Covers getToken() too --
  // it is the exact "stuck SSO silent-refresh" case this guard is for, and
  // fetch()'s AbortSignal only takes effect once fetch() itself is called.
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  let res: Response;
  try {
    const bearerToken = token ?? await raceAbort(getToken(), controller.signal);

    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };

    if (bearerToken) {
      headers["Authorization"] = `Bearer ${bearerToken}`;
    }

    res = await fetch(`${baseUrl ?? API_BASE_URL}${path}`, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: controller.signal,
    });
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") {
      throw new Error("Request timed out. Try again.");
    }
    throw e;
  } finally {
    clearTimeout(timeout);
  }

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    const body = err as {
      error?: string;
      code?: string;
      message?: string;
      upgrade?: boolean;
      resource?: string;
      limit?: number;
      provisioning_since?: string;
    };
    const apiError = new Error(body.message ?? body.error ?? "API error") as Error & {
      status?: number;
      code?: string;
      upgrade?: boolean;
      resource?: string;
      limit?: number;
      provisioningSince?: string;
    };
    apiError.status = res.status;
    apiError.code = body.code ?? body.error;
    apiError.upgrade = body.upgrade;
    apiError.resource = body.resource;
    apiError.limit = body.limit;
    apiError.provisioningSince = body.provisioning_since;
    throw apiError;
  }

  // 204 / empty body (e.g. DELETE) → nothing to parse.
  if (res.status === 204 || res.headers.get("content-length") === "0") {
    return undefined as T;
  }

  return res.json() as Promise<T>;
}

/**
 * Classifies a 409 from ConnectGitRepo (backend/internal/api/gitrepos.go) into
 * which of the two known conflicts happened, so callers can show the right
 * copy instead of the raw backend sentence.
 *
 * `repo_already_connected`: this exact repository is already linked to that
 * app - the caller's previous attempt succeeded and this is a retry.
 * `app_name_taken` (or any other/missing code, for rollout compatibility with
 * a backend that has not deployed `code` yet): the app name collides with a
 * different repository.
 */
export type ConnectRepoConflict = "repo_already_connected" | "app_name_taken" | null;

export function classifyConnectRepoConflict(status: number | undefined, code: string | undefined): ConnectRepoConflict {
  if (status !== 409) return null;
  return code === "repo_already_connected" ? "repo_already_connected" : "app_name_taken";
}

/**
 * True when ConnectGitRepo rejected the request with a 400 and
 * `code: "github_access_required"`: the repository is private (or does not
 * exist for us) and the platform holds neither a GitHub App installation nor
 * a token for it, so the row would never build. Match strictly on status +
 * code, never on the error prose.
 */
export function isGithubAccessRequiredError(status: number | undefined, code: string | undefined): boolean {
  return status === 400 && code === "github_access_required";
}

/**
 * True when the backend rejected an authenticated request because the
 * caller's identity is new and self-serve registration is currently closed
 * (`SIGNUP_ENABLED=false`) - see the backend's `respondErrorCode` call with
 * `code: "signup_closed"`. Existing users resolve normally and never hit
 * this; match strictly on status + code, never on the error prose.
 */
export function isSignupClosedError(status: number | undefined, code: string | undefined): boolean {
  return status === 403 && code === "signup_closed";
}

const UPLOAD_TIMEOUT_MS = 10 * 60 * 1000;

/**
 * Multipart upload with progress reporting. fetch() has no reliable upload-progress
 * event, so this goes through XMLHttpRequest instead. Same bearer auth as apiFetch,
 * but no Content-Type header is set — the browser fills in the multipart boundary.
 * Timeout is a full 10 minutes (vs apiFetch's 30s) since a 100MB archive over a slow
 * uplink legitimately takes a while.
 */
export function apiUpload<T>(
  path: string,
  formData: FormData,
  onProgress?: (percent: number) => void
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    getToken().then((bearerToken) => {
      const xhr = new XMLHttpRequest();
      xhr.open("POST", `${API_BASE_URL}${path}`);
      xhr.timeout = UPLOAD_TIMEOUT_MS;
      if (bearerToken) {
        xhr.setRequestHeader("Authorization", `Bearer ${bearerToken}`);
      }
      if (onProgress) {
        xhr.upload.onprogress = (e) => {
          if (e.lengthComputable) {
            onProgress(Math.round((e.loaded / e.total) * 100));
          }
        };
      }
      xhr.onload = () => {
        let body: unknown;
        try {
          body = xhr.responseText ? JSON.parse(xhr.responseText) : undefined;
        } catch {
          body = undefined;
        }
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve(body as T);
          return;
        }
        const message = (body as { error?: string } | undefined)?.error ?? xhr.statusText ?? "Upload failed";
        const apiError = new Error(message) as Error & { status?: number };
        apiError.status = xhr.status;
        reject(apiError);
      };
      xhr.onerror = () => reject(new Error("Upload failed. Check your connection and try again."));
      xhr.ontimeout = () => reject(new Error("Upload timed out. Try again."));
      xhr.send(formData);
    }, reject);
  });
}

// Convenience helpers
export const api = {
  get: <T>(path: string, token?: string) =>
    apiFetch<T>(path, { method: "GET", token }),

  post: <T>(path: string, body: object, token?: string) =>
    apiFetch<T>(path, { method: "POST", body, token }),

  put: <T>(path: string, body: object, token?: string) =>
    apiFetch<T>(path, { method: "PUT", body, token }),

  delete: <T>(path: string, token?: string) =>
    apiFetch<T>(path, { method: "DELETE", token }),

  onboarding: {
    status: () => apiFetch<Record<string, string>>("/api/v1/onboarding"),
    report: (key: string, body: { status: OnboardingStatus; step: number }) =>
      apiFetch<{ status: string }>(`/api/v1/onboarding/${encodeURIComponent(key)}`, {
        method: "POST",
        body,
      }),
  },
};

// Typed API functions
export const feedbackApi = {
  submit: (message: string, route: string) =>
    apiFetch<{ status: string }>("/api/v1/feedback", { method: "POST", body: { message, route } }),
  mine: () => apiFetch<MyFeedbackListResponse>("/api/v1/feedback/mine"),
};

export const authApi = {
  login: (username: string, password: string) =>
    apiFetch<LoginResponse>("/api/v1/auth/login", { method: "POST", body: { username, password } }),
  me: () => apiFetch<{ user: User }>("/api/v1/auth/me"),
};

export const projectsApi = {
  list: () => apiFetch<ProjectsResponse>("/api/v1/projects"),
  // Create a project. The create-project UI always leaves org_id empty so new
  // projects land in the caller's personal org by default.
  create: (data: { slug: string; display_name?: string; org_id?: string; default_environment?: string }) =>
    apiFetch<CreateProjectResponse>("/api/v1/projects", { method: "POST", body: data }),
  // Idempotent: returns the caller's default project, creating one when they have
  // zero. The console calls this on first load so the user lands inside a project.
  ensureDefault: () =>
    apiFetch<CreateProjectResponse>("/api/v1/projects/default", { method: "POST", body: {} }),
  get: (id: string) => apiFetch<ProjectDetailResponse>(`/api/v1/projects/${id}`),
  operations: (projectId: string) => apiFetch<OperationsResponse>(`/api/v1/projects/${projectId}/operations`),
  getOperation: (projectId: string, opId: string) =>
    apiFetch<{ operation: Operation }>(`/api/v1/projects/${projectId}/operations/${opId}`),

  getDeleteImpact: (projectId: string) =>
    apiFetch<DeleteImpactResponse>(`/api/v1/projects/${projectId}/delete-impact`),

  remove: (projectId: string) =>
    apiFetch<OperationResponse>(`/api/v1/projects/${projectId}`, { method: "DELETE" }),
};

export const s3bucketsApi = {
  list: (projectId: string, envId: string) =>
    apiFetch<S3BucketsResponse>(`/api/v1/projects/${projectId}/environments/${envId}/s3buckets`),
  create: (projectId: string, envId: string, data: {
    name: string; bucket_name: string; region: string;
    description: string; public: boolean; ftp_sftp_enable: boolean;
    app_ref?: string;
  }) =>
    apiFetch<CreateS3BucketResponse>(`/api/v1/projects/${projectId}/environments/${envId}/s3buckets`, {
      method: "POST", body: data,
    }),

  credentials: (projectId: string, envId: string, name: string) =>
    apiFetch<S3BucketCredentialsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/s3buckets/${name}/credentials?reveal=true`
    ),

  remove: (projectId: string, envId: string, name: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/s3buckets/${name}`,
      { method: "DELETE" }
    ),
};

export const databasesApi = {
  list: (projectId: string, envId: string) =>
    apiFetch<DatabasesResponse>(`/api/v1/projects/${projectId}/environments/${envId}/databases`),
  create: (projectId: string, envId: string, data: {
    name: string; database: string; app_ref: string;
    backup_enabled: boolean; backup_schedule: string; backup_retention: string;
  }) =>
    apiFetch<CreateDatabaseResponse>(`/api/v1/projects/${projectId}/environments/${envId}/databases`, {
      method: "POST", body: data,
    }),

  remove: (projectId: string, envId: string, name: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}`,
      { method: "DELETE" }
    ),

  listBackups: (projectId: string, envId: string, name: string) =>
    apiFetch<DBBackupsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/backups`
    ),

  createBackup: (projectId: string, envId: string, name: string) =>
    apiFetch<{ backup: DBBackup }>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/backups`,
      { method: "POST" }
    ),

  restore: (projectId: string, envId: string, name: string, backupId: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/restore`,
      { method: "POST", body: { backup_id: backupId } }
    ),

  downloadBackup: (projectId: string, envId: string, name: string, backupId: string) =>
    apiFetch<DBBackupDownloadResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/backups/${backupId}/download`
    ),

  credentials: (projectId: string, envId: string, name: string) =>
    apiFetch<DatabaseCredentialsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/credentials?reveal=true`
    ),

  insights: (projectId: string, envId: string, name: string) =>
    apiFetch<DatabaseInsights>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/insights`
    ),

  tables: (projectId: string, envId: string, name: string) =>
    apiFetch<DatabaseTablesResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/tables`
    ),

  archivePlan: (
    projectId: string,
    envId: string,
    name: string,
    table: string,
    opts?: { schema?: string; cutoff?: string }
  ) => {
    const q = new URLSearchParams();
    if (opts?.schema) q.set("schema", opts.schema);
    if (opts?.cutoff) q.set("cutoff", opts.cutoff);
    const qs = q.toString();
    return apiFetch<DatabaseArchivePlan>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/tables/${encodeURIComponent(table)}/archive-plan${qs ? `?${qs}` : ""}`
    );
  },

  archiveRuns: (projectId: string, envId: string, name: string) =>
    apiFetch<DatabaseArchiveRunsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/archive-runs`
    ),

  startArchive: (
    projectId: string,
    envId: string,
    name: string,
    body: { table: string; schema?: string; cutoff: string }
  ) =>
    apiFetch<DatabaseArchiveRun>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/archive-runs`,
      { method: "POST", body }
    ),

  activity: (projectId: string, envId: string, name: string) =>
    apiFetch<DatabaseActivityResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/activity`
    ),

  cancelBackend: (projectId: string, envId: string, name: string, pid: number) =>
    apiFetch<{ cancelled: boolean; pid: number }>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/activity/${pid}/cancel`,
      { method: "POST" }
    ),

  table: (projectId: string, envId: string, name: string, table: string, schema?: string) =>
    apiFetch<DatabaseTableDetailResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/tables/${encodeURIComponent(table)}${schema ? `?schema=${encodeURIComponent(schema)}` : ""}`
    ),

  queries: (projectId: string, envId: string, name: string) =>
    apiFetch<DatabaseQueriesResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/queries`
    ),

  advisories: (projectId: string, envId: string, name: string) =>
    apiFetch<DatabaseAdvisoriesResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/databases/${name}/advisories`
    ),
};

export const appsApi = {
  list: (projectId: string, envId: string) =>
    apiFetch<AppsResponse>(`/api/v1/projects/${projectId}/environments/${envId}/apps`),

  // Generic infrastructure resources (kind='Infra') for an environment — e.g. the
  // postgres/nginx services of a VM compose stack, decomposed from the app services.
  listInfra: (projectId: string, envId: string) =>
    apiFetch<InfraResponse>(`/api/v1/projects/${projectId}/environments/${envId}/infra`),

  create: (projectId: string, envId: string, data: {
    name: string;
    image: string;
    port: number;
    replicas: number;
    workload_type?: string;
    volume?: { path: string; size: string; storage_class?: string };
    worker?: boolean;
  }) =>
    apiFetch<CreateAppResponse>(`/api/v1/projects/${projectId}/environments/${envId}/apps`, {
      method: "POST",
      body: data,
    }),

  uploadSourceArchive: (
    projectId: string,
    envId: string,
    appName: string,
    file: File,
    onProgress?: (percent: number) => void
  ) => {
    const formData = new FormData();
    formData.append("archive", file);
    return apiUpload<UploadSourceArchiveResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/source-archive`,
      formData,
      onProgress
    );
  },

  downloadSourceArchive: (projectId: string, envId: string, appName: string) =>
    apiFetch<{ url: string; filename: string; expires_at: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/source-archive/download`
    ),

  updateStorage: (
    projectId: string,
    envId: string,
    appName: string,
    volume: { path: string; size: string; storage_class?: string }
  ) =>
    apiFetch<{ operation: Operation; message: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/storage`,
      { method: "PUT", body: volume }
    ),

  updateComposeConfig: (
    projectId: string,
    envId: string,
    appName: string,
    body: { image: string; ports: string[] }
  ) =>
    apiFetch<{ operation: Operation; message: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/compose-config`,
      { method: "PATCH", body }
    ),

  volumeUsage: (projectId: string, envId: string, appName: string) =>
    apiFetch<{ used_bytes: number; capacity_bytes: number; ratio: number }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/volume/usage`
    ),

  updateComposeVolume: (
    projectId: string,
    envId: string,
    appName: string,
    body: { volumes: string[] }
  ) =>
    apiFetch<{ operation: Operation; message: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/compose-volume`,
      { method: "PUT", body }
    ),

  updateProfile: (projectId: string, envId: string, appName: string, profile: string) =>
    apiFetch<{ operation: Operation; message: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/profile`,
      { method: "PATCH", body: { profile } }
    ),

  updateImage: (projectId: string, envId: string, appName: string, image: string) =>
    apiFetch<DeployImageResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/image`,
      { method: "PATCH", body: { image } }
    ),

  /**
   * Overrides the shell-style arguments the app's container starts with.
   * Empty string clears the override and returns the app to the platform
   * default baked into the image. Does not enqueue a redeploy of its own —
   * takes effect on the app's next organic deploy.
   */
  updateStartCommand: (projectId: string, envId: string, appName: string, startCommand: string) =>
    apiFetch<{ start_command: string; message: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/start-command`,
      { method: "PATCH", body: { start_command: startCommand } }
    ),

  // Roll a compose (VM) app back to its previous committed compose.yaml + redeploy.
  rollback: (projectId: string, envId: string, appName: string) =>
    apiFetch<{ operation: Operation; message: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/rollback`,
      { method: "POST" }
    ),

  // Restart a compose (VM) app: recreate containers from the current compose (no pull).
  restart: (projectId: string, envId: string, appName: string) =>
    apiFetch<{ operation: Operation; message: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/restart`,
      { method: "POST" }
    ),

  /** Claim a starter-template demo app so the reaper stops counting down on it. */
  keepDemo: (projectId: string, envId: string, appName: string) =>
    apiFetch<{ message: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/keep`,
      { method: "POST" }
    ),

  // Adopt an existing single compose app into N first-class per-service Applications
  // (preserves the live stack + external volumes; brief cutover outage).
  adopt: (projectId: string, envId: string, appName: string) =>
    apiFetch<{ operation: Operation; message: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/adopt`,
      { method: "POST" }
    ),

  // Live compose state (Portainer proxy).
  getState: (projectId: string, envId: string, appName: string) =>
    apiFetch<AppState>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/state`
    ),

  getLogs: (projectId: string, envId: string, appName: string, container: string, tail = 200) =>
    apiFetch<{ logs: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/logs?container=${encodeURIComponent(
        container
      )}&tail=${tail}`
    ),

  // Container resource metrics (central Prometheus proxy, cAdvisor).
  getMetrics: (projectId: string, envId: string, appName: string, range = "1h") =>
    apiFetch<MetricsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/metrics?range=${range}`
    ),

  getDeleteImpact: (projectId: string, envId: string, appName: string) =>
    apiFetch<DeleteImpactResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/delete-impact`
    ),

  remove: (projectId: string, envId: string, appName: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}`,
      { method: "DELETE" }
    ),

  getMoveImpact: (projectId: string, envId: string, appName: string, targetProjectId: string) =>
    apiFetch<MoveImpactResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/move-impact?target_project_id=${encodeURIComponent(
        targetProjectId
      )}`
    ),

  move: (projectId: string, envId: string, appName: string, targetProjectId: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/move`,
      { method: "POST", body: { target_project_id: targetProjectId } }
    ),
};

/**
 * Browse and edit an app's persistent volume live, through a running pod.
 *
 * Every `path` is relative to the volume mount ("/" is the volume root), which
 * is also what the backend echoes back — so a path from a listing can be fed
 * straight into the next call.
 */
export const filesApi = {
  list: (projectId: string, envId: string, appName: string, path: string) =>
    apiFetch<AppFileListResponse>(
      `${volumeFilesBase(projectId, envId, appName)}?path=${encodeURIComponent(path)}`
    ),

  read: (projectId: string, envId: string, appName: string, path: string) =>
    apiFetch<AppFileContent>(
      `${volumeFilesBase(projectId, envId, appName)}/content?path=${encodeURIComponent(path)}`
    ),

  write: (
    projectId: string,
    envId: string,
    appName: string,
    body: { path: string; content: string; modified?: number }
  ) =>
    apiFetch<{ path: string; modified: number }>(
      `${volumeFilesBase(projectId, envId, appName)}/content`,
      { method: "PUT", body, timeoutMs: 120_000 }
    ),

  mkdir: (projectId: string, envId: string, appName: string, path: string) =>
    apiFetch<{ path: string }>(`${volumeFilesBase(projectId, envId, appName)}/mkdir`, {
      method: "POST",
      body: { path },
    }),

  move: (projectId: string, envId: string, appName: string, from: string, to: string) =>
    apiFetch<{ from: string; to: string }>(`${volumeFilesBase(projectId, envId, appName)}/move`, {
      method: "POST",
      body: { from, to },
    }),

  remove: (projectId: string, envId: string, appName: string, path: string, recursive: boolean) =>
    apiFetch<{ path: string }>(`${volumeFilesBase(projectId, envId, appName)}/delete`, {
      method: "POST",
      body: { path, recursive },
      timeoutMs: 120_000,
    }),

  upload: (
    projectId: string,
    envId: string,
    appName: string,
    dir: string,
    file: File,
    onProgress?: (percent: number) => void
  ) => {
    const formData = new FormData();
    formData.append("path", dir);
    formData.append("file", file);
    return apiUpload<{ path: string; size: number }>(
      `${volumeFilesBase(projectId, envId, appName)}/upload`,
      formData,
      onProgress
    );
  },

  downloadFile: (projectId: string, envId: string, appName: string, path: string) =>
    downloadAuthed(
      `${volumeFilesBase(projectId, envId, appName)}/raw?path=${encodeURIComponent(path)}`,
      path.split("/").pop() || "download"
    ),

  downloadDirectory: (projectId: string, envId: string, appName: string, path: string) =>
    downloadAuthed(
      `${volumeFilesBase(projectId, envId, appName)}/archive?path=${encodeURIComponent(path)}`,
      `${path.split("/").filter(Boolean).pop() || appName}.tar.gz`
    ),

  /**
   * Object URL for inline previews (images). The caller owns the URL and must
   * revoke it — a leaked blob URL pins the whole file in memory.
   */
  objectUrl: (projectId: string, envId: string, appName: string, path: string) =>
    fetchAuthedBlob(
      `${volumeFilesBase(projectId, envId, appName)}/raw?path=${encodeURIComponent(path)}`
    ).then((blob) => URL.createObjectURL(blob)),
};

function volumeFilesBase(projectId: string, envId: string, appName: string): string {
  return `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/volume/files`;
}

/**
 * Fetch a binary endpoint with the bearer token attached. Plain navigation
 * cannot carry the Authorization header, so downloads go through a blob.
 */
async function fetchAuthedBlob(path: string): Promise<Blob> {
  const token = await getToken();
  const res = await fetch(`${API_BASE_URL}${path}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}) as { error?: string; message?: string });
    const err = new Error(body.message ?? body.error ?? "Download failed") as Error & { status?: number };
    err.status = res.status;
    throw err;
  }
  return res.blob();
}

async function downloadAuthed(path: string, filename: string): Promise<void> {
  const blob = await fetchAuthedBlob(path);
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 30_000);
}

/** CI deploy tokens ("deploy hooks") — lets an external CI push a new image without console access. */
export const deployHooksApi = {
  list: (projectId: string, envId: string, appName: string) =>
    apiFetch<{ deploy_hooks: DeployHook[] } | DeployHook[]>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/deploy-hooks`
    ).then((r) => (Array.isArray(r) ? r : r?.deploy_hooks ?? [])),

  create: (projectId: string, envId: string, appName: string, name?: string) =>
    apiFetch<DeployHookCreated>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/deploy-hooks`,
      { method: "POST", body: { name } }
    ),

  revoke: (projectId: string, envId: string, appName: string, hookId: string) =>
    apiFetch<void>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/deploy-hooks/${hookId}`,
      { method: "DELETE" }
    ),
};

/**
 * AI Gateway control plane: project keys for the OpenAI-compatible endpoint,
 * the BYOK provider credentials the gateway injects server-side, the model
 * catalog, and the project's own consumption.
 */
export const aiGatewayApi = {
  catalog: () => apiFetch<AICatalogResponse>(`/api/v1/ai/catalog`),

  listKeys: (projectId: string) =>
    apiFetch<{ keys: AIGatewayKey[]; base_url: string }>(`/api/v1/projects/${projectId}/ai/keys`),

  createKey: (projectId: string, name?: string) =>
    apiFetch<AIGatewayKeyCreated>(`/api/v1/projects/${projectId}/ai/keys`, {
      method: "POST",
      body: { name: name ?? "" },
    }),

  revokeKey: (projectId: string, keyId: string) =>
    apiFetch<void>(`/api/v1/projects/${projectId}/ai/keys/${keyId}`, { method: "DELETE" }),

  listCredentials: (projectId: string) =>
    apiFetch<{ credentials: AIProviderCredential[] }>(`/api/v1/projects/${projectId}/ai/credentials`),

  putCredential: (projectId: string, provider: string, apiKey: string, apiBase?: string) =>
    apiFetch<{ status: string }>(
      `/api/v1/projects/${projectId}/ai/credentials/${encodeURIComponent(provider)}`,
      { method: "PUT", body: { api_key: apiKey, api_base: apiBase ?? "" } }
    ),

  deleteCredential: (projectId: string, provider: string) =>
    apiFetch<void>(`/api/v1/projects/${projectId}/ai/credentials/${encodeURIComponent(provider)}`, {
      method: "DELETE",
    }),

  usage: (projectId: string, days: 7 | 30 = 7) =>
    apiFetch<ProjectAIUsage>(`/api/v1/projects/${projectId}/ai/usage?days=${days}`),

  routing: (projectId: string) =>
    apiFetch<AIRoutingSettings>(`/api/v1/projects/${projectId}/ai/routing`),

  setRouting: (projectId: string, mode: AIRoutingMode) =>
    apiFetch<AIRoutingSettings>(`/api/v1/projects/${projectId}/ai/routing`, {
      method: "PUT",
      body: { mode },
    }),
};

export const costApi = {
  getProjectCost: (projectId: string, window: string = "30d") =>
    apiFetch<CostResponse>(`/api/v1/projects/${projectId}/cost?window=${encodeURIComponent(window)}`),
};

/**
 * Boxes: ephemeral root sandboxes.
 *
 * `up` is synchronous by design — the backend returns only once a command has
 * actually run inside the box — so it gets its own timeout well above the
 * 30s default. The bound the server honours is wait_seconds; the client
 * timeout sits above it so the caller sees the server's classified failure
 * rather than a generic "Request timed out".
 */
export const boxesApi = {
  list: (projectId: string) =>
    apiFetch<BoxesResponse>(`/api/v1/projects/${projectId}/boxes`),

  catalog: () => apiFetch<BoxCatalogResponse>(`/api/v1/box/catalog`),

  up: (projectId: string, data: { name?: string; ttl_seconds?: number; wait_seconds?: number }) =>
    apiFetch<BoxUpResponse>(`/api/v1/projects/${projectId}/box-up`, {
      method: "POST",
      body: data,
      timeoutMs: 150_000,
    }),

  connection: (projectId: string, boxName: string, newSession = false) =>
    apiFetch<BoxConnectionResponse>(
      `/api/v1/projects/${projectId}/boxes/${boxName}/connection${newSession ? "?new_session=true" : ""}`
    ),

  expose: (projectId: string, boxName: string, port: number) =>
    apiFetch<BoxExposeResponse>(`/api/v1/projects/${projectId}/boxes/${boxName}/expose`, {
      method: "POST",
      body: { port },
      timeoutMs: 60_000,
    }),

  suspend: (projectId: string, boxName: string) =>
    apiFetch<{ message: string }>(`/api/v1/projects/${projectId}/boxes/${boxName}/suspend`, {
      method: "POST",
    }),

  resume: (projectId: string, boxName: string) =>
    apiFetch<{ message: string }>(`/api/v1/projects/${projectId}/boxes/${boxName}/resume`, {
      method: "POST",
    }),

  remove: (projectId: string, boxName: string) =>
    apiFetch<{ message: string }>(`/api/v1/projects/${projectId}/boxes/${boxName}`, {
      method: "DELETE",
    }),
};

export const appServersApi = {
  list: (projectId: string) =>
    apiFetch<AppServersResponse>(`/api/v1/projects/${projectId}/app-servers`),

  get: (projectId: string, serverName: string) =>
    apiFetch<AppServerResponse>(`/api/v1/projects/${projectId}/app-servers/${serverName}`),

  getState: (projectId: string, serverName: string) =>
    apiFetch<AppServerState>(`/api/v1/projects/${projectId}/app-servers/${serverName}/state`),

  create: (projectId: string, data: {
    name: string;
    mode?: "terraform" | "manual";
    // terraform mode
    flavor?: string;
    os_image?: string;
    region?: string;
    ssh_key_name?: string;
    // manual mode (connecting a pre-existing VM over SSH)
    vm_ip?: string;
    ssh_user?: string;
    ssh_port?: number;
    ssh_private_key?: string;
  }) =>
    apiFetch<CreateAppServerResponse>(`/api/v1/projects/${projectId}/app-servers`, {
      method: "POST",
      body: data,
    }),

  remove: (projectId: string, serverName: string) =>
    apiFetch<{ operation: Operation; message: string }>(
      `/api/v1/projects/${projectId}/app-servers/${serverName}`,
      { method: "DELETE" }
    ),

  // Read-only workload discovery via the Portainer docker proxy (no SSH). Result
  // lands on the returned operation's validation_result once it reaches Ready.
  discover: (projectId: string, serverName: string) =>
    apiFetch<{ operation: Operation; message: string }>(
      `/api/v1/projects/${projectId}/app-servers/${serverName}/discover`,
      { method: "POST" }
    ),

  // Adopt a discovered workload into a managed compose app: renders compose.yaml +
  // .env from the selected services, commits to git, deploys via the existing stack
  // pipeline. Async — poll the returned operation until terminal, then the app exists.
  import: (projectId: string, serverName: string, body: ImportRequest) =>
    apiFetch<{ operation: Operation; message: string }>(
      `/api/v1/projects/${projectId}/app-servers/${serverName}/import`,
      { method: "POST", body }
    ),

  // VM resource metrics (central Prometheus proxy, node_exporter).
  getMetrics: (projectId: string, serverName: string, range = "1h") =>
    apiFetch<MetricsResponse>(
      `/api/v1/projects/${projectId}/app-servers/${serverName}/metrics?range=${range}`
    ),
};

// Aggregated log search (Elasticsearch/filebeat proxy). At least one of vm/app
// is required and must belong to the project (enforced server-side).
export const logsApi = {
  search: (
    projectId: string,
    params: { vm?: string; app?: string; q?: string; since?: string; size?: number }
  ) => {
    const qs = new URLSearchParams();
    if (params.vm) qs.set("vm", params.vm);
    if (params.app) qs.set("app", params.app);
    if (params.q) qs.set("q", params.q);
    if (params.since) qs.set("since", params.since);
    if (params.size) qs.set("size", String(params.size));
    return apiFetch<LogSearchResponse>(`/api/v1/projects/${projectId}/logs?${qs.toString()}`);
  },
};

export const endpointsApi = {
  list: (projectId: string, envId: string, appName: string) =>
    apiFetch<EndpointsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/endpoints`
    ),

  create: (
    projectId: string,
    envId: string,
    appName: string,
    data: {
      fqdn: string;
      auth_enabled: boolean;
      auth_scheme: string;
      auth_scopes: string[];
      swagger_enabled: boolean;
      swagger_path: string;
      swagger_title: string;
    }
  ) =>
    apiFetch<CreateEndpointResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/endpoints`,
      { method: "POST", body: data }
    ),
};

// Custom domains (user-owned domains + auto TLS). Level 1 = apex authorizations
// (project-scoped), Level 2 = hostname attachments (app-scoped).
export const customDomainsApi = {
  listAuthorizations: (projectId: string) =>
    apiFetch<DomainAuthorizationsResponse>(
      `/api/v1/projects/${projectId}/domain-authorizations`
    ),

  addAuthorization: (projectId: string, apexDomain: string) =>
    apiFetch<AddDomainAuthorizationResponse>(
      `/api/v1/projects/${projectId}/domain-authorizations`,
      { method: "POST", body: { apex_domain: apexDomain } }
    ),

  verifyAuthorization: (projectId: string, id: string) =>
    apiFetch<VerifyDomainAuthorizationResponse>(
      `/api/v1/projects/${projectId}/domain-authorizations/${id}/verify`,
      { method: "POST" }
    ),

  deleteAuthorization: (projectId: string, id: string) =>
    apiFetch<void>(
      `/api/v1/projects/${projectId}/domain-authorizations/${id}`,
      { method: "DELETE" }
    ),

  listHostnames: (projectId: string, envId: string, appName: string) =>
    apiFetch<HostnamesResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/hostnames`
    ),

  attachHostname: (projectId: string, envId: string, appName: string, hostname: string) =>
    apiFetch<AttachHostnameResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/hostnames`,
      { method: "POST", body: { hostname } }
    ),

  detachHostname: (projectId: string, envId: string, appName: string, id: string) =>
    apiFetch<DetachHostnameResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/hostnames/${id}`,
      { method: "DELETE" }
    ),
};

/**
 * Managed DNS via NS delegation (Slice 1 backend). All endpoints are scoped to a
 * verified apex authorization (authId). Paths mirror the shipped Slice 1 routes
 * under /domains/authorizations/:authId. Record list / import-preview may return
 * either a bare array or a { records } wrapper; callers normalize with
 * normalizeRecords().
 */
export const managedDnsApi = {
  delegate: (projectId: string, authId: string) =>
    apiFetch<DelegateZoneResponse>(
      `/api/v1/projects/${projectId}/domains/authorizations/${authId}/delegate`,
      { method: "POST" }
    ),

  getZone: (projectId: string, authId: string) =>
    apiFetch<ManagedZone>(
      `/api/v1/projects/${projectId}/domains/authorizations/${authId}/zone`
    ),

  listRecords: (projectId: string, authId: string) =>
    apiFetch<ManagedZoneRecord[] | { records: ManagedZoneRecord[] }>(
      `/api/v1/projects/${projectId}/domains/authorizations/${authId}/zone/records`
    ),

  upsertRecord: (projectId: string, authId: string, record: ManagedZoneRecord) =>
    apiFetch<ManagedZoneRecord | { record: ManagedZoneRecord }>(
      `/api/v1/projects/${projectId}/domains/authorizations/${authId}/zone/records`,
      { method: "POST", body: record }
    ),

  deleteRecord: (projectId: string, authId: string, name: string, type: string) =>
    apiFetch<void>(
      `/api/v1/projects/${projectId}/domains/authorizations/${authId}/zone/records`,
      { method: "DELETE", body: { name, type } }
    ),

  importPreview: (projectId: string, authId: string) =>
    apiFetch<ManagedZoneRecord[] | { records: ManagedZoneRecord[] }>(
      `/api/v1/projects/${projectId}/domains/authorizations/${authId}/zone/import-preview`
    ),

  importRecords: (projectId: string, authId: string, records: ManagedZoneRecord[]) =>
    apiFetch<ZoneImportResult>(
      `/api/v1/projects/${projectId}/domains/authorizations/${authId}/zone/import`,
      { method: "POST", body: { records } }
    ),
};

/** Normalize a records/import-preview response into a plain record array. */
export function normalizeRecords(
  data: ManagedZoneRecord[] | { records: ManagedZoneRecord[] }
): ManagedZoneRecord[] {
  return Array.isArray(data) ? data : data.records ?? [];
}

// Values editor — issues a short-lived WS delegate token from the backend.
export const valuesApi = {
  // file selects which editable file the session targets:
  // "values.yaml" (default, Helm apps), "compose.yaml" or ".env" (compose apps).
  getToken: (projectId: string, envId: string, appName: string, file?: string) =>
    apiFetch<{ token: string; ws_url: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/values-token${
        file ? `?file=${encodeURIComponent(file)}` : ""
      }`,
      { method: "POST" }
    ),
};

// AI Studio (v1). Routes are mounted by default; AI_STUDIO_ENABLED=false
// on the backend hides them again as a runtime kill-switch.
export const aiModelsApi = {
  list: (projectId: string, envId: string) =>
    apiFetch<AIModelsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/models`
    ),
  get: (projectId: string, envId: string, name: string) =>
    apiFetch<AIModelDetailResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/models/${name}`
    ),
  create: (projectId: string, envId: string, data: CreateAIModelRequest) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/models`,
      { method: "POST", body: data }
    ),
  delete: (projectId: string, envId: string, name: string, force = false) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/models/${name}${force ? "?force=true" : ""}`,
      { method: "DELETE" }
    ),
  updateArtifact: (
    projectId: string,
    envId: string,
    name: string,
    body: { artifact_uri?: string; mlflow_name?: string; mlflow_version?: string }
  ) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/models/${name}/artifact`,
      { method: "PATCH", body }
    ),
  setCanary: (projectId: string, envId: string, name: string, trafficPercent: number) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/models/${name}/canary`,
      { method: "PATCH", body: { traffic_percent: trafficPercent } }
    ),
  promote: (projectId: string, envId: string, name: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/models/${name}/promote`,
      { method: "POST", body: {} }
    ),
  pinMlflow: (projectId: string, envId: string, name: string, mlflowName: string, mlflowVersion: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/models/${name}/mlflow-pin`,
      { method: "PATCH", body: { mlflow_name: mlflowName, mlflow_version: mlflowVersion } }
    ),
  revealApiKey: (projectId: string, envId: string, name: string) =>
    apiFetch<RevealAPIKeyResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/models/${name}/api-key?reveal=true`
    ),
};

export const quotasApi = {
  get: (projectId: string) =>
    apiFetch<QuotaUsageResponse>(`/api/v1/projects/${projectId}/quotas`),
};

export const mlflowApi = {
  listRegisteredModels: (projectId: string) =>
    apiFetch<MLflowModelsResponse>(`/api/v1/mlflow/registered-models?project=${projectId}`),
  listVersions: (projectId: string, name: string) =>
    apiFetch<MLflowVersionsResponse>(
      `/api/v1/mlflow/registered-models/${encodeURIComponent(name)}/versions?project=${projectId}`
    ),
  getVersion: (projectId: string, name: string, version: string) =>
    apiFetch<{ version: MLflowModelVersion }>(
      `/api/v1/mlflow/registered-models/${encodeURIComponent(name)}/versions/${version}?project=${projectId}`
    ),
};

export const adminApi = {
  listApprovals: () =>
    apiFetch<PendingApprovalsResponse>("/api/v1/admin/operations"),
  approve: (opId: string, note?: string) =>
    apiFetch<{ operation: Operation }>(`/api/v1/admin/operations/${opId}/approve`, {
      method: "POST",
      body: { note: note ?? "" },
    }),
  reject: (opId: string, reason: string) =>
    apiFetch<{ operation: Operation }>(`/api/v1/admin/operations/${opId}/reject`, {
      method: "POST",
      body: { reason },
    }),
  listAuditEvents: (
    params: {
      action?: string;
      user?: string;
      kind?: string;
      excludeActions?: string[];
      excludeUsers?: string[];
      excludeKinds?: string[];
      limit?: number;
      offset?: number;
    } = {},
  ) => {
    const q = new URLSearchParams();
    if (params.action) q.set("action", params.action);
    if (params.user) q.set("user", params.user);
    if (params.kind) q.set("kind", params.kind);
    if (params.excludeActions?.length) q.set("exclude_action", params.excludeActions.join(","));
    if (params.excludeUsers?.length) q.set("exclude_user", params.excludeUsers.join(","));
    if (params.excludeKinds?.length) q.set("exclude_kind", params.excludeKinds.join(","));
    q.set("limit", String(params.limit ?? 50));
    q.set("offset", String(params.offset ?? 0));
    return apiFetch<AuditEventsResponse>(`/api/v1/admin/audit?${q.toString()}`);
  },
  listAuditFacets: (
    params: {
      kind?: string;
      excludeActions?: string[];
      excludeUsers?: string[];
      excludeKinds?: string[];
    } = {},
  ) => {
    const q = new URLSearchParams();
    if (params.kind) q.set("kind", params.kind);
    if (params.excludeActions?.length) q.set("exclude_action", params.excludeActions.join(","));
    if (params.excludeUsers?.length) q.set("exclude_user", params.excludeUsers.join(","));
    if (params.excludeKinds?.length) q.set("exclude_kind", params.excludeKinds.join(","));
    const suffix = q.toString();
    return apiFetch<AuditFacetsResponse>(`/api/v1/admin/audit/facets${suffix ? `?${suffix}` : ""}`);
  },
  getAuditCoverage: (days = 30) =>
    apiFetch<AuditCoverageResponse>(`/api/v1/admin/audit/coverage?days=${days}`),
  listFeedback: (params: { status?: string; limit?: number } = {}) => {
    const q = new URLSearchParams();
    if (params.status) q.set("status", params.status);
    q.set("limit", String(params.limit ?? 50));
    return apiFetch<FeedbackListResponse>(`/api/v1/admin/feedback?${q.toString()}`);
  },
  resolveFeedback: (id: string, resolution: string) =>
    apiFetch<{ status: string }>(`/api/v1/admin/feedback/${id}/resolve`, {
      method: "POST",
      body: { resolution },
    }),
  autofixFeedback: (id: string) =>
    apiFetch<CreateCloudTaskResponse>(`/api/v1/admin/feedback/${id}/autofix`, { method: "POST" }),
  getOverview: (days = 14) =>
    apiFetch<AdminOverviewResponse>(`/api/v1/admin/overview?days=${days}`),
  getCosts: (days: 7 | 30 = 30) =>
    apiFetch<AdminCostsResponse>(`/api/v1/admin/costs?days=${days}`),
  getAIGatewayUsage: (days: 7 | 30 = 7) =>
    apiFetch<AIGatewayUsageResponse>(`/api/v1/admin/ai-gateway/usage?days=${days}`),
  getDBShards: () => apiFetch<AdminDBShardsResponse>(`/api/v1/admin/db-shards`),
};

// Vercel-flow API clients -------------------------------------------------------

export const gitApi = {
  installations: (projectId: string) =>
    apiFetch<InstallationsResponse>(`/api/v1/projects/${projectId}/git/installations`),

  installUrl: (projectId: string, provider: string) =>
    apiFetch<{ url: string }>(`/api/v1/projects/${projectId}/git/install-url?provider=${encodeURIComponent(provider)}`),

  githubAuthorizeUrl: (projectId: string) =>
    apiFetch<{ url: string }>(`/api/v1/projects/${projectId}/git/github/authorize`),

  // Existing App installations the project can bind without a reinstall.
  availableInstallations: (projectId: string) =>
    apiFetch<{ installations: AvailableInstallation[] }>(
      `/api/v1/projects/${projectId}/git/installations/available`
    ),

  // Attach an existing installation (numeric id) to the project.
  bindInstallation: (projectId: string, installationId: string) =>
    apiFetch<GitInstallation>(`/api/v1/projects/${projectId}/git/installations`, {
      method: "POST",
      body: { installation_id: installationId },
    }),

  remoteRepos: (projectId: string, installationId: string) =>
    apiFetch<RemoteReposResponse>(
      `/api/v1/projects/${projectId}/git/installations/${installationId}/repos`
    ),

  detect: (projectId: string, installationId: string, repoFullName: string, rootDir = ".") =>
    apiFetch<FrameworkDetection>(
      `/api/v1/projects/${projectId}/git/installations/${installationId}/detect?repo=${encodeURIComponent(repoFullName)}&root_dir=${encodeURIComponent(rootDir)}`
    ),

  /**
   * Framework detection for a public repo the caller has no installation for.
   * Backs the "Deploy on Dada" badge flow, where the visitor typically does not
   * own the repository they are deploying.
   */
  detectPublic: (projectId: string, repoFullName: string, rootDir = ".") =>
    apiFetch<FrameworkDetection>(
      `/api/v1/projects/${projectId}/git/detect?repo=${encodeURIComponent(repoFullName)}&root_dir=${encodeURIComponent(rootDir)}`
    ),

  /**
   * Framework detection for the connect-by-URL flow: a repo the caller has
   * no App installation for, reached with a caller-supplied token instead
   * (or no token, for a public repo). Backs the port prefill in
   * ConnectByUrlDialog.
   */
  detectByUrl: (projectId: string, repoFullName: string, rootDir = ".", token = "") =>
    apiFetch<FrameworkDetection>(`/api/v1/projects/${projectId}/git/detect-url`, {
      method: "POST",
      body: { repo_full_name: repoFullName, root_dir: rootDir, token },
    }),

  linkRepo: (
    projectId: string,
    envId: string,
    data: {
      installation_id?: string;
      repo_full_name: string;
      app_name: string;
      production_branch: string;
      root_dir: string;
      framework_override?: string;
      auto_deploy: boolean;
      port?: number;
      worker?: boolean;
      replicas?: number;
      profile?: string;
      provider?: string;
      clone_url?: string;
      token?: string;
    }
  ) =>
    apiFetch<GitReposResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/repos`,
      { method: "POST", body: data }
    ),

  listRepos: (projectId: string, envId: string) =>
    apiFetch<GitReposResponse>(`/api/v1/projects/${projectId}/environments/${envId}/repos`),

  updateRepo: (
    projectId: string,
    envId: string,
    repoId: string,
    data: {
      production_branch?: string;
      root_dir?: string;
      framework_override?: string;
      auto_deploy?: boolean;
    }
  ) =>
    apiFetch<{ repo: GitReposResponse["repos"][0] }>(
      `/api/v1/projects/${projectId}/environments/${envId}/repos/${repoId}`,
      { method: "PATCH", body: data }
    ),
};

/**
 * Ready-made project catalog: the open-source projects the console offers on an
 * empty project, and the parser that turns whatever the customer pasted into a
 * repository name. Deploying an entry uses gitApi.linkRepo + buildsApi.trigger —
 * the same path a customer's own repository takes — so there is no install call
 * here on purpose.
 */
export const solutionsApi = {
  list: () =>
    apiFetch<{ solutions: Solution[]; categories: SolutionCategory[] }>(`/api/v1/solutions`),

  get: (slug: string) => apiFetch<Solution>(`/api/v1/solutions/${encodeURIComponent(slug)}`),

  /** Canonicalises a browser URL, clone URL, SSH remote or bare owner/name. */
  parseRepoUrl: (url: string) =>
    apiFetch<{ repo_full_name: string }>(
      `/api/v1/git/parse-repo-url?url=${encodeURIComponent(url)}`
    ),

  /**
   * Turns one typed string into a ranked list of things to deploy: catalog
   * entries, managed resources, a pasted repository, and GitHub search results
   * below them. Project-scoped because searching spends a rate-limit budget
   * shared by the whole platform, so it is gated on write access.
   */
  resolve: (projectId: string, query: string) =>
    apiFetch<ResolveSolutionsResponse>(
      `/api/v1/projects/${projectId}/solutions/resolve?q=${encodeURIComponent(query)}`
    ),

  /**
   * Installs a ready-made project or any public repository: one call that links
   * the repo, orders the managed database the entry declares it needs, and
   * queues the first build. The console used to run those as three calls and
   * had to unwind the first two by hand when the third failed.
   */
  install: (
    projectId: string,
    envId: string,
    data: {
      slug?: string;
      repo?: string;
      app_name?: string;
      branch?: string;
      root_dir?: string;
      framework?: string;
      port?: number;
      profile?: string;
      with_database?: boolean;
      params?: Record<string, string>;
    }
  ) =>
    apiFetch<InstallSolutionResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/solutions/install`,
      { method: "POST", body: data }
    ),
};

export const buildsApi = {
  list: (projectId: string, envId: string, appName: string) =>
    apiFetch<BuildsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/builds`
    ),

  get: (projectId: string, buildId: string) =>
    apiFetch<{ build: Build }>(`/api/v1/projects/${projectId}/builds/${buildId}`),

  trigger: (projectId: string, envId: string, appName: string) =>
    apiFetch<{ build: Build }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/builds`,
      { method: "POST", body: {} }
    ),

  cancel: (projectId: string, buildId: string) =>
    apiFetch<{ build: Build }>(
      `/api/v1/projects/${projectId}/builds/${buildId}/cancel`,
      { method: "POST", body: {} }
    ),

  logsToken: (projectId: string, buildId: string) =>
    apiFetch<{ token: string; ws_url: string }>(
      `/api/v1/projects/${projectId}/builds/${buildId}/logs-token`,
      { method: "POST", body: {} }
    ),
};

export const deploymentsApi = {
  list: (projectId: string, envId: string, appName: string) =>
    apiFetch<DeploymentsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/deployments`
    ),

  promote: (projectId: string, deploymentId: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/deployments/${deploymentId}/promote`,
      { method: "POST", body: {} }
    ),

  rollback: (projectId: string, deploymentId: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/deployments/${deploymentId}/rollback`,
      { method: "POST", body: {} }
    ),
};

export const diagnoseApi = {
  run: (projectId: string, envId: string, appName: string) =>
    apiFetch<AppDiagnosis>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/diagnose`,
      { method: "POST", body: {}, timeoutMs: 60_000 }
    ),
};

export const envVarsApi = {
  list: (projectId: string, envId: string, appName: string) =>
    apiFetch<EnvVarsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/env`
    ),

  upsert: (
    projectId: string,
    envId: string,
    appName: string,
    key: string,
    data: {
      value: string;
      is_secret: boolean;
      scope: "build" | "runtime" | "both";
    }
  ) =>
    apiFetch<SetEnvVarResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/env/${encodeURIComponent(key)}`,
      { method: "PUT", body: data }
    ),

  bulkUpsert: (
    projectId: string,
    envId: string,
    appName: string,
    vars: { key: string; value: string; is_secret: boolean; scope: "build" | "runtime" | "both" }[]
  ) =>
    apiFetch<BulkSetEnvVarsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/env/bulk`,
      { method: "POST", body: { vars } }
    ),

  reveal: (projectId: string, envId: string, appName: string, key: string) =>
    apiFetch<{ value: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/env/${encodeURIComponent(key)}?reveal=true`
    ),

  remove: (projectId: string, envId: string, appName: string, key: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/env/${encodeURIComponent(key)}`,
      { method: "DELETE" }
    ),
};

export const appDomainsApi = {
  list: (projectId: string, envId: string, appName: string) =>
    apiFetch<DomainsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/domains`
    ),
};

/** Preview (ephemeral, PR-scoped) environments — tear-down is the only console-facing action. */
export const previewsApi = {
  delete: (projectId: string, envId: string) =>
    apiFetch<OperationResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/preview`,
      { method: "DELETE" }
    ),
};

export const monitoringApi = {
  base: (projectId: string, envId: string) =>
    `/api/v1/projects/${projectId}/environments/${envId}/monitoring`,

  // List is project-wide (all envs). Backend route has NO envId segment and
  // returns { monitoring_apps }. See backend router.go:223 + monitoring.go:283.
  list: (projectId: string) =>
    apiFetch<{ monitoring_apps: MonitoringApp[] }>(
      `/api/v1/projects/${projectId}/monitoring`
    ),

  // Create is env-scoped; backend returns { monitoring_app, api_key }.
  create: (projectId: string, envId: string, name: string) =>
    apiFetch<{ monitoring_app: MonitoringApp; api_key: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring`,
      { method: "POST", body: { name } }
    ),

  get: (projectId: string, envId: string, appId: string) =>
    apiFetch<{ app: MonitoringApp }>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}`
    ),

  delete: (projectId: string, envId: string, appId: string) =>
    apiFetch<void>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}`,
      { method: "DELETE" }
    ),

  getHealth: (projectId: string, envId: string, appId: string) =>
    apiFetch<HealthStatus>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/health`
    ),

  getLabels: (projectId: string, envId: string, appId: string, range = "24h") => {
    const qs = new URLSearchParams({ range });
    return apiFetch<MonitoringLabelsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/labels?${qs.toString()}`
    );
  },

  getMetrics: (
    projectId: string,
    envId: string,
    appId: string,
    opts?: {
      range?: string;
      groupBy?: string;
      filters?: string[];
      agg?: string;
      from?: number;
      to?: number;
    }
  ) => {
    const qs = new URLSearchParams();
    // Absolute window wins; otherwise fall back to the relative range.
    if (opts?.from && opts?.to) {
      qs.set("from", String(opts.from));
      qs.set("to", String(opts.to));
    } else {
      qs.set("range", opts?.range ?? "1h");
    }
    if (opts?.groupBy) qs.set("groupBy", opts.groupBy);
    if (opts?.agg) qs.set("agg", opts.agg);
    for (const f of opts?.filters ?? []) qs.append("filter", f);
    return apiFetch<MonitoringMetricsResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/metrics?${qs.toString()}`
    );
  },

  getLogs: (
    projectId: string,
    envId: string,
    appId: string,
    params: { q?: string; since?: string; size?: number }
  ) => {
    const qs = new URLSearchParams();
    if (params.q) qs.set("q", params.q);
    if (params.since) qs.set("since", params.since);
    if (params.size) qs.set("size", String(params.size));
    return apiFetch<LogSearchResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/logs?${qs.toString()}`
    );
  },

  getGrafanaLink: (projectId: string, envId: string, appId: string) =>
    apiFetch<{ url: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/grafana-link`
    ),

  listAlertRules: (projectId: string, envId: string, appId: string) =>
    apiFetch<{ rules: AlertRule[] }>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/alert-rules`
    ),

  createAlertRule: (
    projectId: string,
    envId: string,
    appId: string,
    data: {
      name: string;
      metric: string;
      condition: string;
      threshold: number;
      duration: string;
      channel_id?: string;
    }
  ) =>
    apiFetch<{ rule: AlertRule }>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/alert-rules`,
      { method: "POST", body: data }
    ),

  deleteAlertRule: (projectId: string, envId: string, appId: string, ruleId: string) =>
    apiFetch<void>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/alert-rules/${ruleId}`,
      { method: "DELETE" }
    ),

  getDashboard: (projectId: string, envId: string, appId: string) =>
    apiFetch<{ config: unknown | null; version: number; updated_at?: string }>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/dashboard`
    ),

  saveDashboard: (projectId: string, envId: string, appId: string, config: unknown, version: number) =>
    apiFetch<{ saved: boolean; version: number }>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/dashboard`,
      { method: "PUT", body: { config, version } }
    ),

  getEvents: (
    projectId: string,
    envId: string,
    appId: string,
    opts?: { range?: string; from?: number; to?: number }
  ) => {
    const qs = new URLSearchParams();
    if (opts?.from && opts?.to) {
      qs.set("from", String(opts.from));
      qs.set("to", String(opts.to));
    } else if (opts?.range) {
      qs.set("range", opts.range);
    }
    return apiFetch<{ events: { time: number; label: string; kind: string }[] }>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/${appId}/events?${qs.toString()}`
    );
  },

  listChannels: (projectId: string, envId: string) =>
    apiFetch<{ channels: Channel[] }>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/channels`
    ),

  createChannel: (
    projectId: string,
    envId: string,
    data: {
      name: string;
      type: "telegram" | "email" | "webhook";
      settings: Record<string, string>;
    }
  ) =>
    apiFetch<{ channel: Channel }>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/channels`,
      { method: "POST", body: data }
    ),

  deleteChannel: (projectId: string, envId: string, id: string) =>
    apiFetch<void>(
      `/api/v1/projects/${projectId}/environments/${envId}/monitoring/channels/${id}`,
      { method: "DELETE" }
    ),
};

export const cloudTasksApi = {
  list: (projectId: string, envId: string, appName: string) =>
    apiFetch<CloudTasksResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/cloud-tasks`,
    ),

  create: (projectId: string, envId: string, appName: string, taskType: string, params?: Record<string, string>) =>
    apiFetch<CreateCloudTaskResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/cloud-tasks`,
      { method: "POST", body: params ? { task_type: taskType, params } : { task_type: taskType } },
    ),

  triggerAutofix: (projectId: string, envId: string, appName: string, error: string) =>
    apiFetch<CreateCloudTaskResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/autofix`,
      { method: "POST", body: { error } },
    ),

  get: (projectId: string, taskId: string) =>
    apiFetch<CloudTaskResponse>(`/api/v1/projects/${projectId}/cloud-tasks/${taskId}`),

  artifactUrl: (projectId: string, taskId: string, fileId: string) =>
    `/api/v1/projects/${projectId}/cloud-tasks/${taskId}/artifacts/${fileId}`,
};

export type BillingPlanKey = "free" | "startup" | "business" | "enterprise";

/**
 * Plan limits as the backend spells them (pricing.Quotas json tags). The member
 * limit is `team_members` on every billing surface -- quotas, usage, the 403
 * payload and the consumption summary -- so it is spelled that way here too.
 */
export interface BillingQuota {
  apps: number | null;
  databases: number | null;
  storage_gb: number | null;
  domains: number | null;
  environments: number | null;
  team_members: number | null;
  backup_retention_days: number | null;
  app_servers: number | null;
}

export interface BillingUsageItem {
  used: number;
  limit: number | null;
}

export interface BillingUsage {
  apps: BillingUsageItem;
  databases: BillingUsageItem;
  storage_gb: BillingUsageItem;
  domains: BillingUsageItem;
  environments: BillingUsageItem;
  team_members: BillingUsageItem;
}

export interface InvoicePreview {
  period: string;
  amount: number;
  currency: string;
  status: "preview";
}

/** One resource the org already holds more of than its plan allows. */
export interface QuotaOverLimit {
  resource: string;
  used: number;
  limit: number;
}

/**
 * Automatic renewal state. A `methodTitle` without `enabled` is the normal
 * state of a paused subscription: turning renewal off keeps the card so it can
 * be resumed in one click. `enabled` without a `methodTitle` cannot happen --
 * the backend refuses to arm a charge with no instrument behind it.
 */
export interface AutopayState {
  enabled: boolean;
  methodTitle: string;
  failures: number;
  /** When the card is charged next, a day before the term ends. Null when autopay is off. */
  nextChargeAt: string | null;
}

export interface BillingAccount {
  plan: BillingPlanKey;
  plan_expires_at?: string | null;
  quota_grace_until?: string | null;
  /** Whether quotas actually block new resource creation for this org right now. Absent on older backends -- treat as false (do not alarm). */
  quota_enforced?: boolean;
  /** Resources currently over the plan limit. Non-empty during grace means creation breaks the day grace ends. */
  quota_over_limit?: QuotaOverLimit[];
  autopay?: AutopayState;
  quotas: BillingQuota;
  usage: BillingUsage;
  invoicePreview: InvoicePreview;
}

export interface BillingPlan {
  key: BillingPlanKey;
  name: string;
  price_rub: number | null;
  quotas: BillingQuota;
}

export interface RecommendPlanRequest {
  apps: number;
  databases: number;
  domains: number;
  members: number;
  storage_gb: number;
}

export interface RecommendPlanResponse {
  recommended: BillingPlanKey;
  reason: string;
}

export type PaymentStatus = "pending" | "succeeded" | "canceled";

export interface Payment {
  id: string;
  plan: BillingPlanKey;
  amount_value: number;
  currency: string;
  status: PaymentStatus;
  created_at: string;
  paid_at: string | null;
  /**
   * YooKassa's confirmation page for a payment still pending. Present only on
   * pending rows: the backend withholds it once a payment is settled, so no
   * surface can offer to pay for something already paid.
   */
  confirmation_url?: string;
}

export interface CheckoutResponse {
  payment_id: string;
  confirmation_url: string;
}

export const billingApi = {
  getPlans: () =>
    apiFetch<{ plans: BillingPlan[] }>("/api/v1/billing/plans"),

  checkout: (projectId: string, plan: BillingPlanKey, autopay = false) =>
    apiFetch<CheckoutResponse>(`/api/v1/projects/${projectId}/billing/checkout`, {
      method: "POST",
      body: { plan, autopay },
    }),

  setAutopay: (projectId: string, enabled: boolean) =>
    apiFetch<{ autopay_enabled: boolean; autopay_method_title: string }>(
      `/api/v1/projects/${projectId}/billing/autopay`,
      { method: "PUT", body: { enabled } },
    ),

  deletePaymentMethod: (projectId: string) =>
    apiFetch<{ autopay_enabled: boolean; autopay_method_title: string }>(
      `/api/v1/projects/${projectId}/billing/payment-method`,
      { method: "DELETE" },
    ),

  payments: (projectId: string) =>
    apiFetch<{ payments: Payment[] }>(`/api/v1/projects/${projectId}/billing/payments`),

  getAccount: (projectId: string) =>
    apiFetch<BillingAccount>(`/api/v1/projects/${projectId}/billing/account`),

  getUsage: (projectId: string) =>
    apiFetch<{ usage: BillingUsage }>(`/api/v1/projects/${projectId}/billing/usage`),

  recommendPlan: (need: RecommendPlanRequest) =>
    apiFetch<RecommendPlanResponse>("/api/v1/billing/recommend-plan", {
      method: "POST",
      body: need,
    }),

  assignPlan: (projectId: string, plan: BillingPlanKey) =>
    apiFetch<{ plan: BillingPlanKey }>(`/api/v1/projects/${projectId}/billing/plan`, {
      method: "PUT",
      body: { plan },
    }),

  consumption: (projectId: string) =>
    apiFetch<ConsumptionResponse>(`/api/v1/projects/${projectId}/billing/consumption`),

  accountSummary: () =>
    apiFetch<AccountSummary>("/api/v1/billing/account/summary"),
};

/** One metered resource row in the consumption estimate. */
export interface ConsumptionResource {
  kind: "app" | "database" | "storage" | "dns";
  name: string;
  cpu_cores: number | null;
  ram_gb: number | null;
  storage_gb: number | null;
  cost_rub: number;
  basis: "actual" | "estimate";
}

/**
 * Read-only money-equivalent estimate for a project's real resource usage over
 * a period. Informational ("estimated at our rates"), not an issued invoice.
 */
export interface ConsumptionResponse {
  period: { start: string; end: string };
  currency: "RUB";
  total_rub: number;
  resources: ConsumptionResource[];
}

/** One countable resource: how much of the plan's allowance is already used. */
export interface QuotaUsage {
  used: number;
  limit: number;
}

/**
 * Account-wide spend snapshot for the top-bar spend widget.
 *
 * `quotas` is absent for orgs the plan ladder does not apply to (billing
 * disabled, or an exempt platform org) — those must not be shown a free-tier
 * meter they are not subject to. `quota_grace_until` is the date the
 * grandfathering window closes; null once it has passed.
 */
export interface AccountSummary {
  currency: "RUB";
  plan: string;
  period_spend_rub: number;
  balance_rub: number;
  quotas?: Record<string, QuotaUsage> | null;
  quota_grace_until?: string | null;
  quota_exempt?: boolean;
}

export interface PaymentsConnectResponse {
  authorize_url: string;
}

export interface PaymentsWebhook {
  id: string;
  event: string;
}

export interface PaymentsConnection {
  status: string;
  account_id: string | null;
  expires_at: string | null;
  webhooks: PaymentsWebhook[];
  webhook_note: string | null;
  env_keys: string[];
  connected_at: string | null;
}

export const paymentsApi = {
  connect: (projectId: string, envId: string, appName: string) =>
    apiFetch<PaymentsConnectResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/payments/connect`,
      { method: "POST" }
    ),

  get: (projectId: string, envId: string, appName: string) =>
    apiFetch<PaymentsConnection>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/payments`
    ),

  disconnect: (projectId: string, envId: string, appName: string) =>
    apiFetch<void>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/payments`,
      { method: "DELETE" }
    ),
};

// Inference proxy is intentionally NOT in apiFetch (which forces JSON):
// the playground needs to send multipart and receive arbitrary content types.
// Returns the raw Response so callers can decide how to interpret the body.
export async function callInference(
  projectId: string,
  envId: string,
  name: string,
  body: BodyInit,
  contentType?: string,
): Promise<Response> {
  const token = typeof window !== "undefined" ? await getToken() : null;
  const headers: Record<string, string> = {};
  if (token) headers["Authorization"] = `Bearer ${token}`;
  if (contentType) headers["Content-Type"] = contentType;
  return fetch(
    `${API_BASE_URL}/api/v1/projects/${projectId}/environments/${envId}/models/${name}/infer`,
    { method: "POST", headers, body },
  );
}
