"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { databasesApi } from "@/lib/api";
import type { ResourceSnapshot } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { PhaseBadge } from "@/components/ui/phase-badge";

interface CreateDbForm {
  name: string;
  database: string;
  app_ref: string;
  backup_enabled: boolean;
  backup_schedule: string;
  backup_retention: string;
}

export default function DatabasesPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const router = useRouter();

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";
  const [databases, setDatabases] = useState<ResourceSnapshot[]>([]);
  const [isLoadingDbs, setIsLoadingDbs] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [form, setForm] = useState<CreateDbForm>({
    name: "",
    database: "",
    app_ref: "",
    backup_enabled: true,
    backup_schedule: "daily",
    backup_retention: "7d",
  });
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Load databases when the selected environment (from shared context) changes.
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
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load databases"))
      .finally(() => setIsLoadingDbs(false));
  }, [projectId, selectedEnvId, isLoadingEnvs]);

  function handleFormChange(field: keyof CreateDbForm, value: string | boolean) {
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const result = await databasesApi.create(projectId, selectedEnvId, {
        name: form.name,
        database: form.database,
        app_ref: form.app_ref,
        backup_enabled: form.backup_enabled,
        backup_schedule: form.backup_schedule,
        backup_retention: form.backup_retention,
      });
      setIsModalOpen(false);
      setForm({ name: "", database: "", app_ref: "", backup_enabled: true, backup_schedule: "daily", backup_retention: "7d" });
      // Redirect immediately to the live-updating, highlighted operation.
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Failed to create database");
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
      {/* Header */}
      <div className="mb-8 flex items-start justify-between">
        <div>
          <Breadcrumb
            items={[
              { label: "Projects", href: "/projects" },
              { label: project?.display_name ?? "Overview", href: `/projects/${projectId}` },
              { label: "Databases" },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900">Databases</h1>
          <p className="mt-0.5 text-sm text-gray-500">PostgreSQL database instances</p>
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
          Create Database
        </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {/* Database list */}
      {isLoadingDbs ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : databases.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4" />
          </svg>
          <p className="text-sm font-medium text-gray-500">No databases in {selectedEnv?.name ?? "this environment"}</p>
          <button
            onClick={() => setIsModalOpen(true)}
            className="mt-4 text-sm text-blue-600 hover:text-blue-700"
          >
            Create your first database →
          </button>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {databases.map((db) => (
            <Link
              key={db.id}
              href={`/projects/${projectId}/databases/${db.name}${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`}
              className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm transition-all hover:border-blue-200 hover:shadow-md"
            >
              <div className="mb-3 flex items-start justify-between">
                <div>
                  <p className="font-mono text-sm font-semibold text-gray-900">{db.name}</p>
                  <p className="mt-0.5 text-xs text-gray-400">{db.kind}</p>
                </div>
                <PhaseBadge phase={db.phase} />
              </div>
              <p className="text-xs text-gray-400">
                Synced {timeAgo(db.last_synced_at)}
              </p>
            </Link>
          ))}
        </div>
      )}

      {/* Create Database Modal */}
      <Modal
        isOpen={isModalOpen}
        onClose={() => {
          setIsModalOpen(false);
          setSubmitError(null);
        }}
        title="Create Database"
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">
              Database Name <span className="text-gray-400 font-normal">(Kubernetes resource name)</span>
            </label>
            <input
              type="text"
              required
              value={form.name}
              onChange={(e) => handleFormChange("name", e.target.value)}
              placeholder="my-app-db"
              pattern="[a-z0-9-]+"
              title="Lowercase letters, numbers, and hyphens only"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">
              PostgreSQL DB Name
            </label>
            <input
              type="text"
              required
              value={form.database}
              onChange={(e) => handleFormChange("database", e.target.value)}
              placeholder="myappdb"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">
              App Reference
            </label>
            <input
              type="text"
              required
              value={form.app_ref}
              onChange={(e) => handleFormChange("app_ref", e.target.value)}
              placeholder="my-app"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          {/* Backup toggle */}
          <div className="flex items-center justify-between rounded-lg border border-gray-200 px-4 py-3">
            <div>
              <p className="text-sm font-medium text-gray-700">Enable Backups</p>
              <p className="text-xs text-gray-400">Automatic scheduled backups</p>
            </div>
            <button
              type="button"
              onClick={() => handleFormChange("backup_enabled", !form.backup_enabled)}
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 ${
                form.backup_enabled ? "bg-blue-600" : "bg-gray-200"
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
                <label className="block text-sm font-medium text-gray-700">Backup Schedule</label>
                <select
                  value={form.backup_schedule}
                  onChange={(e) => handleFormChange("backup_schedule", e.target.value)}
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                >
                  <option value="hourly">Hourly</option>
                  <option value="daily">Daily</option>
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700">Retention</label>
                <select
                  value={form.backup_retention}
                  onChange={(e) => handleFormChange("backup_retention", e.target.value)}
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                >
                  <option value="7d">7 days</option>
                  <option value="14d">14 days</option>
                  <option value="30d">30 days</option>
                </select>
              </div>
            </div>
          )}

          {submitError && (
            <div role="alert" className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
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
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? (
                <>
                  <Spinner size="sm" />
                  Creating...
                </>
              ) : (
                "Create Database"
              )}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
