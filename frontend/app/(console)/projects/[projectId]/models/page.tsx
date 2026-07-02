"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { aiModelsApi, quotasApi } from "@/lib/api";
import type {
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
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { useT } from "@/lib/i18n/console/context";

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
  const { t } = useT();

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";
  const [models, setModels] = useState<ResourceSnapshot[]>([]);
  const [quotas, setQuotas] = useState<QuotaUsageResponse | null>(null);
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
    quotasApi.get(projectId).then(setQuotas).catch(() => setQuotas(null));
  }, [projectId]);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!selectedEnvId) {
      if (!isLoadingEnvs) setIsLoadingModels(false);
      return;
    }
    setIsLoadingModels(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    aiModelsApi
      .list(projectId, selectedEnvId)
      .then((data) => setModels(data.models ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : t("models.error.load")))
      .finally(() => setIsLoadingModels(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, selectedEnvId, isLoadingEnvs]);

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
      // Redirect immediately to the live-updating, highlighted operation.
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("models.error.create"));
    } finally {
      setIsSubmitting(false);
    }
  }

  const canDeploy = canMutate(role);
  const selectedProfile = PROFILES.find((p) => p.name === form.profile);
  const gpuRequiresApproval = !!selectedProfile?.gpu && (quotas?.quotas?.gpu_model_max ?? 0) === 0;

  if (isLoadingEnvs) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.models") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("models.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("models.subtitle")}</p>
        </div>
        {canDeploy && (
        <button
          onClick={() => setIsModalOpen(true)}
          disabled={!selectedEnvId}
          className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          {t("models.deploy")}
        </button>
        )}
      </div>

      {quotas && (
        <div className="mb-6 grid gap-3 sm:grid-cols-3">
          <QuotaCard label={t("models.quota.cpuModels")} used={quotas.cpu_models_in_use} max={quotas.quotas.cpu_model_max} />
          <QuotaCard label={t("models.quota.gpuModels")} used={quotas.gpu_models_in_use} max={quotas.quotas.gpu_model_max} />
          <QuotaCard
            label={t("models.quota.inferenceCalls")}
            used={quotas.inference_calls_month}
            max={quotas.quotas.monthly_inference_calls}
            advisory
          />
        </div>
      )}

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {isLoadingModels ? (
        <div className="flex h-40 items-center justify-center"><Spinner /></div>
      ) : models.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300 dark:text-gray-700" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
          </svg>
          <p className="text-sm font-medium text-gray-500 dark:text-gray-400">{t("models.empty.title", { env: selectedEnv?.name ?? "" })}</p>
          <button
            onClick={() => setIsModalOpen(true)}
            className="mt-4 text-sm text-blue-600 hover:text-blue-700"
          >
            {t("models.empty.deploy")}
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
                className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm hover:border-blue-200 hover:shadow-md transition-all"
              >
                <div className="mb-3 flex items-start justify-between gap-2">
                  <div className="min-w-0 flex-1">
                    <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100 truncate">{m.name}</p>
                    <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">v{s.version ?? "—"}</p>
                  </div>
                  <PhaseBadge phase={m.phase} />
                </div>
                {/* Scannable card: type + GPU only. Profile/auth/canary live on
                    the detail page to keep the grid low-density. */}
                <div className="flex flex-wrap items-center gap-2">
                  <Pill color="indigo">{s.model_type}</Pill>
                  {s.profile?.startsWith("gpu") && <Pill color="purple">GPU</Pill>}
                  {typeof s.canary_percent === "number" && s.canary_percent > 0 && (
                    <Pill color="gray">canary {s.canary_percent}%</Pill>
                  )}
                </div>
                <p className="mt-3 text-xs text-gray-400 dark:text-gray-500">{t("models.card.synced", { ago: timeAgo(m.last_synced_at) })}</p>
              </Link>
            );
          })}
        </div>
      )}

      <Modal
        isOpen={isModalOpen}
        onClose={() => { setIsModalOpen(false); setSubmitError(null); }}
        title={t("models.modal.title")}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("models.form.name.label")}
            </label>
            <input
              type="text" required value={form.name}
              onChange={(e) => update("name", e.target.value)}
              placeholder="iris-classifier"
              pattern="[a-z0-9-]+"
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.form.modelType.label")}</label>
            <select
              value={form.model_type}
              onChange={(e) => update("model_type", e.target.value as AIModelType)}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              {MODEL_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
            </select>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.form.source.label")}</label>
            <select
              value={form.source}
              onChange={(e) => update("source", e.target.value as AIModelSource)}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="s3">{t("models.form.source.s3")}</option>
              <option value="mlflow">{t("models.form.source.mlflow")}</option>
              <option value="custom">{t("models.form.source.custom")}</option>
            </select>
          </div>

          {form.source === "s3" && (
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.form.artifactUri.label")}</label>
              <input
                type="text" required value={form.artifact_uri}
                onChange={(e) => update("artifact_uri", e.target.value)}
                placeholder="s3://platform-models/<project>/iris/v1"
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
              <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("models.form.artifactUri.help")}</p>
            </div>
          )}

          {form.source === "mlflow" && (
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.form.mlflowName.label")}</label>
                <input
                  type="text" required value={form.mlflow_name}
                  onChange={(e) => update("mlflow_name", e.target.value)}
                  placeholder="iris"
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.form.mlflowVersion.label")}</label>
                <input
                  type="text" required value={form.mlflow_version}
                  onChange={(e) => update("mlflow_version", e.target.value)}
                  placeholder="3"
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
            </div>
          )}

          {form.source === "custom" && (
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.form.containerImage.label")}</label>
              <input
                type="text" required value={form.container_image}
                onChange={(e) => update("container_image", e.target.value)}
                placeholder="ghcr.io/org/runner:1.0"
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          )}

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.form.profile.label")}</label>
            <select
              value={form.profile}
              onChange={(e) => update("profile", e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              {PROFILES.map((p) => <option key={p.name} value={p.name}>{p.label}</option>)}
            </select>
            {gpuRequiresApproval && (
              <p className="mt-1 text-xs text-amber-700 dark:text-amber-300">
                {t("models.form.profile.gpuApproval")}
              </p>
            )}
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.form.authMode.label")}</label>
            <select
              value={form.auth_mode}
              onChange={(e) => update("auth_mode", e.target.value as AIModelAuthMode)}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="apikey">{t("models.form.authMode.apikey")}</option>
              <option value="jwt">{t("models.form.authMode.jwt")}</option>
              <option value="public">{t("models.form.authMode.public")}</option>
            </select>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
                {t("models.form.attachedApp.label")} <span className="font-normal text-gray-400 dark:text-gray-500">{t("common.optional")}</span>
              </label>
              <input
                type="text" value={form.attached_app_name}
                onChange={(e) => update("attached_app_name", e.target.value)}
                placeholder="my-api"
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
                {t("models.form.versionLabel.label")} <span className="font-normal text-gray-400 dark:text-gray-500">{t("common.optional")}</span>
              </label>
              <input
                type="text" value={form.version}
                onChange={(e) => update("version", e.target.value)}
                placeholder="v1"
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          {submitError && (
            <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              {submitError}
            </div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => { setIsModalOpen(false); setSubmitError(null); }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit" disabled={isSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? <><Spinner size="sm" /> {t("models.form.deploying")}</> : t("models.form.deploy")}
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
  const { t } = useT();
  const pct = max > 0 ? Math.min(100, (used / max) * 100) : 0;
  const over = max > 0 && used > max;
  const barColor = over ? "bg-red-500" : pct > 80 ? "bg-amber-500" : "bg-blue-500";
  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 shadow-sm">
      <div className="flex items-center justify-between">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
          {label}
          {advisory && <span className="ml-1 normal-case text-amber-600 dark:text-amber-400">{t("models.quota.advisory")}</span>}
        </p>
        <p className="text-sm font-semibold text-gray-900 dark:text-gray-100 font-mono">
          {used.toLocaleString()} / {max.toLocaleString()}
        </p>
      </div>
      <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
        <div className={`h-full ${barColor}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

function Pill({ children, color }: { children: React.ReactNode; color: "indigo" | "purple" | "gray" | "amber" }) {
  const tones = {
    indigo: "bg-indigo-50 dark:bg-indigo-950/40 text-indigo-700 dark:text-indigo-300",
    purple: "bg-purple-50 dark:bg-purple-950/40 text-purple-700 dark:text-purple-300",
    gray: "bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400",
    amber: "bg-amber-50 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300",
  };
  return (
    <span className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${tones[color]}`}>
      {children}
    </span>
  );
}
