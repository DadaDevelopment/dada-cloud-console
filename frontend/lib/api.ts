// API client — fetch wrapper that attaches the JWT Authorization header.

import type {
  LoginResponse,
  User,
  ProjectsResponse,
  ProjectDetailResponse,
  OperationsResponse,
  Operation,
  DatabasesResponse,
  CreateDatabaseResponse,
  AppsResponse,
  CreateAppResponse,
  DeployImageResponse,
  EndpointsResponse,
  CreateEndpointResponse,
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
} from "./types";

// Empty string → relative URLs → requests go through the ingress proxy.
// Override with NEXT_PUBLIC_API_URL at build time only for non-prod targets.
const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL ?? "";

function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return localStorage.getItem("dada_token");
}

type RequestOptions = {
  method?: string;
  body?: unknown;
  token?: string;
};

export async function apiFetch<T>(
  path: string,
  options: RequestOptions = {}
): Promise<T> {
  const { method = "GET", body, token } = options;

  const bearerToken = token ?? getToken();

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };

  if (bearerToken) {
    headers["Authorization"] = `Bearer ${bearerToken}`;
  }

  const res = await fetch(`${API_BASE_URL}${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error((err as { error: string }).error ?? "API error");
  }

  return res.json() as Promise<T>;
}

// Convenience helpers
export const api = {
  get: <T>(path: string, token?: string) =>
    apiFetch<T>(path, { method: "GET", token }),

  post: <T>(path: string, body: unknown, token?: string) =>
    apiFetch<T>(path, { method: "POST", body, token }),

  put: <T>(path: string, body: unknown, token?: string) =>
    apiFetch<T>(path, { method: "PUT", body, token }),

  delete: <T>(path: string, token?: string) =>
    apiFetch<T>(path, { method: "DELETE", token }),
};

// Typed API functions
export const authApi = {
  login: (username: string, password: string) =>
    apiFetch<LoginResponse>("/api/v1/auth/login", { method: "POST", body: { username, password } }),
  me: () => apiFetch<{ user: User }>("/api/v1/auth/me"),
};

export const projectsApi = {
  list: () => apiFetch<ProjectsResponse>("/api/v1/projects"),
  get: (id: string) => apiFetch<ProjectDetailResponse>(`/api/v1/projects/${id}`),
  operations: (projectId: string) => apiFetch<OperationsResponse>(`/api/v1/projects/${projectId}/operations`),
  getOperation: (projectId: string, opId: string) =>
    apiFetch<{ operation: Operation }>(`/api/v1/projects/${projectId}/operations/${opId}`),
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
};

export const appsApi = {
  list: (projectId: string, envId: string) =>
    apiFetch<AppsResponse>(`/api/v1/projects/${projectId}/environments/${envId}/apps`),

  create: (projectId: string, envId: string, data: {
    name: string;
    image: string;
    port: number;
    replicas: number;
    profile: string;
  }) =>
    apiFetch<CreateAppResponse>(`/api/v1/projects/${projectId}/environments/${envId}/apps`, {
      method: "POST",
      body: data,
    }),

  updateImage: (projectId: string, envId: string, appName: string, image: string) =>
    apiFetch<DeployImageResponse>(
      `/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/image`,
      { method: "PATCH", body: { image } }
    ),
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

// AI Studio (v2). Routes only resolve when AI_STUDIO_ENABLED on the backend.
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
  const token = typeof window !== "undefined" ? localStorage.getItem("dada_token") : null;
  const headers: Record<string, string> = {};
  if (token) headers["Authorization"] = `Bearer ${token}`;
  if (contentType) headers["Content-Type"] = contentType;
  return fetch(
    `${API_BASE_URL}/api/v1/projects/${projectId}/environments/${envId}/models/${name}/infer`,
    { method: "POST", headers, body },
  );
}
