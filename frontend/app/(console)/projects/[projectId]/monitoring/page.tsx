"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { monitoringApi } from "@/lib/api";
import type { MonitoringApp } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";

export default function MonitoringPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";

  const [apps, setApps] = useState<MonitoringApp[]>([]);
  const [isLoadingApps, setIsLoadingApps] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [name, setName] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Shown once after create — the plaintext API key.
  const [newApiKey, setNewApiKey] = useState<string | null>(null);
  const [isKeyModalOpen, setIsKeyModalOpen] = useState(false);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!selectedEnvId) {
      if (!isLoadingEnvs) setIsLoadingApps(false);
      return;
    }
    setIsLoadingApps(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    monitoringApi
      .list(projectId)
      .then((data) => setApps(data.monitoring_apps ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load monitoring apps"))
      .finally(() => setIsLoadingApps(false));
  }, [projectId, selectedEnvId, isLoadingEnvs]);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const result = await monitoringApi.create(projectId, selectedEnvId, name);
      setApps((prev) => [...prev, result.monitoring_app]);
      setIsModalOpen(false);
      setName("");
      setNewApiKey(result.api_key);
      setIsKeyModalOpen(true);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Failed to create monitoring app");
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
              { label: "Monitoring" },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900">Monitoring</h1>
          <p className="mt-0.5 text-sm text-gray-500">Grafana-backed observability apps</p>
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
            Create Monitoring
          </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {isLoadingApps ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : apps.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M14.857 17.082a23.848 23.848 0 005.454-1.31A8.967 8.967 0 0118 9.75v-.7V9A6 6 0 006 9v.75a8.967 8.967 0 01-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 01-5.714 0m5.714 0a3 3 0 11-5.714 0" />
          </svg>
          <p className="text-sm font-medium text-gray-500">
            No monitoring apps in {selectedEnv?.name ?? "this environment"}
          </p>
          {canCreate && (
            <button
              onClick={() => setIsModalOpen(true)}
              className="mt-4 text-sm text-blue-600 hover:text-blue-700"
            >
              Create your first monitoring app →
            </button>
          )}
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((app) => (
            <Link
              key={app.id}
              href={`/projects/${projectId}/monitoring/${app.id}${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`}
              className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm transition-all hover:border-blue-200 hover:shadow-md"
            >
              <div className="mb-3 flex items-start justify-between">
                <div>
                  <p className="font-mono text-sm font-semibold text-gray-900">{app.name}</p>
                  <p className="mt-0.5 text-xs text-gray-400">monitoring app</p>
                </div>
              </div>
              <p className="text-xs text-gray-400">
                Created {new Date(app.created_at).toLocaleDateString()}
              </p>
            </Link>
          ))}
        </div>
      )}

      {/* Create Monitoring Modal */}
      <Modal
        isOpen={isModalOpen}
        onClose={() => {
          setIsModalOpen(false);
          setSubmitError(null);
          setName("");
        }}
        title="Create Monitoring App"
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">Name</label>
            <input
              type="text"
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-service-monitor"
              pattern="[a-z0-9-]+"
              title="Lowercase letters, numbers, and hyphens only"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

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
                setName("");
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
                "Create"
              )}
            </button>
          </div>
        </form>
      </Modal>

      {/* API Key reveal modal — shown once after create */}
      <Modal
        isOpen={isKeyModalOpen}
        onClose={() => {
          setIsKeyModalOpen(false);
          setNewApiKey(null);
        }}
        title="Monitoring App Created"
      >
        <div className="space-y-4">
          <p className="text-sm text-gray-600">
            Your monitoring app API key is shown below. Copy it now — it will not be shown again.
          </p>
          <div>
            <label className="block text-xs font-medium uppercase tracking-wide text-gray-500 mb-1">
              API Key
            </label>
            <div className="flex items-center gap-2">
              <pre className="flex-1 overflow-x-auto rounded-lg border border-amber-200 bg-amber-50 p-3 font-mono text-sm text-amber-900 break-all whitespace-pre-wrap">
                {newApiKey}
              </pre>
              <button
                type="button"
                onClick={() => newApiKey && navigator.clipboard.writeText(newApiKey)}
                className="shrink-0 rounded-lg border border-gray-200 px-3 py-2 text-sm font-medium text-gray-600 hover:bg-gray-50 transition-colors"
              >
                Copy
              </button>
            </div>
            <p className="mt-2 text-xs text-amber-700">Save this key — it cannot be recovered after closing this dialog.</p>
          </div>
          <div className="flex justify-end">
            <button
              type="button"
              onClick={() => {
                setIsKeyModalOpen(false);
                setNewApiKey(null);
              }}
              className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 transition-colors"
            >
              Done
            </button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
