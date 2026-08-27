"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { cachesApi, appsApi } from "@/lib/api";
import { docsHref } from "@/lib/site";
import type { ResourceSnapshot, CacheProfile } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { DataTabs } from "@/components/data/data-tabs";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { ResourceZeroState } from "@/components/ui/resource-zero-state";
import { Database } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";
import { isSettling } from "@/lib/phase";
import { trackUxEvent } from "@/lib/ux-telemetry";

interface CreateCacheForm {
  name: string;
  app_ref: string;
  key_prefix: string;
  profile: CacheProfile;
}

/** Same generation shape as generateDbNames on the Databases page. */
function generateCacheName(): string {
  const suffix = (
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : Math.random().toString(36).slice(2)
  )
    .replace(/-/g, "")
    .slice(0, 8);
  return `cache-${suffix}`;
}

const DEFAULT_CACHE_PROFILE: CacheProfile = "redis-full-access";

const CACHE_PROFILES: CacheProfile[] = [
  "redis-full-access",
  "redis-kv-readonly",
  "redis-kv-readwrite",
  "redis-stream-producer",
  "redis-stream-consumer",
  "redis-stream-admin",
  "redis-list-producer",
  "redis-list-consumer",
];

function profileLabelKey(profile: CacheProfile): string {
  return `caches.modal.profile.${profile.replace("redis-", "")}`;
}

export default function CachesPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const [refreshTick, setRefreshTick] = useState(0);
  const { t } = useT();

  const { project, selectedEnv, role, environments, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";
  const [caches, setCaches] = useState<ResourceSnapshot[]>([]);
  const [isLoadingCaches, setIsLoadingCaches] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [form, setForm] = useState<CreateCacheForm>(() => ({
    name: generateCacheName(),
    app_ref: "",
    key_prefix: "",
    profile: DEFAULT_CACHE_PROFILE,
  }));
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [apps, setApps] = useState<ResourceSnapshot[]>([]);

  function openCreateModal() {
    trackUxEvent("view", "create_cache_modal:opened");
    if (selectedEnvId) {
      appsApi
        .list(projectId, selectedEnvId)
        .then((data) => setApps(data.apps ?? []))
        .catch(() => setApps([]));
    }
    setForm({ name: generateCacheName(), app_ref: "", key_prefix: "", profile: DEFAULT_CACHE_PROFILE });
    setShowAdvanced(false);
    setSubmitError(null);
    setIsModalOpen(true);
  }

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!selectedEnvId) {
      if (!isLoadingEnvs && environments.length === 0) setIsLoadingCaches(false);
      return;
    }
    setIsLoadingCaches(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    cachesApi
      .list(projectId, selectedEnvId)
      .then((data) => setCaches(data.caches ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : t("caches.error.load")))
      .finally(() => setIsLoadingCaches(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, selectedEnvId, isLoadingEnvs, environments.length, refreshTick]);

  useEffect(() => {
    if (!selectedEnvId) return;
    if (!isSettling(caches)) return;
    const id = setTimeout(() => {
      cachesApi
        .list(projectId, selectedEnvId)
        .then((data) => setCaches(data.caches ?? []))
        .catch(() => undefined);
    }, 4000);
    return () => clearTimeout(id);
  }, [caches, projectId, selectedEnvId]);

  function handleFormChange(field: keyof CreateCacheForm, value: string) {
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      await cachesApi.create(projectId, selectedEnvId, {
        name: form.name,
        app_ref: form.app_ref,
        key_prefix: form.key_prefix,
        profile: form.profile,
      });
      setIsModalOpen(false);
      setRefreshTick((v) => v + 1);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("caches.error.create"));
    } finally {
      setIsSubmitting(false);
    }
  }

  const canCreate = canMutate(role);

  if (isLoadingEnvs || (!selectedEnvId && environments.length > 0)) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.redis") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("caches.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("caches.subtitle")}</p>
          <DataTabs projectId={projectId} active="redis" />
        </div>
        {canCreate && (
        <button
          onClick={openCreateModal}
          disabled={!selectedEnvId}
          className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          {t("caches.createButton")}
        </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {isLoadingCaches ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : caches.length === 0 ? (
        <div>
          <ResourceZeroState
            tone="violet"
            icon={<Database className="h-8 w-8" />}
            title={t("caches.empty.title")}
            description={t("caches.empty.description")}
            cta={
              canCreate
                ? { label: t("caches.empty.create"), onClick: openCreateModal, disabled: !selectedEnvId }
                : undefined
            }
            steps={[t("caches.empty.step1"), t("caches.empty.step2"), t("caches.empty.step3")]}
          />
          <div className="mt-4 text-center">
            <a href={docsHref("databases-postgres")} target="_blank" rel="noopener noreferrer" className="text-sm font-medium text-blue-600 hover:text-blue-700">
              {t("common.learnMore")} →
            </a>
          </div>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {caches.map((cache) => {
            const spec = (cache.summary_json?.spec ?? {}) as { profile?: string; appRef?: string };
            return (
              <Link
                key={cache.id}
                href={`/projects/${projectId}/redis/${cache.name}${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`}
                className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm transition-all hover:border-blue-200 hover:shadow-md"
              >
                <div className="mb-3 flex items-start justify-between">
                  <div>
                    <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{cache.name}</p>
                    <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{spec.profile ?? cache.kind}</p>
                  </div>
                  <PhaseBadge phase={cache.phase} />
                </div>
                <div className="flex items-center gap-3 text-xs text-gray-500 dark:text-gray-400">
                  <span className="font-mono">{spec.appRef ?? "—"}</span>
                </div>
                <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
                  {t("caches.card.synced", { ago: timeAgo(cache.last_synced_at) })}
                </p>
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
        title={t("caches.modal.title")}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("caches.modal.name.label")}{" "}
              <span className="text-gray-400 dark:text-gray-500 font-normal">({t("caches.modal.name.hint")})</span>
            </label>
            <input
              type="text"
              required
              value={form.name}
              onChange={(e) => handleFormChange("name", e.target.value)}
              pattern="[a-z0-9-]+"
              title={t("caches.modal.name.validation")}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <button
            type="button"
            onClick={() => setShowAdvanced((v) => !v)}
            className="text-sm font-medium text-blue-600 hover:text-blue-700 dark:text-blue-400 dark:hover:text-blue-300"
          >
            {showAdvanced ? t("caches.modal.advanced.hide") : t("caches.modal.advanced.show")}
          </button>

          {showAdvanced && (
            <div className="space-y-4 rounded-lg border border-gray-200 dark:border-gray-800 p-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
                  {t("caches.modal.appRef.label")}
                </label>
                <select
                  value={form.app_ref}
                  onChange={(e) => handleFormChange("app_ref", e.target.value)}
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                >
                  <option value="">{t("caches.modal.appRef.auto")}</option>
                  {apps.map((app) => (
                    <option key={app.name} value={app.name}>
                      {app.name}
                    </option>
                  ))}
                </select>
                <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("caches.modal.appRef.optionalHint")}</p>
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
                  {t("caches.modal.keyPrefix.label")}
                </label>
                <input
                  type="text"
                  value={form.key_prefix}
                  onChange={(e) => handleFormChange("key_prefix", e.target.value)}
                  placeholder={form.name}
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 font-mono text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
                <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("caches.modal.keyPrefix.hint")}</p>
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
                  {t("caches.modal.profile.label")}
                </label>
                <select
                  value={form.profile}
                  onChange={(e) => handleFormChange("profile", e.target.value)}
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                >
                  {CACHE_PROFILES.map((p) => (
                    <option key={p} value={p}>{t(profileLabelKey(p))}</option>
                  ))}
                </select>
                <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("caches.modal.profile.hint")}</p>
              </div>
            </div>
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
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
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
                  {t("common.creating")}
                </>
              ) : (
                t("caches.modal.submit")
              )}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
