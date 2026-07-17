"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { appsApi } from "@/lib/api";
import { docsHref } from "@/lib/site";
import type { ResourceSnapshot, AppSummary, InfraSummary, Environment } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { MetricSparkline } from "@/components/metrics/fixed-metrics-dashboard";
import { EmptyState } from "@/components/ui/empty-state";
import { DeployChooser } from "@/components/deploy/deploy-chooser";
import { TemplateDeployCards } from "@/components/console/template-deploy-cards";
import { useT } from "@/lib/i18n/console/context";
import { Globe, Database } from "lucide-react";
import { classifyVMResource, extractIngressSpec, extractDatabaseSpec } from "@/lib/vm-resources";

interface CreateAppForm {
  name: string;
  image: string;
  port: number;
  replicas: number;
  profile: string;
  workloadType: string;
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
    profile: "small",
    workloadType: "Deployment",
  });
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  useEffect(() => {
    if (environments.length === 0) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch-on-mount pattern; clears loading once env resolution settles.
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

  function handleFormChange(field: keyof CreateAppForm, value: string | number) {
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
        profile: form.profile,
        workload_type: form.workloadType,
      });
      setIsModalOpen(false);
      setForm({ name: "", image: "", port: 8080, replicas: 1, profile: "small", workloadType: "Deployment" });
      void result;
      router.push(`/projects/${projectId}/apps/${form.name}`);
    } catch (err) {
      const raw = err instanceof Error ? err.message : "";
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

  const canCreate = canMutate(role);

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

      {isLoadingApps ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : (
        <div className="space-y-10">
          {environments.map((env) => (
            <EnvBlock
              key={env.id}
              env={env}
              projectId={projectId}
              apps={appsByEnv[env.id] ?? []}
              infra={infraByEnv[env.id] ?? []}
              canCreate={canCreate}
              onCreate={() => openChooser(env.id)}
              t={t}
            />
          ))}
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

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.create.port.label")}</label>
              <input
                type="number"
                required
                min={1}
                max={65535}
                value={form.port}
                onChange={(e) => handleFormChange("port", parseInt(e.target.value, 10))}
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
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
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.create.profile.label")}</label>
            <select
              value={form.profile}
              onChange={(e) => handleFormChange("profile", e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="small">small</option>
              <option value="medium">medium</option>
              <option value="large">large</option>
            </select>
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

interface EnvBlockProps {
  env: Environment;
  projectId: string;
  apps: ResourceSnapshot[];
  infra: ResourceSnapshot[];
  canCreate: boolean;
  onCreate: () => void;
  t: (key: string, vars?: Record<string, string>) => string;
}

function EnvBlock({ env, projectId, apps, infra, canCreate, onCreate, t }: EnvBlockProps) {
  return (
    <section>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 dark:border-gray-800 pb-3">
        <div className="flex items-center gap-2">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{env.name}</h2>
          <span className="rounded-full bg-gray-100 dark:bg-gray-800 px-2 py-0.5 text-xs font-medium text-gray-500 dark:text-gray-400">
            {env.runtime === "vm" ? t("env.runtime.vm") : t("env.runtime.cloud")}
          </span>
          <span className="text-xs text-gray-400 dark:text-gray-500">{t("apps.env.count", { count: String(apps.length) })}</span>
        </div>
        {canCreate && (
          <button
            onClick={onCreate}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 active:bg-blue-800 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("apps.deploy.button")}
          </button>
        )}
      </div>

      {apps.length === 0 ? (
        <>
          <EmptyState
            title={t("apps.empty.title")}
            description={env.runtime === "vm" ? t("apps.empty.vm.description") : t("apps.empty.k8s.description")}
            action={
              env.runtime === "vm"
                ? { label: t("apps.empty.vm.action"), href: `/projects/${projectId}/app-servers` }
                : { label: t("apps.empty.k8s.action"), href: `/projects/${projectId}/git/import?envId=${env.id}` }
            }
            secondary={{ label: t("common.learnMore"), href: docsHref("applications-deploy-from-github") }}
          />
          {env.runtime !== "vm" && (
            <div className="mt-6">
              <p className="mb-3 text-center text-sm text-gray-500 dark:text-gray-400">{t("apps.empty.k8s.orTemplate")}</p>
              <TemplateDeployCards projectId={projectId} envId={env.id} />
            </div>
          )}
        </>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((app) => {
            const summary = app.summary_json as unknown as AppSummary;
            const resType = classifyVMResource(app);
            const ing = resType === "ingress" ? extractIngressSpec(app) : null;
            const db = resType === "database" ? extractDatabaseSpec(app) : null;
            const subtitle =
              resType === "ingress"
                ? ing?.host ?? summary.image ?? "—"
                : resType === "database"
                  ? `${db?.engine ?? ""}${db?.version ? " " + db.version : ""}`
                  : summary.image ?? "—";
            return (
              <Link
                key={app.id}
                href={`/projects/${projectId}/apps/${app.name}?envId=${env.id}`}
                className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm hover:border-blue-200 hover:shadow-md transition-all"
              >
                <div className="mb-3 flex items-start justify-between gap-2">
                  <div className="flex min-w-0 flex-1 items-start gap-2.5">
                    {resType === "ingress" && (
                      <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
                        <Globe className="h-4 w-4" />
                      </span>
                    )}
                    {resType === "database" && (
                      <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-violet-50 dark:bg-violet-950/40 text-violet-600 dark:text-violet-400">
                        <Database className="h-4 w-4" />
                      </span>
                    )}
                    <div className="min-w-0">
                      <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100 truncate">{app.name}</p>
                      <p className="mt-0.5 truncate font-mono text-xs text-gray-400 dark:text-gray-500">{subtitle}</p>
                    </div>
                  </div>
                  <PhaseBadge phase={app.phase} />
                </div>
                <div className="flex items-center gap-3 text-xs text-gray-400 dark:text-gray-500">
                  {resType === "ingress" ? (
                    <span>{t("resources.card.routes", { count: String(ing?.rules?.length ?? 0) })}</span>
                  ) : resType === "database" ? (
                    <span className="truncate font-mono">{db?.volume || db?.database || "—"}</span>
                  ) : (
                    <>
                      <span>{summary.profile ?? "small"}</span>
                      <span>·</span>
                      <span>{t("apps.card.replicas", { count: String(summary.replicas ?? 1) })}</span>
                    </>
                  )}
                </div>
                <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
                  {t("apps.card.synced", { ago: timeAgo(app.last_synced_at) })}
                </p>
                {summary.url && (
                  <a
                    href={summary.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(e) => e.stopPropagation()}
                    className="mt-2 inline-flex items-center gap-1.5 truncate text-xs font-medium text-blue-600 hover:text-blue-700 hover:underline dark:text-blue-400 dark:hover:text-blue-300"
                  >
                    <Globe className="h-3.5 w-3.5 shrink-0" />
                    {t("apps.card.openUrl", { hostname: appHostname(summary.url) })}
                  </a>
                )}
                <MetricSparkline projectId={projectId} envId={env.id} appName={app.name} />
              </Link>
            );
          })}
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
