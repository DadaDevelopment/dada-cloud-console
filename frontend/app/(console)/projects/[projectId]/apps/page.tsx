"use client";
import { useEffect, useRef, useState, FormEvent } from "react";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { appsApi } from "@/lib/api";
import { docsHref } from "@/lib/site";
import type { ResourceSnapshot, AppSummary, InfraSummary, Environment } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { isSettling } from "@/lib/phase";
import { timeAgo } from "@/lib/format";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { MetricSparkline } from "@/components/metrics/fixed-metrics-dashboard";
import { EmptyState } from "@/components/ui/empty-state";
import { DeployChooser } from "@/components/deploy/deploy-chooser";
import { TemplateDeployCards } from "@/components/console/template-deploy-cards";
import { UploadDeployCard } from "@/components/deploy/upload-deploy";
import { useT } from "@/lib/i18n/console/context";
import { QuotaUpsell } from "@/components/billing/quota-upsell";
import { Globe, Database, GitPullRequest, ChevronDown } from "lucide-react";
import { AppPreviewPane } from "@/components/app-preview-pane";
import { LogsViewer } from "@/components/logs-viewer";
import { classifyVMResource, extractIngressSpec, extractDatabaseSpec } from "@/lib/vm-resources";
import { getAppAlerts, hasAlertType, type AppAlert } from "@/lib/app-alerts";

interface CreateAppForm {
  name: string;
  image: string;
  port: number;
  replicas: number;
  workloadType: string;
  worker: boolean;
}

const APP_NAME_RE = /^([a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])$/;
const APP_IMAGE_RE = /^[a-zA-Z0-9][a-zA-Z0-9._\-/:]*:[a-zA-Z0-9._\-]+$/;

/** Bare host of a live-app URL, for a compact link label ("Open <host>"). */
function appHostname(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

export default function AppsPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const router = useRouter();
  const searchParams = useSearchParams();
  const { t } = useT();

  const { project, environments, role, loading: isLoadingEnvs } = useProjectContext();
  const [appsByEnv, setAppsByEnv] = useState<Record<string, ResourceSnapshot[]>>({});
  const [infraByEnv, setInfraByEnv] = useState<Record<string, ResourceSnapshot[]>>({});
  const [isLoadingApps, setIsLoadingApps] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [modalEnvId, setModalEnvId] = useState("");
  const [chooserOpen, setChooserOpen] = useState(false);
  const [chooserEnvId, setChooserEnvId] = useState("");
  const [form, setForm] = useState<CreateAppForm>({
    name: "",
    image: "",
    port: 8080,
    replicas: 1,
    workloadType: "Deployment",
    worker: false,
  });
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [quotaBlocked, setQuotaBlocked] = useState<{ resource: string; limit?: number } | null>(null);

  useEffect(() => {
    if (environments.length === 0) {
      if (!isLoadingEnvs) setIsLoadingApps(false);
      return;
    }
    setIsLoadingApps(true);
    setError(null);
    Promise.all(
      environments.map((env) =>
        appsApi
          .list(projectId, env.id)
          .then((d) => [env.id, d.apps ?? []] as const)
          .catch(() => [env.id, [] as ResourceSnapshot[]] as const)
      )
    )
      .then((entries) => setAppsByEnv(Object.fromEntries(entries)))
      .catch((err) => setError(err instanceof Error ? err.message : t("apps.error.load")))
      .finally(() => setIsLoadingApps(false));
    // Infrastructure (kind='Infra') per env — best-effort; never blocks the apps list.
    Promise.all(
      environments.map((env) =>
        appsApi
          .listInfra(projectId, env.id)
          .then((d) => [env.id, d.infra ?? []] as const)
          .catch(() => [env.id, [] as ResourceSnapshot[]] as const)
      )
    ).then((entries) => setInfraByEnv(Object.fromEntries(entries)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, environments, isLoadingEnvs]);

  useEffect(() => {
    if (environments.length === 0) return;
    if (!isSettling(Object.values(appsByEnv).flat())) return;
    const id = setTimeout(() => {
      Promise.all(
        environments.map((env) =>
          appsApi
            .list(projectId, env.id)
            .then((d) => [env.id, d.apps ?? []] as const)
            .catch(() => [env.id, [] as ResourceSnapshot[]] as const)
        )
      )
        .then((entries) => setAppsByEnv(Object.fromEntries(entries)))
        .catch(() => undefined);
    }, 4000);
    return () => clearTimeout(id);
  }, [appsByEnv, environments, projectId]);

  function handleFormChange(field: keyof CreateAppForm, value: string | number | boolean) {
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  function openCreate(envId: string) {
    setModalEnvId(envId);
    setSubmitError(null);
    setIsModalOpen(true);
  }

  function openChooser(envId: string) {
    setChooserEnvId(envId);
    setChooserOpen(true);
  }

  const deployImageParamHandledRef = useRef(false);

  useEffect(() => {
    if (deployImageParamHandledRef.current) return;
    if (isLoadingEnvs) return;
    if (searchParams.get("deploy") !== "image") return;
    deployImageParamHandledRef.current = true;
    const targetEnvId = searchParams.get("envId") || environments[0]?.id || "";
    if (targetEnvId && canMutate(role)) openCreate(targetEnvId);
  }, [searchParams, isLoadingEnvs, environments, role]);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!APP_NAME_RE.test(form.name.trim()) || !APP_IMAGE_RE.test(form.image.trim())) return;
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const result = await appsApi.create(projectId, modalEnvId, {
        name: form.name,
        image: form.image,
        port: form.port,
        replicas: form.replicas,
        workload_type: form.workloadType,
        worker: form.worker,
      });
      setIsModalOpen(false);
      setForm({ name: "", image: "", port: 8080, replicas: 1, workloadType: "Deployment", worker: false });
      void result;
      router.push(`/projects/${projectId}/apps/${form.name}`);
    } catch (err) {
      const raw = err instanceof Error ? err.message : "";
      const quota = err as { code?: string; resource?: string; limit?: number } | undefined;
      if (quota?.code === "quota_exceeded") {
        setSubmitError(null);
        setQuotaBlocked({ resource: quota.resource ?? "apps", limit: quota.limit });
        return;
      }
      let msg = raw || t("apps.error.create");
      if (/already exists|unique per environment/i.test(raw)) {
        msg = t("apps.error.create.duplicateGlobal");
      } else if (/lowercase alphanumeric with hyphens/i.test(raw)) {
        msg = t("apps.modal.create.name.invalid.format");
      }
      setSubmitError(msg);
    } finally {
      setIsSubmitting(false);
    }
  }

  const alertedApps = Object.entries(appsByEnv).flatMap(([envId, apps]) => {
    const env = environments.find((e) => e.id === envId);
    return apps
      .map((app) => ({ app, env, alerts: getAppAlerts(app) }))
      .filter((row): row is { app: ResourceSnapshot; env: Environment; alerts: AppAlert[] } => row.env != null && row.alerts.length > 0);
  });
  const crashCount = alertedApps.filter((r) => hasAlertType(r.alerts, "crash")).length;
  const volumeCount = alertedApps.filter((r) => hasAlertType(r.alerts, "volume")).length;

  const canCreate = canMutate(role);
  const modalEnv = environments.find((e) => e.id === modalEnvId);
  const modalIsVM = modalEnv?.runtime === "vm";

  const existingNames = new Set((appsByEnv[modalEnvId] ?? []).map((a) => a.name.toLowerCase()));
  const trimmedName = form.name.trim();
  const nameError =
    trimmedName === ""
      ? null
      : !APP_NAME_RE.test(trimmedName)
        ? t("apps.modal.create.name.invalid.format")
        : existingNames.has(trimmedName.toLowerCase())
          ? t("apps.modal.create.name.invalid.duplicate")
          : null;
  const trimmedImage = form.image.trim();
  const imageError = trimmedImage !== "" && !APP_IMAGE_RE.test(trimmedImage) ? t("apps.modal.create.image.invalid") : null;
  const formValid = trimmedName !== "" && !nameError && trimmedImage !== "" && !imageError;

  if (isLoadingEnvs) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-8">
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
            { label: t("nav.apps") },
          ]}
        />
        <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("apps.title")}</h1>
        <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("apps.subtitle")}</p>
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {alertedApps.length > 0 && (
        <div
          className={`mb-6 rounded-lg border px-4 py-3 text-sm ${
            crashCount > 0
              ? "border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 text-red-700 dark:text-red-300"
              : "border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300"
          }`}
        >
          <p className="font-medium">
            {t("apps.alerts.summary.title", { count: alertedApps.length, crash: crashCount, volume: volumeCount })}
          </p>
          <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1">
            {alertedApps.map(({ app, env }) => (
              <Link
                key={`${env.id}-${app.id}`}
                href={`/projects/${projectId}/apps/${app.name}?envId=${env.id}`}
                className="font-mono text-xs font-semibold underline underline-offset-2 hover:opacity-80"
              >
                {app.name}
              </Link>
            ))}
          </div>
        </div>
      )}

      {isLoadingApps ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : (
        <div className="space-y-10">
          {(() => {
            const realEnvs = environments.filter((env) => !env.is_ephemeral);
            const previews = environments
              .filter((pe) => pe.is_ephemeral && pe.pr_number != null)
              .map((pe) => ({
                env: pe,
                url: (appsByEnv[pe.id] ?? [])
                  .map((a) => (a.summary_json as unknown as AppSummary).url)
                  .find(Boolean),
              }));
            const groups = (["cloud", "vm"] as const)
              .map((runtime) => {
                const envs = realEnvs.filter((e) => (e.runtime === "vm") === (runtime === "vm"));
                return {
                  runtime,
                  envs,
                  entries: envs.flatMap((e) => (appsByEnv[e.id] ?? []).map((app) => ({ app, env: e }))),
                  infra: envs.flatMap((e) => infraByEnv[e.id] ?? []),
                };
              })
              .filter((g) => g.envs.length > 0);
            const showTitles = groups.length > 1;
            return groups.map((g) => (
              <GroupBlock
                key={g.runtime}
                title={showTitles ? t(g.runtime === "vm" ? "env.runtime.vm" : "env.runtime.cloud") : undefined}
                primaryEnv={g.envs[0]}
                projectId={projectId}
                entries={g.entries}
                infra={g.infra}
                previews={previews}
                canCreate={canCreate}
                onCreate={(envId) => openChooser(envId)}
                t={t}
              />
            ));
          })()}
        </div>
      )}

      <Modal
        isOpen={isModalOpen}
        onClose={() => {
          setIsModalOpen(false);
          setSubmitError(null);
        }}
        title={t("apps.modal.create.title")}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("apps.modal.create.name.label")}
            </label>
            <input
              type="text"
              required
              value={form.name}
              onChange={(e) => handleFormChange("name", e.target.value)}
              placeholder="my-service"
              aria-invalid={nameError ? true : undefined}
              title={t("apps.modal.create.name.title")}
              className={`mt-1 block w-full rounded-lg border px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:outline-none focus:ring-1 ${
                nameError
                  ? "border-red-400 dark:border-red-700 focus:border-red-500 focus:ring-red-500"
                  : "border-gray-300 dark:border-gray-700 focus:border-blue-500 focus:ring-blue-500"
              }`}
            />
            {nameError ? (
              <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">{nameError}</p>
            ) : (
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("apps.modal.create.name.hint")}</p>
            )}
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.create.image.label")}</label>
            <input
              type="text"
              required
              value={form.image}
              onChange={(e) => handleFormChange("image", e.target.value)}
              placeholder="ghcr.io/org/service:v1.0.0"
              aria-invalid={imageError ? true : undefined}
              className={`mt-1 block w-full rounded-lg border px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:outline-none focus:ring-1 ${
                imageError
                  ? "border-red-400 dark:border-red-700 focus:border-red-500 focus:ring-red-500"
                  : "border-gray-300 dark:border-gray-700 focus:border-blue-500 focus:ring-blue-500"
              }`}
            />
            {imageError && <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">{imageError}</p>}
          </div>

          {!modalIsVM && (
            <div className="rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2.5">
              <label className="flex items-start gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={form.worker}
                  onChange={(e) => handleFormChange("worker", e.target.checked)}
                  className="mt-0.5 h-4 w-4 rounded border-gray-300 dark:border-gray-600 text-blue-600 focus:ring-blue-500"
                />
                <span className="font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.create.worker.label")}</span>
              </label>
              <p className="mt-1 pl-6 text-xs text-gray-500 dark:text-gray-400">{t("apps.modal.create.worker.hint")}</p>
            </div>
          )}

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.create.port.label")}</label>
              <input
                type="number"
                required={!form.worker}
                disabled={form.worker}
                min={1}
                max={65535}
                value={form.port}
                onChange={(e) => handleFormChange("port", parseInt(e.target.value, 10))}
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:opacity-50 disabled:bg-gray-100 dark:disabled:bg-gray-800"
              />
              {form.worker && (
                <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("apps.modal.create.port.workerHint")}</p>
              )}
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.create.replicas.label")}</label>
              <input
                type="number"
                required
                min={1}
                max={10}
                value={form.replicas}
                onChange={(e) => handleFormChange("replicas", parseInt(e.target.value, 10) || 1)}
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.create.workloadType.label")}</label>
            <select
              value={form.workloadType}
              onChange={(e) => handleFormChange("workloadType", e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="Deployment">{t("apps.modal.create.workloadType.deployment")}</option>
              <option value="StatefulSet">{t("apps.modal.create.workloadType.statefulset")}</option>
            </select>
            <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("apps.modal.create.workloadType.hint")}</p>
          </div>

          {quotaBlocked && (
            <QuotaUpsell resource={quotaBlocked.resource} limit={quotaBlocked.limit} projectId={projectId} />
          )}

          {submitError && (
            <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              {submitError}
            </div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => {
                setIsModalOpen(false);
                setSubmitError(null);
              }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 transition-colors"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit"
              disabled={isSubmitting || !formValid}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? (
                <>
                  <Spinner size="sm" />
                  {t("apps.modal.create.submitting")}
                </>
              ) : (
                t("apps.modal.create.submit")
              )}
            </button>
          </div>
        </form>
      </Modal>

      <DeployChooser
        open={chooserOpen}
        onClose={() => setChooserOpen(false)}
        projectId={projectId}
        environments={environments}
        defaultEnvId={chooserEnvId}
        onPickImage={(envId) => openCreate(envId)}
      />
    </div>
  );
}

interface EnvPreviewChip {
  env: Environment;
  url?: string;
}

interface GroupBlockProps {
  title?: string;
  primaryEnv: Environment;
  projectId: string;
  entries: { app: ResourceSnapshot; env: Environment }[];
  infra: ResourceSnapshot[];
  previews: EnvPreviewChip[];
  canCreate: boolean;
  onCreate: (envId: string) => void;
  t: (key: string, vars?: Record<string, string>) => string;
}

function GroupBlock({ title, primaryEnv, projectId, entries, infra, previews, canCreate, onCreate, t }: GroupBlockProps) {
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const isVM = primaryEnv.runtime === "vm";
  return (
    <section>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 dark:border-gray-800 pb-3">
        <div className="flex items-center gap-2">
          {title && <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{title}</h2>}
          <span className="text-xs text-gray-400 dark:text-gray-500">{t("apps.env.count", { count: String(entries.length) })}</span>
        </div>
        {canCreate && (
          <button
            onClick={() => onCreate(primaryEnv.id)}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 active:bg-blue-800 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("apps.deploy.button")}
          </button>
        )}
      </div>

      {entries.length === 0 ? (
        <>
          {!isVM && (
            <div data-onboarding="first-deploy" className="mb-6 grid gap-4 lg:grid-cols-2">
              <TemplateDeployCards projectId={projectId} envId={primaryEnv.id} hero />
              <UploadDeployCard projectId={projectId} envId={primaryEnv.id} hero />
            </div>
          )}
          <EmptyState
            title={isVM ? t("apps.empty.title") : t("apps.empty.k8s.gitTitle")}
            description={isVM ? t("apps.empty.vm.description") : t("apps.empty.k8s.description")}
            action={
              isVM
                ? { label: t("apps.empty.vm.action"), href: `/projects/${projectId}/app-servers` }
                : { label: t("apps.empty.k8s.action"), href: `/projects/${projectId}/git/import?envId=${primaryEnv.id}` }
            }
            secondary={{ label: t("common.learnMore"), href: docsHref("applications-deploy-from-github") }}
          />
        </>
      ) : (
        <div className="flex flex-col gap-3">
          {entries.map(({ app, env }) => (
            <AppRow
              key={app.id}
              app={app}
              env={env}
              projectId={projectId}
              previews={previews}
              expanded={expandedId === app.id}
              onToggle={() => setExpandedId((cur) => (cur === app.id ? null : app.id))}
              t={t}
            />
          ))}
        </div>
      )}

      {infra.length > 0 && (
        <div className="mt-6">
          <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300">{t("apps.infra.title")}</h3>
          <div className="mt-3 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {infra.map((r) => {
              const s = r.summary_json as unknown as InfraSummary;
              return (
                <div
                  key={r.id}
                  className="rounded-xl border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900/60 p-5 shadow-sm"
                >
                  <div className="mb-2 flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{r.name}</p>
                      <p className="mt-0.5 font-mono text-xs text-gray-400 dark:text-gray-500 truncate">{s.image ?? "—"}</p>
                    </div>
                    {s.subtype && (
                      <span className="shrink-0 rounded-full bg-slate-200 dark:bg-slate-700 px-2 py-0.5 text-xs font-medium text-slate-700 dark:text-slate-200">
                        {s.subtype}
                      </span>
                    )}
                  </div>
                  <PhaseBadge phase={r.phase} />
                </div>
              );
            })}
          </div>
        </div>
      )}
    </section>
  );
}

interface AppRowProps {
  app: ResourceSnapshot;
  env: Environment;
  projectId: string;
  previews: EnvPreviewChip[];
  expanded: boolean;
  onToggle: () => void;
  t: (key: string, vars?: Record<string, string>) => string;
}

function AppRow({ app, env, projectId, previews, expanded, onToggle, t }: AppRowProps) {
  const summary = app.summary_json as unknown as AppSummary;
  const resType = classifyVMResource(app);
  const ing = resType === "ingress" ? extractIngressSpec(app) : null;
  const db = resType === "database" ? extractDatabaseSpec(app) : null;
  const appHref = `/projects/${projectId}/apps/${app.name}?envId=${env.id}`;
  const alerts = getAppAlerts(app);
  const hasCrashAlert = hasAlertType(alerts, "crash");
  const hasVolumeAlert = hasAlertType(alerts, "volume");
  const rowPreviews = previews.filter((p) => p.env.name === `pr-${p.env.pr_number}-${app.name}`);
  const subtitle =
    resType === "ingress"
      ? ing?.host ?? summary.image ?? "—"
      : resType === "database"
        ? `${db?.engine ?? ""}${db?.version ? " " + db.version : ""}`
        : summary.image ?? "—";
  return (
    <div
      className={`rounded-xl border bg-white dark:bg-gray-900 shadow-sm transition-all ${
        expanded ? "border-blue-200 dark:border-blue-900 shadow-md" : "border-gray-200 dark:border-gray-800 hover:border-blue-200 hover:shadow-md"
      }`}
    >
      <div
        role="button"
        tabIndex={0}
        onClick={onToggle}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onToggle();
          }
        }}
        className="flex w-full cursor-pointer flex-wrap items-center gap-x-6 gap-y-3 px-5 py-4 text-left"
      >
        <div className="flex min-w-0 flex-1 basis-60 items-center gap-3">
          {resType === "ingress" && (
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
              <Globe className="h-4 w-4" />
            </span>
          )}
          {resType === "database" && (
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-violet-50 dark:bg-violet-950/40 text-violet-600 dark:text-violet-400">
              <Database className="h-4 w-4" />
            </span>
          )}
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100 truncate">{app.name}</p>
              <PhaseBadge phase={app.phase} />
              {hasCrashAlert && (
                <span className="inline-flex items-center rounded-full bg-red-100 dark:bg-red-950/60 px-2 py-0.5 text-xs font-medium text-red-700 dark:text-red-300">
                  {t("apps.alerts.chip.crash")}
                </span>
              )}
              {hasVolumeAlert && (
                <span className="inline-flex items-center rounded-full bg-amber-100 dark:bg-amber-950/60 px-2 py-0.5 text-xs font-medium text-amber-700 dark:text-amber-300">
                  {t("apps.alerts.chip.volume")}
                </span>
              )}
              {rowPreviews.map((p) =>
                p.url ? (
                  <a
                    key={p.env.id}
                    href={p.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(e) => e.stopPropagation()}
                    className="inline-flex items-center gap-1 rounded-full bg-purple-50 dark:bg-purple-950/40 px-2 py-0.5 text-xs font-medium text-purple-700 dark:text-purple-300 hover:bg-purple-100 dark:hover:bg-purple-950/60 transition-colors"
                  >
                    <GitPullRequest className="h-3 w-3" />
                    {t("apps.card.prPreview", { pr: String(p.env.pr_number) })}
                  </a>
                ) : (
                  <span
                    key={p.env.id}
                    className="inline-flex items-center gap-1 rounded-full bg-purple-50 dark:bg-purple-950/40 px-2 py-0.5 text-xs font-medium text-purple-700 dark:text-purple-300"
                  >
                    <GitPullRequest className="h-3 w-3" />
                    {t("apps.card.prPreview", { pr: String(p.env.pr_number) })}
                  </span>
                ),
              )}
            </div>
            <p className="mt-0.5 truncate font-mono text-xs text-gray-400 dark:text-gray-500">{subtitle}</p>
          </div>
        </div>

        <div className="hidden lg:flex items-center gap-3 text-xs text-gray-400 dark:text-gray-500">
          {resType === "ingress" ? (
            <span>{t("resources.card.routes", { count: String(ing?.rules?.length ?? 0) })}</span>
          ) : resType === "database" ? (
            <span className="max-w-40 truncate font-mono">{db?.volume || db?.database || "—"}</span>
          ) : (
            <>
              <span>{summary.resources ? `${summary.resources.cpu_limit} CPU · ${summary.resources.memory_limit}` : "—"}</span>
              <span>·</span>
              <span>{t("apps.card.replicas", { count: String(summary.replicas ?? 1) })}</span>
            </>
          )}
          <span>·</span>
          <span>{t("apps.card.synced", { ago: timeAgo(app.last_synced_at) })}</span>
        </div>

        <div className="hidden xl:block">
          <MetricSparkline projectId={projectId} envId={env.id} appName={app.name} />
        </div>

        <div className="flex shrink-0 items-center gap-2" onClick={(e) => e.stopPropagation()}>
          {summary.url && (
            <a
              href={summary.url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 dark:border-gray-800 px-3 py-1.5 text-xs font-medium text-blue-600 dark:text-blue-400 hover:border-blue-300 transition-colors"
            >
              <Globe className="h-3.5 w-3.5 shrink-0" />
              {t("apps.card.openUrl", { hostname: appHostname(summary.url) })}
            </a>
          )}
          <Link
            href={appHref}
            className="rounded-lg border border-gray-200 dark:border-gray-800 px-3 py-1.5 text-xs font-medium text-gray-600 dark:text-gray-300 hover:border-blue-300 hover:text-blue-600 transition-colors"
          >
            {t("apps.card.settings")}
          </Link>
          <span
            className={`flex h-7 w-7 items-center justify-center rounded-lg text-gray-400 dark:text-gray-500 transition-transform ${expanded ? "rotate-180" : ""}`}
          >
            <ChevronDown className="h-4 w-4" />
          </span>
        </div>
      </div>

      {expanded && (
        <div className="space-y-4 border-t border-gray-100 dark:border-gray-800 p-4">
          <div className="grid gap-4 xl:grid-cols-[1fr_320px]">
            <div className="min-w-0">
              {summary.url ? (
                <AppPreviewPane key={summary.preview_url ?? summary.url} url={summary.preview_url ?? summary.url} openUrl={summary.url} title={app.name} />
              ) : (
                <div className="flex h-40 items-center justify-center rounded-lg border border-dashed border-gray-200 dark:border-gray-800 text-sm text-gray-400 dark:text-gray-500">
                  {t("apps.row.noUrl")}
                </div>
              )}
            </div>
            <div className="rounded-xl border border-gray-100 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 p-4">
              <dl className="space-y-3 text-sm">
                <div>
                  <dt className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("apps.detail.spec.image")}</dt>
                  <dd className="mt-0.5 break-all font-mono text-xs text-gray-700 dark:text-gray-200">{summary.image ?? "—"}</dd>
                </div>
                <div className="flex gap-6">
                  <div>
                    <dt className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("apps.detail.spec.port")}</dt>
                    <dd className="mt-0.5 font-mono text-gray-700 dark:text-gray-200">{summary.port || "—"}</dd>
                  </div>
                  <div>
                    <dt className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("apps.detail.spec.size")}</dt>
                    <dd className="mt-0.5 text-gray-700 dark:text-gray-200">
                      {summary.resources ? `${summary.resources.cpu_limit} CPU · ${summary.resources.memory_limit}` : "—"}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("apps.detail.spec.replicas")}</dt>
                    <dd className="mt-0.5 text-gray-700 dark:text-gray-200">{summary.replicas ?? 1}</dd>
                  </div>
                </div>
                {summary.url && (
                  <div>
                    <dt className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">URL</dt>
                    <dd className="mt-0.5 break-all">
                      <a
                        href={summary.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="font-mono text-xs text-blue-600 dark:text-blue-400 hover:underline"
                      >
                        {summary.url}
                      </a>
                    </dd>
                  </div>
                )}
                <div>
                  <dt className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("apps.card.syncedLabel")}</dt>
                  <dd className="mt-0.5 text-gray-700 dark:text-gray-200">{timeAgo(app.last_synced_at)}</dd>
                </div>
              </dl>
            </div>
          </div>
          <LogsViewer projectId={projectId} app={app.name} />
        </div>
      )}
    </div>
  );
}
