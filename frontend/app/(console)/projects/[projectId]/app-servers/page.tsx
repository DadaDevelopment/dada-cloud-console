"use client";
import { FormEvent, useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { appServersApi } from "@/lib/api";
import type { AppServer, AppServerStatus } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";

interface CreateAppServerForm {
  name: string;
  flavor: string;
  os_image: string;
  region: string;
  ssh_key_name: string;
}

const statusTone: Record<AppServerStatus, string> = {
  Provisioning: "bg-blue-100 text-blue-800",
  WaitingForAgent: "bg-amber-100 text-amber-800",
  Ready: "bg-green-100 text-green-800",
  Deleting: "bg-orange-100 text-orange-800",
  Deleted: "bg-gray-200 text-gray-700",
  Failed: "bg-red-100 text-red-800",
};

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
  return `${Math.floor(diffHours / 24)}d ago`;
}

function AppServerStatusBadge({ status }: { status: AppServerStatus }) {
  return <Badge className={statusTone[status] ?? "bg-gray-100 text-gray-800"}>{status}</Badge>;
}

export default function AppServersPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const router = useRouter();

  const [servers, setServers] = useState<AppServer[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [deletingName, setDeletingName] = useState<string | null>(null);
  const [form, setForm] = useState<CreateAppServerForm>({
    name: "",
    flavor: "small",
    os_image: "ubuntu-22.04",
    region: "ru1",
    ssh_key_name: "dada-agent",
  });

  async function loadServers() {
    setError(null);
    try {
      const data = await appServersApi.list(projectId);
      setServers(data.app_servers ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load app servers");
    } finally {
      setIsLoading(false);
    }
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch-on-mount pattern used by console pages; state updates happen after the API promise settles.
    void loadServers();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- loadServers closes over stable projectId for this fetch-on-mount pattern.
  }, [projectId]);

  function handleFormChange(field: keyof CreateAppServerForm, value: string) {
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const result = await appServersApi.create(projectId, form);
      setIsModalOpen(false);
      setForm({ name: "", flavor: "small", os_image: "ubuntu-22.04", region: "ru1", ssh_key_name: "dada-agent" });
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Failed to create app server");
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleDelete(serverName: string) {
    setDeletingName(serverName);
    setError(null);
    try {
      const result = await appServersApi.remove(projectId, serverName);
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete app server");
    } finally {
      setDeletingName(null);
    }
  }

  if (isLoading) {
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
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Link href="/projects" className="hover:text-gray-700">Projects</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}`} className="hover:text-gray-700">Overview</Link>
            <span>/</span>
            <span className="text-gray-900">App Servers</span>
          </div>
          <h1 className="mt-2 text-2xl font-bold text-gray-900">App Servers</h1>
          <p className="mt-0.5 text-sm text-gray-500">Dedicated VM hosts for Docker Compose workloads.</p>
        </div>
        <button
          onClick={() => setIsModalOpen(true)}
          className="inline-flex items-center gap-2 rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 transition-colors"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          Create AppServer
        </button>
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {servers.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M3 15.75V8.25A2.25 2.25 0 015.25 6h13.5A2.25 2.25 0 0121 8.25v7.5A2.25 2.25 0 0118.75 18H5.25A2.25 2.25 0 013 15.75zM7 9h10M7 12h4" />
          </svg>
          <p className="text-sm font-medium text-gray-500">No AppServers yet</p>
          <button onClick={() => setIsModalOpen(true)} className="mt-4 text-sm text-amber-700 hover:text-amber-800">
            Provision the first VM host →
          </button>
        </div>
      ) : (
        <div className="overflow-hidden rounded-xl border border-gray-200 bg-white">
          <div className="grid grid-cols-[1.2fr_1fr_1fr_1fr_auto] gap-4 border-b border-gray-100 bg-gray-50 px-5 py-3 text-xs font-semibold uppercase tracking-wide text-gray-500">
            <span>Name</span>
            <span>Status</span>
            <span>VM IP</span>
            <span>Portainer</span>
            <span className="text-right">Actions</span>
          </div>
          {servers.map((server) => (
            <div key={server.id} className="grid grid-cols-[1.2fr_1fr_1fr_1fr_auto] items-center gap-4 border-b border-gray-100 px-5 py-4 last:border-0">
              <div className="min-w-0">
                <p className="font-mono text-sm font-semibold text-gray-900">{server.name}</p>
                <p className="mt-0.5 text-xs text-gray-400">Updated {timeAgo(server.updated_at)}</p>
                {server.error_message && <p className="mt-1 text-xs text-red-600">{server.error_message}</p>}
              </div>
              <AppServerStatusBadge status={server.status} />
              <span className="font-mono text-sm text-gray-600">{server.vm_ip ?? "—"}</span>
              <span className="font-mono text-sm text-gray-600">{server.portainer_endpoint_id ?? "—"}</span>
              <button
                onClick={() => void handleDelete(server.name)}
                disabled={deletingName === server.name || server.status === "Deleting"}
                className="rounded-lg border border-red-200 px-3 py-1.5 text-sm font-medium text-red-700 hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {deletingName === server.name ? "Deleting..." : "Delete"}
              </button>
            </div>
          ))}
        </div>
      )}

      <Modal
        isOpen={isModalOpen}
        onClose={() => {
          setIsModalOpen(false);
          setSubmitError(null);
        }}
        title="Create AppServer"
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">Name</label>
            <input
              type="text"
              required
              value={form.name}
              onChange={(e) => handleFormChange("name", e.target.value)}
              placeholder="client-a-prod-1"
              className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
            />
            <p className="mt-1 text-xs text-gray-400">Lowercase DNS-style name used for the VM and Portainer endpoint.</p>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-gray-700">Flavor</label>
              <select
                value={form.flavor}
                onChange={(e) => handleFormChange("flavor", e.target.value)}
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              >
                <option value="small">small</option>
                <option value="medium">medium</option>
                <option value="large">large</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">Region</label>
              <select
                value={form.region}
                onChange={(e) => handleFormChange("region", e.target.value)}
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              >
                <option value="ru1">ru1</option>
                <option value="ru2">ru2</option>
                <option value="kz1">kz1</option>
                <option value="eu1">eu1</option>
              </select>
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-gray-700">OS image</label>
              <input
                type="text"
                value={form.os_image}
                onChange={(e) => handleFormChange("os_image", e.target.value)}
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">SSH key name</label>
              <input
                type="text"
                value={form.ssh_key_name}
                onChange={(e) => handleFormChange("ssh_key_name", e.target.value)}
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              />
            </div>
          </div>

          {submitError && (
            <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
              {submitError}
            </div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setIsModalOpen(false)}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50"
            >
              {isSubmitting ? "Creating..." : "Create AppServer"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
