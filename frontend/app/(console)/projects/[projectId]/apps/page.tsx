"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { projectsApi, appsApi } from "@/lib/api";
import type { Environment, ResourceSnapshot, AppSummary } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";

function timeAgo(dateStr: string): string {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffSecs = Math.floor(diffMs / 1000);
  if (diffSecs < 60) return `${diffSecs}s ago`;
  const diffMins = Math.floor(diffSecs / 60);
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays}d ago`;
}

function PhaseBadge({ phase }: { phase?: string }) {
  const p = phase ?? "";
  const isReady = p.toLowerCase() === "ready";
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
      isReady ? "bg-green-100 text-green-700" : "bg-yellow-100 text-yellow-700"
    }`}>
      {p || "Unknown"}
    </span>
  );
}

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

  const [environments, setEnvironments] = useState<Environment[]>([]);
  const [selectedEnvId, setSelectedEnvId] = useState<string>("");
  const [apps, setApps] = useState<ResourceSnapshot[]>([]);
  const [isLoadingEnvs, setIsLoadingEnvs] = useState(true);
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

  // Load environments
  useEffect(() => {
    projectsApi
      .get(projectId)
      .then((data) => {
        const envs = data.environments ?? [];
        setEnvironments(envs);
        if (envs.length > 0) {
          setSelectedEnvId(envs[0].id);
        } else {
          setIsLoadingApps(false);
        }
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : "Failed to load project");
        setIsLoadingApps(false);
      })
      .finally(() => setIsLoadingEnvs(false));
  }, [projectId]);

  // Load apps when env changes
  useEffect(() => {
    if (!selectedEnvId) return;
    appsApi
      .list(projectId, selectedEnvId)
      .then((data) => setApps(data.apps ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load applications"))
      .finally(() => setIsLoadingApps(false));
  }, [projectId, selectedEnvId]);

  function handleFormChange(field: keyof CreateAppForm, value: string | number) {
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  function handleEnvironmentChange(envId: string) {
    setIsLoadingApps(true);
    setError(null);
    setSelectedEnvId(envId);
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
      setTimeout(() => {
        router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
      }, 2000);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Failed to create application");
    } finally {
      setIsSubmitting(false);
    }
  }

  const selectedEnv = environments.find((e) => e.id === selectedEnvId);
  const isVMEnvironment = selectedEnv?.runtime === "vm";

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
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Link href="/projects" className="hover:text-gray-700">Projects</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}`} className="hover:text-gray-700">Overview</Link>
            <span>/</span>
            <span className="text-gray-900">Applications</span>
          </div>
          <h1 className="mt-2 text-2xl font-bold text-gray-900">Applications</h1>
          <p className="mt-0.5 text-sm text-gray-500">Managed application workloads</p>
        </div>
        <button
          onClick={() => setIsModalOpen(true)}
          disabled={!selectedEnvId || isVMEnvironment}
          className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          Create App
        </button>
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {/* Environment tabs */}
      {environments.length > 0 && (
        <div className="mb-6 flex gap-1 rounded-lg border border-gray-200 bg-gray-50 p-1 w-fit">
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
              <span>{env.name}</span>
              <span className={`ml-2 rounded-full px-1.5 py-0.5 text-[10px] font-semibold ${
                env.runtime === "vm" ? "bg-amber-100 text-amber-700" : "bg-slate-100 text-slate-600"
              }`}>
                {env.runtime === "vm" ? "VM" : "K8s"}
              </span>
            </button>
          ))}
        </div>
      )}

      {isVMEnvironment && (
        <div className="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
          This environment runs on the VM track. AppServer lifecycle is available now; VM app deployment is intentionally blocked until the Portainer stack worker is wired.
          <Link href={`/projects/${projectId}/app-servers`} className="ml-1 font-medium underline">
            Manage AppServers
          </Link>
        </div>
      )}

      {/* Apps list */}
      {isLoadingApps ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : apps.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M5 12h14M12 5l7 7-7 7" />
          </svg>
          <p className="text-sm font-medium text-gray-500">No applications in {selectedEnv?.name ?? "this environment"}</p>
          <button
            onClick={() => setIsModalOpen(true)}
            disabled={isVMEnvironment}
            className="mt-4 text-sm text-blue-600 hover:text-blue-700"
          >
            {isVMEnvironment ? "VM app deployment is not enabled yet" : "Create your first application →"}
          </button>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((app) => {
            const summary = app.summary_json as unknown as AppSummary;
            return (
              <Link
                key={app.id}
                href={`/projects/${projectId}/apps/${app.name}?envId=${selectedEnvId}`}
                className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm hover:border-blue-200 hover:shadow-md transition-all"
              >
                <div className="mb-3 flex items-start justify-between">
                  <div className="min-w-0 flex-1">
                    <p className="font-mono text-sm font-semibold text-gray-900">{app.name}</p>
                    <p className="mt-0.5 font-mono text-xs text-gray-400 truncate">{summary.image ?? "—"}</p>
                  </div>
                  <PhaseBadge phase={app.phase} />
                </div>
                <div className="flex items-center gap-3 text-xs text-gray-400">
                  <span>{summary.profile ?? "small"}</span>
                  <span>·</span>
                  <span>{summary.replicas ?? 2} replicas</span>
                </div>
                <p className="mt-2 text-xs text-gray-400">
                  Synced {timeAgo(app.last_synced_at)}
                </p>
              </Link>
            );
          })}
        </div>
      )}

      {/* Create App Modal */}
      <Modal
        isOpen={isModalOpen}
        onClose={() => {
          setIsModalOpen(false);
          setSubmitError(null);
        }}
        title="Create Application"
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">
              Name <span className="text-gray-400 font-normal">(Kubernetes resource name)</span>
            </label>
            <input
              type="text"
              required
              value={form.name}
              onChange={(e) => handleFormChange("name", e.target.value)}
              placeholder="my-service"
              pattern="[a-z0-9-]+"
              title="Lowercase letters, numbers, and hyphens only"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">Image</label>
            <input
              type="text"
              required
              value={form.image}
              onChange={(e) => handleFormChange("image", e.target.value)}
              placeholder="ghcr.io/org/service:v1.0.0"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700">Port</label>
              <input
                type="number"
                required
                min={1}
                max={65535}
                value={form.port}
                onChange={(e) => handleFormChange("port", parseInt(e.target.value, 10))}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">Replicas</label>
              <input
                type="number"
                required
                min={1}
                max={10}
                value={form.replicas}
                onChange={(e) => handleFormChange("replicas", parseInt(e.target.value, 10) || 1)}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">Profile</label>
            <select
              value={form.profile}
              onChange={(e) => handleFormChange("profile", e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="small">small</option>
              <option value="medium">medium</option>
              <option value="large">large</option>
            </select>
          </div>

          {submitError && (
            <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
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
                "Create App"
              )}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
