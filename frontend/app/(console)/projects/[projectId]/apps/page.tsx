"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { appsApi } from "@/lib/api";
import type { ResourceSnapshot, AppSummary } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { MetricSparkline } from "@/components/metrics/fixed-metrics-dashboard";
import { EmptyState } from "@/components/ui/empty-state";
import { useT } from "@/lib/i18n/console/context";

interface CreateAppForm {
  name: string;
  image: string;
  port: number;
  replicas: number;
  profile: string;
}

export default function AppsPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const router = useRouter();
  const { t } = useT();

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";
  const [apps, setApps] = useState<ResourceSnapshot[]>([]);
  const [isLoadingApps, setIsLoadingApps] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [form, setForm] = useState<CreateAppForm>({
    name: "",
    image: "",
    port: 8080,
    replicas: 2,
    profile: "small",
  });
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  useEffect(() => {
    if (!selectedEnvId) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch-on-mount pattern used by console pages; clears the loading flag once env resolution settles.
      if (!isLoadingEnvs) setIsLoadingApps(false);
      return;
    }
    setIsLoadingApps(true);
    setError(null);
    appsApi
      .list(projectId, selectedEnvId)
      .then((data) => setApps(data.apps ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : t("apps.error.load")))
      .finally(() => setIsLoadingApps(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, selectedEnvId, isLoadingEnvs]);

  function handleFormChange(field: keyof CreateAppForm, value: string | number) {
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const result = await appsApi.create(projectId, selectedEnvId, {
        name: form.name,
        image: form.image,
        port: form.port,
        replicas: form.replicas,
        profile: form.profile,
      });
      setIsModalOpen(false);
      setForm({ name: "", image: "", port: 8080, replicas: 2, profile: "small" });
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("apps.error.create"));
    } finally {
      setIsSubmitting(false);
    }
  }

  const isVMEnvironment = selectedEnv?.runtime === "vm";
  const canCreate = canMutate(role);

  if (isLoadingEnvs) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-8 flex items-start justify-between">
        <div>
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
        {canCreate && (
        <div className="flex items-center gap-2">
          <Link
            href={`/projects/${projectId}/git/import${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`}
            aria-disabled={!selectedEnvId || isVMEnvironment}
            className={`inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors ${
              !selectedEnvId || isVMEnvironment
                ? "pointer-events-none cursor-not-allowed opacity-50"
                : "hover:bg-blue-700"
            }`}
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9.568 3H5.25A2.25 2.25 0 003 5.25v4.318c0 .597.237 1.17.659 1.591l9.581 9.581c.699.699 1.78.872 2.607.33a18.095 18.095 0 005.223-5.223c.542-.827.369-1.908-.33-2.607L11.16 3.66A2.25 2.25 0 009.568 3z" />
            </svg>
            {t("apps.deployFromGit")}
          </Link>
          <button
            onClick={() => setIsModalOpen(true)}
            disabled={!selectedEnvId || isVMEnvironment}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
          >
            {t("apps.deployImage")}
          </button>
        </div>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {isVMEnvironment && (
        <div className="mb-6 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {t("apps.vmWarning")}
          <Link href={`/projects/${projectId}/app-servers`} className="ml-1 font-medium underline">
            {t("apps.vmWarning.manageAppServers")}
          </Link>
        </div>
      )}

      {isLoadingApps ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : apps.length === 0 ? (
        isVMEnvironment ? (
          <EmptyState
            title={t("apps.empty.title")}
            description={t("apps.empty.vm.description")}
          />
        ) : (
          <EmptyState
            title={t("apps.empty.title")}
            description={t("apps.empty.k8s.description")}
            action={{
              label: t("apps.empty.k8s.action"),
              href: `/projects/${projectId}/git/import${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`,
            }}
          />
        )
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((app) => {
            const summary = app.summary_json as unknown as AppSummary;
            return (
              <Link
                key={app.id}
                href={`/projects/${projectId}/apps/${app.name}?envId=${selectedEnvId}`}
                className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm hover:border-blue-200 hover:shadow-md transition-all"
              >
                <div className="mb-3 flex items-start justify-between">
                  <div className="min-w-0 flex-1">
                    <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{app.name}</p>
                    <p className="mt-0.5 font-mono text-xs text-gray-400 dark:text-gray-500 truncate">{summary.image ?? "—"}</p>
                  </div>
                  <PhaseBadge phase={app.phase} />
                </div>
                <div className="flex items-center gap-3 text-xs text-gray-400 dark:text-gray-500">
                  <span>{summary.profile ?? "small"}</span>
                  <span>·</span>
                  <span>{t("apps.card.replicas", { count: String(summary.replicas ?? 2) })}</span>
                </div>
                <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
                  {t("apps.card.synced", { ago: timeAgo(app.last_synced_at) })}
                </p>
                <MetricSparkline projectId={projectId} envId={selectedEnvId} appName={app.name} />
              </Link>
            );
          })}
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
              pattern="[a-z0-9-]+"
              title={t("apps.modal.create.name.title")}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.create.image.label")}</label>
            <input
              type="text"
              required
              value={form.image}
              onChange={(e) => handleFormChange("image", e.target.value)}
              placeholder="ghcr.io/org/service:v1.0.0"
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
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
              disabled={isSubmitting}
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
    </div>
  );
}
