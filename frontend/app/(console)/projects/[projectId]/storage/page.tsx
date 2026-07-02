"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useRouter } from "next/navigation";
import { s3bucketsApi } from "@/lib/api";
import type { ResourceSnapshot } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { EmptyState } from "@/components/ui/empty-state";
import { useT } from "@/lib/i18n/console/context";

interface CreateBucketForm {
  name: string;
  bucket_name: string;
  region: string;
  description: string;
  public: boolean;
  ftp_sftp_enable: boolean;
  app_ref: string;
}

const DEFAULT_FORM: CreateBucketForm = {
  name: "",
  bucket_name: "",
  region: "ru1",
  description: "",
  public: false,
  ftp_sftp_enable: true,
  app_ref: "",
};

export default function StoragePage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const router = useRouter();
  const { t } = useT();

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";
  const [buckets, setBuckets] = useState<ResourceSnapshot[]>([]);
  const [isLoadingBuckets, setIsLoadingBuckets] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [form, setForm] = useState<CreateBucketForm>(DEFAULT_FORM);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!selectedEnvId) {
      if (!isLoadingEnvs) setIsLoadingBuckets(false);
      return;
    }
    setIsLoadingBuckets(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    s3bucketsApi
      .list(projectId, selectedEnvId)
      .then((data) => setBuckets(data.buckets ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : t("storage.error.load")))
      .finally(() => setIsLoadingBuckets(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, selectedEnvId, isLoadingEnvs]);

  function handleFormChange(field: keyof CreateBucketForm, value: string | boolean) {
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const result = await s3bucketsApi.create(projectId, selectedEnvId, {
        name: form.name,
        bucket_name: form.bucket_name,
        region: form.region,
        description: form.description,
        public: form.public,
        ftp_sftp_enable: form.ftp_sftp_enable,
        app_ref: form.app_ref.trim() || undefined,
      });
      setIsModalOpen(false);
      setForm(DEFAULT_FORM);
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("storage.error.create"));
    } finally {
      setIsSubmitting(false);
    }
  }

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
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.storage") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("storage.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("storage.subtitle")}</p>
        </div>
        {canCreate && (
          <button
            onClick={() => setIsModalOpen(true)}
            disabled={!selectedEnvId}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("storage.createBucket")}
          </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {isLoadingBuckets ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : buckets.length === 0 ? (
        <div className="space-y-4">
          <EmptyState
            title={t("storage.empty.title")}
            description={t("storage.empty.description")}
          />
          {canCreate && (
            <div className="flex justify-center">
              <button
                onClick={() => setIsModalOpen(true)}
                disabled={!selectedEnvId}
                className="text-sm font-medium text-blue-600 hover:text-blue-700 disabled:opacity-50"
              >
                {t("storage.empty.cta")}
              </button>
            </div>
          )}
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {buckets.map((b) => {
            const summary = b.summary_json as Record<string, unknown>;
            return (
              <div
                key={b.id}
                className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm"
              >
                <div className="mb-3 flex items-start justify-between">
                  <div>
                    <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{b.name}</p>
                    <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
                      {String(summary.bucket_name ?? "")} · {String(summary.region ?? "ru1")}
                    </p>
                  </div>
                  <PhaseBadge phase={b.phase} />
                </div>
                <div className="flex flex-wrap gap-1.5">
                  {Boolean(summary.public) && (
                    <span className="inline-flex items-center rounded-full bg-amber-50 dark:bg-amber-950/40 px-2 py-0.5 text-xs font-medium text-amber-700 dark:text-amber-300 ring-1 ring-amber-600/20">
                      {t("storage.badge.public")}
                    </span>
                  )}
                  <span className="inline-flex items-center rounded-full bg-slate-50 dark:bg-slate-950/40 px-2 py-0.5 text-xs font-medium text-slate-600 dark:text-slate-400 ring-1 ring-slate-500/20">
                    {summary.app_ref
                      ? t("storage.badge.appRef", { name: String(summary.app_ref) })
                      : t("storage.badge.envLevel")}
                  </span>
                </div>
                <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
                  {t("common.status.synced", { ago: timeAgo(b.last_synced_at) })}
                </p>
              </div>
            );
          })}
        </div>
      )}

      <Modal
        isOpen={isModalOpen}
        onClose={() => { setIsModalOpen(false); setSubmitError(null); }}
        title={t("storage.modal.title")}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("storage.modal.resourceName")} <span className="text-gray-400 dark:text-gray-500 font-normal">{t("storage.modal.resourceNameSub")}</span>
            </label>
            <input
              type="text"
              required
              value={form.name}
              onChange={(e) => handleFormChange("name", e.target.value)}
              placeholder="my-bucket"
              pattern="[a-z0-9-]+"
              title={t("storage.modal.resourceNameTitle")}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("storage.modal.bucketName")}</label>
            <input
              type="text"
              required
              value={form.bucket_name}
              onChange={(e) => handleFormChange("bucket_name", e.target.value)}
              placeholder="my-app-assets"
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("storage.modal.region")}</label>
              <select
                value={form.region}
                onChange={(e) => handleFormChange("region", e.target.value)}
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value="ru1">ru1</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("storage.modal.description")}</label>
              <input
                type="text"
                value={form.description}
                onChange={(e) => handleFormChange("description", e.target.value)}
                placeholder={t("storage.modal.descriptionPlaceholder")}
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("storage.modal.appRef")} <span className="text-gray-400 dark:text-gray-500 font-normal">{t("storage.modal.appRefSub")}</span>
            </label>
            <input
              type="text"
              value={form.app_ref}
              onChange={(e) => handleFormChange("app_ref", e.target.value)}
              placeholder="my-app"
              pattern="[a-z0-9-]*"
              title={t("storage.modal.appRefTitle")}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {t("storage.modal.appRefHelp")}
            </p>
          </div>

          <div className="space-y-2">
            <Toggle
              label={t("storage.toggle.public.label")}
              description={t("storage.toggle.public.description")}
              checked={form.public}
              onChange={(v) => handleFormChange("public", v)}
            />
            <Toggle
              label={t("storage.toggle.ftp.label")}
              description={t("storage.toggle.ftp.description")}
              checked={form.ftp_sftp_enable}
              onChange={(v) => handleFormChange("ftp_sftp_enable", v)}
            />
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
              type="submit"
              disabled={isSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? <><Spinner size="sm" />{t("common.creating")}</> : t("storage.createBucket")}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

function Toggle({ label, description, checked, onChange }: {
  label: string; description: string; checked: boolean; onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between rounded-lg border border-gray-200 dark:border-gray-800 px-4 py-3">
      <div>
        <p className="text-sm font-medium text-gray-700 dark:text-gray-200">{label}</p>
        <p className="text-xs text-gray-400 dark:text-gray-500">{description}</p>
      </div>
      <button
        type="button"
        onClick={() => onChange(!checked)}
        className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 ${checked ? "bg-blue-600" : "bg-gray-200 dark:bg-gray-700"}`}
        role="switch"
        aria-checked={checked}
      >
        <span className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${checked ? "translate-x-6" : "translate-x-1"}`} />
      </button>
    </div>
  );
}
