"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { projectsApi, aiModelsApi, quotasApi } from "@/lib/api";
import type {
  Environment,
  ResourceSnapshot,
  AIModelSummary,
  AIModelType,
  AIModelAuthMode,
  AIModelSource,
  CreateAIModelRequest,
  QuotaUsageResponse,
} from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";

const MODEL_TYPES: AIModelType[] = [
  "sklearn", "xgboost", "lightgbm",
  "pytorch", "tensorflow", "triton",
  "huggingface", "custom",
];

const PROFILES = [
  { name: "cpu-small", label: "cpu-small (1 CPU, 2Gi)", gpu: false },
  { name: "cpu-medium", label: "cpu-medium (2 CPU, 4Gi)", gpu: false },
  { name: "gpu-t4", label: "gpu-t4 (4 CPU, 16Gi, 1×T4)", gpu: true },
  { name: "gpu-a100", label: "gpu-a100 (8 CPU, 32Gi, 1×A100)", gpu: true },
];

function timeAgo(dateStr: string): string {
  const diffSecs = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
  if (diffSecs < 60) return `${diffSecs}s ago`;
  const diffMins = Math.floor(diffSecs / 60);
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  return `${Math.floor(diffHours / 24)}d ago`;
}

function PhaseBadge({ phase }: { phase?: string }) {
  const p = phase ?? "";
  const lower = p.toLowerCase();
  const tone = lower === "ready"
    ? "bg-green-100 text-green-700"
    : lower === "failed"
      ? "bg-red-100 text-red-700"
      : lower === "waitingforapproval"
        ? "bg-amber-100 text-amber-700"
        : "bg-yellow-100 text-yellow-700";
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${tone}`}>
      {p || "Unknown"}
    </span>
  );
}

function CanaryBar({ percent }: { percent: number }) {
  const pct = Math.max(0, Math.min(100, percent));
  return (
    <div className="mt-2">
      <div className="flex items-center justify-between text-xs text-gray-500">
        <span>canary</span>
        <span className="font-mono">{pct}%</span>
      </div>
      <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-100">
        <div className="h-full bg-purple-500" style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

interface CreateForm {
  name: string;
  source: AIModelSource;
  mlflow_name: string;
  mlflow_version: string;
  artifact_uri: string;
  container_image: string;
  model_type: AIModelType;
  profile: string;
  auth_mode: AIModelAuthMode;
  attached_app_name: string;
  version: string;
}

const defaultForm: CreateForm = {
  name: "",
  source: "s3",
  mlflow_name: "",
  mlflow_version: "",
  artifact_uri: "",
  container_image: "",
  model_type: "sklearn",
  profile: "cpu-small",
  auth_mode: "apikey",
  attached_app_name: "",
  version: "",
};

export default function ModelsPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const router = useRouter();
  const search = useSearchParams();

  const [environments, setEnvironments] = useState<Environment[]>([]);
  const [selectedEnvId, setSelectedEnvId] = useState<string>("");
  const [models, setModels] = useState<ResourceSnapshot[]>([]);
  const [quotas, setQuotas] = useState<QuotaUsageResponse | null>(null);
  const [isLoadingEnvs, setIsLoadingEnvs] = useState(true);
  const [isLoadingModels, setIsLoadingModels] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(() => Boolean(search.get("fromMlflow")));
  // Prefill from MLflow registry when arriving via "Deploy this version" link.
  // Reading search params in a useState initializer fires once on mount and
  // satisfies react-hooks/set-state-in-effect.
  const [form, setForm] = useState<CreateForm>(() => {
    const fromMlflow = search.get("fromMlflow");
    if (!fromMlflow) return defaultForm;
    const fromMlflowVersion = search.get("fromMlflowVersion");
    return {
      ...defaultForm,
      source: "mlflow",
      mlflow_name: fromMlflow,
      mlflow_version: fromMlflowVersion ?? "",
      name: fromMlflow.replace(/[^a-z0-9-]/g, "-").toLowerCase(),
    };
  });
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  useEffect(() => {
    Promise.all([projectsApi.get(projectId), quotasApi.get(projectId).catch(() => null)])
      .then(([detail, q]) => {
        const envs = detail.environments ?? [];
        setEnvironments(envs);
        setQuotas(q);
        if (envs.length > 0) setSelectedEnvId(envs[0].id);
        else setIsLoadingModels(false);
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : "Failed to load project");
        setIsLoadingModels(false);
      })
      .finally(() => setIsLoadingEnvs(false));
  }, [projectId]);

  useEffect(() => {
    if (!selectedEnvId) return;
    aiModelsApi
      .list(projectId, selectedEnvId)
      .then((data) => setModels(data.models ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load models"))
      .finally(() => setIsLoadingModels(false));
  }, [projectId, selectedEnvId]);

  function handleEnvironmentChange(envId: string) {
    setIsLoadingModels(true);
    setError(null);
    setSelectedEnvId(envId);
  }

  function update<K extends keyof CreateForm>(k: K, v: CreateForm[K]) {
    setForm((prev) => ({ ...prev, [k]: v }));
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const body: CreateAIModelRequest = {
        name: form.name,
        model_type: form.model_type,
        source: form.source,
        profile: form.profile,
        auth_mode: form.auth_mode,
      };
      if (form.source === "mlflow") {
        body.mlflow_name = form.mlflow_name;
        body.mlflow_version = form.mlflow_version;
      } else if (form.source === "s3") {
        body.artifact_uri = form.artifact_uri;
      } else if (form.source === "custom") {
        body.container_image = form.container_image;
      }
      if (form.attached_app_name.trim()) body.attached_app_name = form.attached_app_name.trim();
      if (form.version.trim()) body.version = form.version.trim();

      const result = await aiModelsApi.create(projectId, selectedEnvId, body);
      setIsModalOpen(false);
      setForm(defaultForm);
      const opId = result.operation?.id;
      setTimeout(() => {
        router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
      }, 1500);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Failed to create model");
    } finally {
      setIsSubmitting(false);
    }
  }

  const selectedEnv = environments.find((e) => e.id === selectedEnvId);
  const selectedProfile = PROFILES.find((p) => p.name === form.profile);
  const gpuRequiresApproval = !!selectedProfile?.gpu && (quotas?.quotas?.gpu_model_max ?? 0) === 0;

  if (isLoadingEnvs) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }

  return (
    <div>
      <div className="mb-8 flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Link href="/projects" className="hover:text-gray-700">Projects</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}`} className="hover:text-gray-700">Overview</Link>
            <span>/</span>
            <span className="text-gray-900">AI Models</span>
          </div>
          <h1 className="mt-2 text-2xl font-bold text-gray-900">AI Models</h1>
          <p className="mt-0.5 text-sm text-gray-500">KServe-backed inference services</p>
        </div>
        <button
          onClick={() => setIsModalOpen(true)}
          disabled={!selectedEnvId}
          className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          Deploy Model
        </button>
      </div>

      {quotas && (
        <div className="mb-6 grid gap-3 sm:grid-cols-3">
          <QuotaCard label="CPU models" used={quotas.cpu_models_in_use} max={quotas.quotas.cpu_model_max} />
          <QuotaCard label="GPU models" used={quotas.gpu_models_in_use} max={quotas.quotas.gpu_model_max} />
          <QuotaCard
            label="Inference calls / month"
            used={quotas.inference_calls_month}
            max={quotas.quotas.monthly_inference_calls}
            advisory
          />
        </div>
      )}

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {environments.length > 0 && (
        <div className="mb-6 flex w-fit gap-1 rounded-lg border border-gray-200 bg-gray-50 p-1">
          {environments.map((env) => (
            <button
              key={env.id}
              onClick={() => handleEnvironmentChange(env.id)}
              className={`rounded-md px-4 py-1.5 text-sm font-medium transition-colors ${
                selectedEnvId === env.id
                  ? "bg-white text-gray-900 shadow-sm"
                  : "text-gray-500 hover:text-gray-700"
              }`}
            >
              {env.name}
            </button>
          ))}
        </div>
      )}

      {isLoadingModels ? (
        <div className="flex h-40 items-center justify-center"><Spinner /></div>
      ) : models.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
          </svg>
          <p className="text-sm font-medium text-gray-500">No models in {selectedEnv?.name ?? "this environment"}</p>
          <button
            onClick={() => setIsModalOpen(true)}
            className="mt-4 text-sm text-blue-600 hover:text-blue-700"
          >
            Deploy your first model →
          </button>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {models.map((m) => {
            const s = m.summary_json as unknown as AIModelSummary;
            return (
              <Link
                key={m.id}
                href={`/projects/${projectId}/models/${m.name}?envId=${selectedEnvId}`}
                className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm hover:border-blue-200 hover:shadow-md transition-all"
              >
                <div className="mb-3 flex items-start justify-between gap-2">
                  <div className="min-w-0 flex-1">
                    <p className="font-mono text-sm font-semibold text-gray-900 truncate">{m.name}</p>
                    <p className="mt-0.5 text-xs text-gray-400">v{s.version ?? "—"}</p>
                  </div>
                  <PhaseBadge phase={m.phase} />
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Pill color="indigo">{s.model_type}</Pill>
                  <Pill color={s.profile?.startsWith("gpu") ? "purple" : "gray"}>{s.profile}</Pill>
                  <Pill color={s.auth_mode === "public" ? "amber" : "gray"}>{s.auth_mode}</Pill>
                </div>
                {typeof s.canary_percent === "number" && s.canary_percent > 0 && (
                  <CanaryBar percent={s.canary_percent} />
                )}
                <p className="mt-3 text-xs text-gray-400">Synced {timeAgo(m.last_synced_at)}</p>
              </Link>
            );
          })}
        </div>
      )}

      <Modal
        isOpen={isModalOpen}
        onClose={() => { setIsModalOpen(false); setSubmitError(null); }}
        title="Deploy AI Model"
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">
              Name <span className="font-normal text-gray-400">(k8s resource name)</span>
            </label>
            <input
              type="text" required value={form.name}
              onChange={(e) => update("name", e.target.value)}
              placeholder="iris-classifier"
              pattern="[a-z0-9-]+"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">Model type</label>
            <select
              value={form.model_type}
              onChange={(e) => update("model_type", e.target.value as AIModelType)}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              {MODEL_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
            </select>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">Source</label>
            <select
              value={form.source}
              onChange={(e) => update("source", e.target.value as AIModelSource)}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="s3">S3 artifact URI</option>
              <option value="mlflow">MLflow registered model</option>
              <option value="custom">Custom container image</option>
            </select>
          </div>

          {form.source === "s3" && (
            <div>
              <label className="block text-sm font-medium text-gray-700">Artifact URI</label>
              <input
                type="text" required value={form.artifact_uri}
                onChange={(e) => update("artifact_uri", e.target.value)}
                placeholder="s3://platform-models/<project>/iris/v1"
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
              <p className="mt-1 text-xs text-gray-400">Must start with this project&apos;s storage prefix.</p>
            </div>
          )}

          {form.source === "mlflow" && (
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-sm font-medium text-gray-700">Registered name</label>
                <input
                  type="text" required value={form.mlflow_name}
                  onChange={(e) => update("mlflow_name", e.target.value)}
                  placeholder="iris"
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700">Version</label>
                <input
                  type="text" required value={form.mlflow_version}
                  onChange={(e) => update("mlflow_version", e.target.value)}
                  placeholder="3"
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
            </div>
          )}

          {form.source === "custom" && (
            <div>
              <label className="block text-sm font-medium text-gray-700">Container image</label>
              <input
                type="text" required value={form.container_image}
                onChange={(e) => update("container_image", e.target.value)}
                placeholder="ghcr.io/org/runner:1.0"
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          )}

          <div>
            <label className="block text-sm font-medium text-gray-700">Compute profile</label>
            <select
              value={form.profile}
              onChange={(e) => update("profile", e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              {PROFILES.map((p) => <option key={p.name} value={p.name}>{p.label}</option>)}
            </select>
            {gpuRequiresApproval && (
              <p className="mt-1 text-xs text-amber-700">
                ⓘ GPU profile requires admin approval — this op will be parked in <span className="font-mono">WaitingForApproval</span>.
              </p>
            )}
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">Auth mode</label>
            <select
              value={form.auth_mode}
              onChange={(e) => update("auth_mode", e.target.value as AIModelAuthMode)}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="apikey">api-key (default)</option>
              <option value="jwt">platform-jwt</option>
              <option value="public">public (admin only)</option>
            </select>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700">
                Attached app <span className="font-normal text-gray-400">(optional)</span>
              </label>
              <input
                type="text" value={form.attached_app_name}
                onChange={(e) => update("attached_app_name", e.target.value)}
                placeholder="my-api"
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">
                Version label <span className="font-normal text-gray-400">(optional)</span>
              </label>
              <input
                type="text" value={form.version}
                onChange={(e) => update("version", e.target.value)}
                placeholder="v1"
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          {submitError && (
            <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
              {submitError}
            </div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => { setIsModalOpen(false); setSubmitError(null); }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit" disabled={isSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? <><Spinner size="sm" /> Deploying...</> : "Deploy"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

function QuotaCard({
  label, used, max, advisory,
}: { label: string; used: number; max: number; advisory?: boolean }) {
  const pct = max > 0 ? Math.min(100, (used / max) * 100) : 0;
  const over = max > 0 && used > max;
  const barColor = over ? "bg-red-500" : pct > 80 ? "bg-amber-500" : "bg-blue-500";
  return (
    <div className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
      <div className="flex items-center justify-between">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">
          {label}
          {advisory && <span className="ml-1 normal-case text-amber-600">(advisory)</span>}
        </p>
        <p className="text-sm font-semibold text-gray-900 font-mono">
          {used.toLocaleString()} / {max.toLocaleString()}
        </p>
      </div>
      <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-gray-100">
        <div className={`h-full ${barColor}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

function Pill({ children, color }: { children: React.ReactNode; color: "indigo" | "purple" | "gray" | "amber" }) {
  const tones = {
    indigo: "bg-indigo-50 text-indigo-700",
    purple: "bg-purple-50 text-purple-700",
    gray: "bg-gray-100 text-gray-600",
    amber: "bg-amber-50 text-amber-700",
  };
  return (
    <span className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${tones[color]}`}>
      {children}
    </span>
  );
}
