"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { databasesApi } from "@/lib/api";
import { docsHref } from "@/lib/site";
import type { ResourceSnapshot } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { ResourceZeroState } from "@/components/ui/resource-zero-state";
import { Database } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";

interface CreateDbForm {
  name: string;
  database: string;
  backup_enabled: boolean;
  backup_schedule: string;
  backup_retention: string;
  external_enabled: boolean;
}

/**
 * Generates a unique-enough resource name + derived PostgreSQL identifier so
 * the create form never forces the user to invent one. The random suffix is
 * shared between both so the pair reads as one database.
 */
function generateDbNames(): { name: string; database: string } {
  const suffix = (
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : Math.random().toString(36).slice(2)
  )
    .replace(/-/g, "")
    .slice(0, 8);
  return { name: `db-${suffix}`, database: `db_${suffix}` };
}

function fmtBytes(v: number): string {
  if (v >= 1 << 30) return `${(v / (1 << 30)).toFixed(1)} GB`;
  if (v >= 1 << 20) return `${(v / (1 << 20)).toFixed(0)} MB`;
  if (v >= 1 << 10) return `${(v / (1 << 10)).toFixed(0)} KB`;
  return `${v} B`;
}

export default function DatabasesPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const [refreshTick, setRefreshTick] = useState(0);
  const { t } = useT();

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";
  const [databases, setDatabases] = useState<ResourceSnapshot[]>([]);
  const [isLoadingDbs, setIsLoadingDbs] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [form, setForm] = useState<CreateDbForm>(() => ({
    ...generateDbNames(),
    backup_enabled: false,
    backup_schedule: "daily",
    backup_retention: "7d",
    external_enabled: false,
  }));
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  function openCreateModal() {
    setForm((prev) => ({ ...prev, ...generateDbNames() }));
    setSubmitError(null);
    setIsModalOpen(true);
  }

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!selectedEnvId) {
      if (!isLoadingEnvs) setIsLoadingDbs(false);
      return;
    }
    setIsLoadingDbs(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    databasesApi
      .list(projectId, selectedEnvId)
      .then((data) => setDatabases(data.databases ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : t("databases.error.load")))
      .finally(() => setIsLoadingDbs(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, selectedEnvId, isLoadingEnvs, refreshTick]);

  function handleFormChange(field: keyof CreateDbForm, value: string | boolean) {
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      await databasesApi.create(projectId, selectedEnvId, {
        name: form.name,
        database: form.database,
        app_ref: "",
        backup_enabled: form.backup_enabled,
        backup_schedule: form.backup_schedule,
        backup_retention: form.backup_retention,
        external_enabled: form.external_enabled,
      });
      setIsModalOpen(false);
      setForm({ ...generateDbNames(), backup_enabled: false, backup_schedule: "daily", backup_retention: "7d", external_enabled: false });
      setRefreshTick((v) => v + 1);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("databases.error.create"));
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
              { label: t("nav.databases") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("databases.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("databases.subtitle")}</p>
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
          {t("databases.createButton")}
        </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {isLoadingDbs ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : databases.length === 0 ? (
        <div>
          <ResourceZeroState
            tone="violet"
            icon={<Database className="h-8 w-8" />}
            title={t("databases.empty.title")}
            description={t("databases.empty.description")}
            cta={
              canCreate
                ? { label: t("databases.empty.create"), onClick: openCreateModal, disabled: !selectedEnvId }
                : undefined
            }
            steps={[t("databases.empty.step1"), t("databases.empty.step2"), t("databases.empty.step3")]}
          />
          <div className="mt-4 text-center">
            <a href={docsHref("databases-postgres")} target="_blank" rel="noopener noreferrer" className="text-sm font-medium text-blue-600 hover:text-blue-700">
              {t("common.learnMore")} →
            </a>
          </div>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {databases.map((db) => {
            const s = (db.summary_json ?? {}) as {
              size_bytes?: number;
            };
            return (
              <Link
                key={db.id}
                href={`/projects/${projectId}/databases/${db.name}${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`}
                className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm transition-all hover:border-blue-200 hover:shadow-md"
              >
                <div className="mb-3 flex items-start justify-between">
                  <div>
                    <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{db.name}</p>
                    <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{db.kind}</p>
                  </div>
                  <PhaseBadge phase={db.phase} />
                </div>
                <div className="flex items-center gap-3 text-xs text-gray-500 dark:text-gray-400">
                  <span>{typeof s.size_bytes === "number" ? fmtBytes(s.size_bytes) : t("databases.card.size")}</span>
                </div>
                <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
                  {t("databases.card.synced", { ago: timeAgo(db.last_synced_at) })}
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
        title={t("databases.modal.title")}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("databases.modal.name.label")}{" "}
              <span className="text-gray-400 dark:text-gray-500 font-normal">({t("databases.modal.name.hint")})</span>
            </label>
            <input
              type="text"
              required
              value={form.name}
              onChange={(e) => handleFormChange("name", e.target.value)}
              pattern="[a-z0-9-]+"
              title={t("databases.modal.name.validation")}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("databases.modal.pgName.label")}{" "}
              <span className="text-gray-400 dark:text-gray-500 font-normal">({t("databases.modal.pgName.hint")})</span>
            </label>
            <input
              type="text"
              required
              value={form.database}
              onChange={(e) => handleFormChange("database", e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div className="flex items-center justify-between rounded-lg border border-gray-200 dark:border-gray-800 px-4 py-3">
            <div>
              <p className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("databases.modal.backups.title")}</p>
              <p className="text-xs text-gray-400 dark:text-gray-500">{t("databases.modal.backups.subtitle")}</p>
            </div>
            <button
              type="button"
              onClick={() => handleFormChange("backup_enabled", !form.backup_enabled)}
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 ${
                form.backup_enabled ? "bg-blue-600" : "bg-gray-200 dark:bg-gray-700"
              }`}
              role="switch"
              aria-checked={form.backup_enabled}
            >
              <span
                className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
                  form.backup_enabled ? "translate-x-6" : "translate-x-1"
                }`}
              />
            </button>
          </div>

          {form.backup_enabled && (
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("databases.modal.schedule.label")}</label>
                <select
                  value={form.backup_schedule}
                  onChange={(e) => handleFormChange("backup_schedule", e.target.value)}
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                >
                  <option value="hourly">{t("databases.modal.schedule.hourly")}</option>
                  <option value="daily">{t("databases.modal.schedule.daily")}</option>
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("databases.modal.retention.label")}</label>
                <select
                  value={form.backup_retention}
                  onChange={(e) => handleFormChange("backup_retention", e.target.value)}
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                >
                  <option value="7d">{t("databases.modal.retention.7d")}</option>
                  <option value="14d">{t("databases.modal.retention.14d")}</option>
                  <option value="30d">{t("databases.modal.retention.30d")}</option>
                </select>
              </div>
            </div>
          )}

          <div className="flex items-center justify-between rounded-lg border border-gray-200 dark:border-gray-800 px-4 py-3">
            <div>
              <p className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("databases.modal.external.title")}</p>
              <p className="text-xs text-gray-400 dark:text-gray-500">{t("databases.modal.external.subtitle")}</p>
            </div>
            <button
              type="button"
              onClick={() => handleFormChange("external_enabled", !form.external_enabled)}
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 ${
                form.external_enabled ? "bg-blue-600" : "bg-gray-200 dark:bg-gray-700"
              }`}
              role="switch"
              aria-checked={form.external_enabled}
            >
              <span
                className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
                  form.external_enabled ? "translate-x-6" : "translate-x-1"
                }`}
              />
            </button>
          </div>
          {form.external_enabled && (
            <p className="rounded-lg bg-amber-50 dark:bg-amber-950/30 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
              {t("databases.modal.external.warning")}
            </p>
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
                t("databases.modal.submit")
              )}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
